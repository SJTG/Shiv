// Package render convierte un CV en HTML con formato Harvard y ese HTML en PDF.
package render

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"cvtailor/internal/cv"
)

//go:embed templates/harvard.html
var harvardSrc string

var tmpl = template.Must(template.New("harvard").
	Funcs(template.FuncMap{"join": strings.Join}).
	Parse(harvardSrc))

type labels struct{ Summary, Education, Experience, Projects, Skills string }

var (
	labelsES = labels{"Resumen", "Educación", "Experiencia", "Proyectos", "Habilidades"}
	labelsEN = labels{"Summary", "Education", "Experience", "Projects", "Skills"}
)

type view struct {
	Lang    string
	L       labels
	CV      *cv.CV
	Contact []string
}

// HTML renderiza el CV con la plantilla Harvard (una columna, texto sobre
// blanco). lang es "es" o "en" y solo cambia los títulos de las secciones.
func HTML(c *cv.CV, lang string) ([]byte, error) {
	v := view{Lang: "es", L: labelsES, CV: c, Contact: contactParts(c)}
	if strings.EqualFold(lang, "en") {
		v.Lang, v.L = "en", labelsEN
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, v); err != nil {
		return nil, fmt.Errorf("renderizar la plantilla: %w", err)
	}
	return buf.Bytes(), nil
}

func contactParts(c *cv.CV) []string {
	b := c.Basics
	var parts []string
	for _, p := range []string{b.Location, b.Phone, b.Email} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	for _, l := range b.Links {
		switch {
		case l.Label != "" && l.URL != "":
			parts = append(parts, l.Label+": "+l.URL)
		case l.URL != "":
			parts = append(parts, l.URL)
		case l.Label != "":
			parts = append(parts, l.Label)
		}
	}
	return parts
}

// PDFRenderer convierte un HTML ya renderizado en un PDF. Es una interfaz para
// poder cambiar la implementación (por ejemplo, a chromedp) o simularla en tests.
type PDFRenderer interface {
	PDF(ctx context.Context, html []byte) ([]byte, error)
}

// ErrNoChrome indica que no se encontró un Chrome o Chromium instalado.
var ErrNoChrome = errors.New("no se encontró Chrome ni Chromium: instalalo o indicá la ruta en CHROME_PATH")

// FindChrome busca el ejecutable de Chrome/Chromium. Si custom no está vacío,
// es la ruta a usar y debe existir.
func FindChrome(custom string) (string, error) {
	if custom != "" {
		if _, err := os.Stat(custom); err != nil {
			return "", fmt.Errorf("CHROME_PATH=%q no es accesible: %w", custom, err)
		}
		return custom, nil
	}
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome", "chrome-headless-shell"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	var fixed []string
	switch runtime.GOOS {
	case "darwin":
		fixed = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		}
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base != "" {
				fixed = append(fixed, filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"))
			}
		}
	}
	for _, p := range fixed {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", ErrNoChrome
}

// ChromePDF imprime HTML a PDF llamando a Chrome headless por línea de comandos.
type ChromePDF struct {
	Path      string
	NoSandbox bool
	Timeout   time.Duration
}

// NewChromePDF crea el renderer. path puede estar vacío para autodetectar.
// Chrome se niega a correr como root sin --no-sandbox, así que se agrega solo
// en ese caso (o si CHROME_NO_SANDBOX=1).
func NewChromePDF(path string) (*ChromePDF, error) {
	p, err := FindChrome(path)
	if err != nil {
		return nil, err
	}
	return &ChromePDF{
		Path:      p,
		NoSandbox: os.Geteuid() == 0 || os.Getenv("CHROME_NO_SANDBOX") == "1",
		Timeout:   60 * time.Second,
	}, nil
}

// PDF implementa PDFRenderer.
func (c *ChromePDF) PDF(ctx context.Context, html []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "cvtailor-*")
	if err != nil {
		return nil, fmt.Errorf("crear carpeta temporal: %w", err)
	}
	defer os.RemoveAll(dir)

	in := filepath.Join(dir, "cv.html")
	out := filepath.Join(dir, "cv.pdf")
	if err := os.WriteFile(in, html, 0o600); err != nil {
		return nil, fmt.Errorf("escribir el HTML temporal: %w", err)
	}

	var args []string
	// chrome-headless-shell ya es headless y no acepta (ni necesita) el flag.
	if !strings.Contains(strings.ToLower(filepath.Base(c.Path)), "headless-shell") {
		args = append(args, "--headless=new")
	}
	args = append(args,
		"--disable-gpu",
		"--no-first-run",
		"--no-default-browser-check",
		"--user-data-dir="+filepath.Join(dir, "profile"),
		"--no-pdf-header-footer",
		"--print-to-pdf="+out,
	)
	if c.NoSandbox {
		args = append(args, "--no-sandbox")
	}
	args = append(args, fileURL(in))

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.Path, args...)
	cmd.WaitDelay = 5 * time.Second // Chrome lanza procesos hijos que pueden demorar en cerrar los pipes.
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("Chrome tardó más de %s en generar el PDF", timeout)
	}
	pdf, readErr := os.ReadFile(out)
	if readErr != nil {
		msg := strings.TrimSpace(string(output))
		if len(msg) > 600 {
			msg = msg[len(msg)-600:]
		}
		if err != nil {
			return nil, fmt.Errorf("Chrome falló al generar el PDF: %w: %s", err, msg)
		}
		return nil, fmt.Errorf("Chrome terminó pero no generó el PDF: %s", msg)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return nil, errors.New("el archivo generado por Chrome no es un PDF válido")
	}
	return pdf, nil
}

func fileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // rutas de Windows: C:/Users/... -> /C:/Users/...
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}
