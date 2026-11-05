package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cvtailor/internal/cv"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCVRoundTripAndNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.LoadCV(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, quería ErrNotFound", err)
	}
	want := cv.Sample()
	if err := s.SaveCV(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadCV()
	if err != nil {
		t.Fatal(err)
	}
	if got.Basics.Name != want.Basics.Name || got.Experience[0].Bullets[1].ID != "exp-1-b2" {
		t.Fatalf("el CV leído no coincide: %+v", got.Basics)
	}
}

func TestSaveCVLeavesNoTempFiles(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 3; i++ {
		if err := s.SaveCV(cv.Sample()); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("quedó un temporal: %s", e.Name())
		}
	}
}

func TestVersionIDsAndListing(t *testing.T) {
	s := newStore(t)
	old := &cv.Version{CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Job: cv.Job{Title: "Ingeniera Backend", Company: "Peñaflor S.A."}, CV: *cv.Sample()}
	newer := &cv.Version{CreatedAt: time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC),
		Job: cv.Job{Title: "Data", Company: "Otra"}, CV: *cv.Sample()}
	for _, v := range []*cv.Version{old, newer} {
		if err := s.SaveVersion(v); err != nil {
			t.Fatal(err)
		}
	}
	if old.ID != "2026-01-02-030405-penaflor-s-a-ingeniera-backend" {
		t.Fatalf("id = %q", old.ID)
	}
	list, problems := s.ListVersions()
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(list) != 2 || list[0].ID != newer.ID {
		t.Fatalf("el listado debería empezar por la más nueva: %+v", list)
	}
	got, err := s.LoadVersion(old.ID)
	if err != nil || got.Job.Company != "Peñaflor S.A." {
		t.Fatalf("LoadVersion = %+v, %v", got, err)
	}
}

func TestVersionIDCollisionGetsSuffix(t *testing.T) {
	s := newStore(t)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a := &cv.Version{CreatedAt: at, CV: *cv.Sample()}
	b := &cv.Version{CreatedAt: at, CV: *cv.Sample()}
	if err := s.SaveVersion(a); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVersion(b); err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatalf("ids iguales: %q", a.ID)
	}
}

func TestSaveVersionNeverOverwrites(t *testing.T) {
	s := newStore(t)
	v := &cv.Version{ID: "fijo", CV: *cv.Sample()}
	if err := s.SaveVersion(v); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveVersion(&cv.Version{ID: "fijo", CV: *cv.Sample()}); err == nil {
		t.Fatal("debía rechazar pisar una versión existente")
	}
}

func TestBadVersionIDsRejected(t *testing.T) {
	s := newStore(t)
	for _, id := range []string{"../cv", "..", "a/b", `a\b`, "", ".oculto", "a b", strings.Repeat("x", 200)} {
		if _, err := s.LoadVersion(id); !errors.Is(err, ErrBadID) {
			t.Errorf("LoadVersion(%q) err = %v, quería ErrBadID", id, err)
		}
	}
	if _, err := s.LoadVersion("no-existe"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, quería ErrNotFound", err)
	}
}

func TestListVersionsReportsBrokenFiles(t *testing.T) {
	s := newStore(t)
	if err := s.SaveVersion(&cv.Version{CV: *cv.Sample()}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, "versions", "roto.json"), []byte("{no es json"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, problems := s.ListVersions()
	if len(list) != 1 || len(problems) != 1 || !strings.Contains(problems[0].Error(), "roto.json") {
		t.Fatalf("list = %+v problems = %v", list, problems)
	}
}

func TestBackupCV(t *testing.T) {
	s := newStore(t)
	if err := s.BackupCV(); err != nil { // sin CV no hace nada
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "cv.backup.json")); err == nil {
		t.Fatal("no debía crear respaldo sin CV")
	}
	first := cv.Sample()
	if err := s.SaveCV(first); err != nil {
		t.Fatal(err)
	}
	if err := s.BackupCV(); err != nil {
		t.Fatal(err)
	}
	changed := first.Clone()
	changed.Basics.Name = "Otra Persona"
	if err := s.SaveCV(changed); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "cv.backup.json"))
	if err != nil || !strings.Contains(string(data), "Ada Ejemplo") {
		t.Fatalf("el respaldo no conserva el CV anterior: %v", err)
	}
}
