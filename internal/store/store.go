// Package store guarda el CV maestro y las versiones como archivos JSON en disco.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"cvtailor/internal/cv"
)

// ErrNotFound se devuelve cuando el CV o la versión pedida no existe.
var ErrNotFound = errors.New("no encontrado")

// ErrBadID se devuelve cuando un ID de versión tiene caracteres no permitidos.
var ErrBadID = errors.New("id de versión inválido")

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,120}$`)

// Store lee y escribe en dir/cv.json y dir/versions/<id>.json.
type Store struct {
	dir string
	mu  sync.Mutex
}

// New crea las carpetas necesarias y devuelve el Store.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "versions"), 0o700); err != nil {
		return nil, fmt.Errorf("crear la carpeta de datos %q: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) cvPath() string { return filepath.Join(s.dir, "cv.json") }

func (s *Store) versionPath(id string) (string, error) {
	if !idRe.MatchString(id) || strings.Contains(id, "..") {
		return "", ErrBadID
	}
	return filepath.Join(s.dir, "versions", id+".json"), nil
}

// LoadCV lee el CV maestro. Devuelve ErrNotFound si todavía no se importó.
func (s *Store) LoadCV() (*cv.CV, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c cv.CV
	if err := readJSON(s.cvPath(), &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveCV guarda el CV maestro de forma atómica.
func (s *Store) SaveCV(c *cv.CV) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.cvPath(), c)
}

// BackupCV copia el CV maestro actual a cv.backup.json (pisando el respaldo
// anterior). No hace nada si todavía no hay CV. Se usa antes de reemplazarlo.
func (s *Store) BackupCV() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.cvPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("leer cv.json para el respaldo: %w", err)
	}
	return writeFileAtomic(filepath.Join(s.dir, "cv.backup.json"), data)
}

// SaveVersion guarda una versión. Si v.ID está vacío lo genera a partir de la
// fecha y del puesto/empresa; nunca pisa una versión existente.
func (s *Store) SaveVersion(v *cv.Version) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}
	if v.ID == "" {
		base := v.CreatedAt.Format("2006-01-02-150405")
		if slug := slugify(v.Job.Company + " " + v.Job.Title); slug != "" {
			base += "-" + slug
		}
		v.ID = base
		for n := 2; s.exists(v.ID); n++ {
			v.ID = fmt.Sprintf("%s-%d", base, n)
		}
	}
	path, err := s.versionPath(v.ID)
	if err != nil {
		return err
	}
	if s.exists(v.ID) {
		return fmt.Errorf("la versión %q ya existe", v.ID)
	}
	return writeJSON(path, v)
}

func (s *Store) exists(id string) bool {
	path, err := s.versionPath(id)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// LoadVersion lee una versión por ID.
func (s *Store) LoadVersion(id string) (*cv.Version, error) {
	path, err := s.versionPath(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var v cv.Version
	if err := readJSON(path, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// VersionSummary es lo mínimo para listar versiones.
type VersionSummary struct {
	ID        string
	CreatedAt time.Time
	Title     string
	Company   string
}

// ListVersions devuelve las versiones, la más nueva primero. Los archivos
// ilegibles se informan en el segundo valor y no cortan el listado.
func (s *Store) ListVersions() ([]VersionSummary, []error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(s.dir, "versions"))
	if err != nil {
		return nil, []error{err}
	}
	var out []VersionSummary
	var problems []error
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		var v cv.Version
		if err := readJSON(filepath.Join(s.dir, "versions", name), &v); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", name, err))
			continue
		}
		out = append(out, VersionSummary{ID: v.ID, CreatedAt: v.CreatedAt, Title: v.Job.Title, Company: v.Job.Company})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, problems
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("leer %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s no es un JSON válido: %w", path, err)
	}
	return nil
}

// writeJSON escribe en un archivo temporal de la misma carpeta y lo renombra,
// así un corte a mitad de escritura nunca deja un archivo a medias.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("serializar JSON: %w", err)
	}
	return writeFileAtomic(path, data)
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("crear archivo temporal: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("escribir %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("sincronizar %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("cerrar %s: %w", path, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		cleanup()
		return fmt.Errorf("permisos de %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("guardar %s: %w", path, err)
	}
	return nil
}

// slugify deja solo letras ASCII, números y guiones, para usar en nombres de archivo.
func slugify(s string) string {
	var b strings.Builder
	prevDash := true
	for _, r := range strings.ToLower(s) {
		r = foldAccent(r)
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteRune('-')
			prevDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-")
	}
	return out
}

func foldAccent(r rune) rune {
	switch r {
	case 'á', 'à', 'ä', 'â':
		return 'a'
	case 'é', 'è', 'ë', 'ê':
		return 'e'
	case 'í', 'ì', 'ï', 'î':
		return 'i'
	case 'ó', 'ò', 'ö', 'ô':
		return 'o'
	case 'ú', 'ù', 'ü', 'û':
		return 'u'
	case 'ñ':
		return 'n'
	}
	return r
}
