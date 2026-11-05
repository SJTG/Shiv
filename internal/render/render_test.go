package render

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"cvtailor/internal/cv"
)

func TestHTMLContainsContent(t *testing.T) {
	out, err := HTML(cv.Sample(), "es")
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	for _, want := range []string{
		"Ada Ejemplo", "Experiencia", "Educación", "Habilidades", "Proyectos", "Resumen",
		"Acme Software", "2023 - Actualidad", "Desarrollé servicios REST en Go",
		"Lenguajes:", "Go, Python, SQL", "linkedin.com/in/ada-ejemplo",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("el HTML no contiene %q", want)
		}
	}
}

func TestHTMLEnglishLabels(t *testing.T) {
	out, err := HTML(cv.Sample(), "en")
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	if !strings.Contains(html, "Experience") || strings.Contains(html, "Experiencia") {
		t.Fatal("los títulos no están en inglés")
	}
}

func TestHTMLEscapesUserText(t *testing.T) {
	c := cv.Sample().Clone()
	c.Basics.Name = `<script>alert(1)</script>`
	c.Experience[0].Bullets[0].Text = `<img src=x onerror=alert(1)>`
	out, err := HTML(c, "es")
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	if strings.Contains(html, "<script>alert") || strings.Contains(html, "<img src=x") {
		t.Fatal("el texto del CV no se escapó")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("falta el texto escapado")
	}
}

func TestHTMLOmitsEmptySections(t *testing.T) {
	c := &cv.CV{Basics: cv.Basics{Name: "Solo Nombre"}}
	if err := c.Prepare(); err != nil {
		t.Fatal(err)
	}
	out, err := HTML(c, "es")
	if err != nil {
		t.Fatal(err)
	}
	html := string(out)
	if strings.Contains(html, "<h2>") {
		t.Fatal("no debería haber secciones vacías")
	}
}

func TestFileURL(t *testing.T) {
	if got, want := fileURL("/tmp/a b/cv.html"), "file:///tmp/a%20b/cv.html"; got != want {
		t.Fatalf("fileURL = %q, quería %q", got, want)
	}
}

func TestFindChromeCustomMissing(t *testing.T) {
	if _, err := FindChrome("/ruta/que/no/existe/chrome"); err == nil {
		t.Fatal("debía fallar con una ruta inexistente")
	}
}

func TestChromePDF(t *testing.T) {
	r, err := NewChromePDF(os.Getenv("CHROME_PATH"))
	if err != nil {
		t.Skipf("sin Chrome en este entorno: %v", err)
	}
	r.Timeout = 30 * time.Second
	html, err := HTML(cv.Sample(), "es")
	if err != nil {
		t.Fatal(err)
	}
	pdf, err := r.PDF(context.Background(), html)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 1000 {
		t.Fatalf("PDF inválido: %d bytes", len(pdf))
	}
	// Con CVTAILOR_DUMP_PDF=ruta.pdf se guarda el PDF para revisarlo a ojo.
	if p := os.Getenv("CVTAILOR_DUMP_PDF"); p != "" {
		if err := os.WriteFile(p, pdf, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Si hay pdftotext, comprobamos que el texto sea seleccionable (RF6).
	if bin, err := exec.LookPath("pdftotext"); err == nil {
		tmp := t.TempDir() + "/out.pdf"
		if err := os.WriteFile(tmp, pdf, 0o600); err != nil {
			t.Fatal(err)
		}
		txt, err := exec.Command(bin, tmp, "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Ada Ejemplo", "Acme Software", "Postgres"} {
			if !strings.Contains(string(txt), want) {
				t.Errorf("el texto del PDF no contiene %q", want)
			}
		}
	}
}

func TestChromePDFFailsClearlyWithBadBinary(t *testing.T) {
	r := &ChromePDF{Path: "/bin/false"}
	if _, err := r.PDF(context.Background(), []byte("<html></html>")); err == nil {
		t.Fatal("debía fallar")
	}
}
