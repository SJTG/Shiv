package cv

import (
	"errors"
	"strings"
	"testing"
)

func TestAssignIDs(t *testing.T) {
	c := Sample()
	if got := c.Experience[0].ID; got != "exp-1" {
		t.Fatalf("exp id = %q", got)
	}
	if got := c.Experience[0].Bullets[1].ID; got != "exp-1-b2" {
		t.Fatalf("bullet id = %q", got)
	}
	if got := c.Projects[0].Bullets[0].ID; got != "proj-1-b1" {
		t.Fatalf("project bullet id = %q", got)
	}
	if got := c.Skills[1].ID; got != "skills-2" {
		t.Fatalf("skills id = %q", got)
	}
	if got := c.Education[0].ID; got != "edu-1" {
		t.Fatalf("edu id = %q", got)
	}
}

func TestNormalizeDropsBlankAndKeepsNonNilSlices(t *testing.T) {
	c := &CV{
		Basics:     Basics{Name: "  Ana  "},
		Experience: []Experience{{}, {Company: " X ", Bullets: []Bullet{{Text: "  "}, {Text: " hizo algo "}}}},
		Skills:     []SkillGroup{{Category: " ", Items: []string{"", " "}}},
		Unparsed:   []string{"", "  texto suelto  "},
	}
	c.Normalize()
	if c.Basics.Name != "Ana" {
		t.Fatalf("name = %q", c.Basics.Name)
	}
	if len(c.Experience) != 1 || c.Experience[0].Company != "X" {
		t.Fatalf("experience = %+v", c.Experience)
	}
	if len(c.Experience[0].Bullets) != 1 || c.Experience[0].Bullets[0].Text != "hizo algo" {
		t.Fatalf("bullets = %+v", c.Experience[0].Bullets)
	}
	if c.Skills == nil || len(c.Skills) != 0 || c.Education == nil || c.Projects == nil || c.Basics.Links == nil {
		t.Fatal("los slices vacíos deben ser no nulos")
	}
	if len(c.Unparsed) != 1 || c.Unparsed[0] != "texto suelto" {
		t.Fatalf("unparsed = %+v", c.Unparsed)
	}
}

func TestPrepareEmpty(t *testing.T) {
	if err := (&CV{}).Prepare(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("err = %v, quería ErrEmpty", err)
	}
}

func TestCloneIsIndependent(t *testing.T) {
	a := Sample()
	b := a.Clone()
	b.Experience[0].Bullets[0].Text = "otro"
	b.Skills[0].Items[0] = "Rust"
	if a.Experience[0].Bullets[0].Text == "otro" || a.Skills[0].Items[0] == "Rust" {
		t.Fatal("el clon comparte memoria con el original")
	}
}

func TestValidateChanges(t *testing.T) {
	c := Sample()
	tests := []struct {
		name   string
		change Change
		wantOK bool
		why    string
	}{
		{
			name: "intercambio válido en un bullet",
			change: Change{TargetID: "exp-1-b2", Before: "consultas a Postgres", After: "consultas a PostgreSQL",
				Keyword: "PostgreSQL"},
			wantOK: true,
		},
		{
			name: "intercambio válido en skills",
			change: Change{TargetID: "skills-2", Before: "Postgres", After: "PostgreSQL",
				Keyword: "PostgreSQL"},
			wantOK: true,
		},
		{
			name: "la primera palabra en mayúscula no cuenta como nombre propio",
			change: Change{TargetID: "exp-1-b3", Before: "Coordiné las revisiones", After: "Lideré las revisiones",
				Keyword: "liderazgo"},
			wantOK: true,
		},
		{
			name:   "target inexistente",
			change: Change{TargetID: "exp-9-b1", Before: "x", After: "y", Keyword: "k"},
			why:    "no existe",
		},
		{
			name:   "before que no está en el CV",
			change: Change{TargetID: "exp-1-b1", Before: "microservicios en Java", After: "microservicios en Go", Keyword: "microservicios"},
			why:    "no aparece literal",
		},
		{
			name:   "agrega un número",
			change: Change{TargetID: "exp-1-b3", Before: "equipo de tres personas", After: "equipo de 3 personas", Keyword: "liderazgo"},
			why:    "números",
		},
		{
			name:   "agrega un nombre propio que no es la keyword",
			change: Change{TargetID: "exp-1-b1", Before: "servicios REST en Go", After: "servicios REST en Kubernetes", Keyword: "microservicios"},
			why:    "nombres propios",
		},
		{
			name:   "sin keyword",
			change: Change{TargetID: "exp-1-b1", Before: "servicios REST", After: "APIs REST"},
			why:    "keyword",
		},
		{
			name:   "no cambia nada",
			change: Change{TargetID: "exp-1-b1", Before: "servicios REST", After: "servicios REST", Keyword: "REST"},
			why:    "no modifica",
		},
		{
			name:   "after vacío",
			change: Change{TargetID: "exp-1-b1", Before: "servicios REST", After: "", Keyword: "REST"},
			why:    "vacío",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, dropped := ValidateChanges(c, []Change{tt.change})
			if tt.wantOK {
				if len(ok) != 1 || len(dropped) != 0 {
					t.Fatalf("debía pasar; dropped = %+v", dropped)
				}
				return
			}
			if len(ok) != 0 || len(dropped) != 1 {
				t.Fatalf("debía descartarse; ok = %+v", ok)
			}
			if !strings.Contains(dropped[0].Why, tt.why) {
				t.Fatalf("motivo = %q, quería que contenga %q", dropped[0].Why, tt.why)
			}
		})
	}
}

func TestApplyAccepted(t *testing.T) {
	c := Sample()
	changes := []Change{
		{TargetID: "exp-1-b2", Before: "consultas a Postgres", After: "consultas a PostgreSQL", Keyword: "PostgreSQL", Accepted: true},
		{TargetID: "skills-2", Before: "Postgres", After: "PostgreSQL", Keyword: "PostgreSQL", Accepted: true},
		{TargetID: "exp-1-b3", Before: "Coordiné las revisiones", After: "Lideré las revisiones", Keyword: "liderazgo", Accepted: false},
	}
	out, skipped := ApplyAccepted(c, changes)
	if len(skipped) != 0 {
		t.Fatalf("skipped = %+v", skipped)
	}
	if got := out.Experience[0].Bullets[1].Text; !strings.Contains(got, "PostgreSQL") {
		t.Fatalf("bullet = %q", got)
	}
	if got := out.Skills[1].Items[2]; got != "PostgreSQL" {
		t.Fatalf("skill = %q", got)
	}
	if got := out.Experience[0].Bullets[2].Text; !strings.Contains(got, "Coordiné") {
		t.Fatalf("un cambio no aceptado se aplicó: %q", got)
	}
	// El original no se toca.
	if got := c.Experience[0].Bullets[1].Text; strings.Contains(got, "PostgreSQL") {
		t.Fatalf("se modificó el CV original: %q", got)
	}
}

func TestApplyAcceptedSkipsConflictingChange(t *testing.T) {
	c := Sample()
	changes := []Change{
		{TargetID: "exp-1-b1", Before: "servicios REST en Go", After: "APIs REST en Go", Keyword: "APIs", Accepted: true},
		// Este "before" ya no existe después del primero.
		{TargetID: "exp-1-b1", Before: "servicios REST", After: "web services REST", Keyword: "web services", Accepted: true},
	}
	out, skipped := ApplyAccepted(c, changes)
	if len(skipped) != 1 {
		t.Fatalf("skipped = %+v", skipped)
	}
	if got := out.Experience[0].Bullets[0].Text; !strings.HasPrefix(got, "Desarrollé APIs REST en Go") {
		t.Fatalf("bullet = %q", got)
	}
}
