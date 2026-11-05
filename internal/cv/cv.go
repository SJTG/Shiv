// Package cv contiene el modelo de dominio: el CV, las versiones por oferta y
// las reglas que impiden que un cambio proponga contenido inventado.
package cv

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CV es el CV maestro. Se guarda tal cual como data/cv.json.
type CV struct {
	Basics     Basics       `json:"basics"`
	Summary    string       `json:"summary"`
	Education  []Education  `json:"education"`
	Experience []Experience `json:"experience"`
	Projects   []Project    `json:"projects"`
	Skills     []SkillGroup `json:"skills"`
	// Unparsed guarda el texto que la IA no supo ubicar, para revisarlo a mano.
	Unparsed []string `json:"unparsed"`
}

type Basics struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Location string `json:"location"`
	Links    []Link `json:"links"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Education struct {
	ID          string   `json:"id"`
	Institution string   `json:"institution"`
	Degree      string   `json:"degree"`
	Period      string   `json:"period"`
	Details     []string `json:"details"`
}

type Bullet struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type Experience struct {
	ID      string   `json:"id"`
	Company string   `json:"company"`
	Role    string   `json:"role"`
	Period  string   `json:"period"`
	Bullets []Bullet `json:"bullets"`
}

type Project struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Bullets []Bullet `json:"bullets"`
}

type SkillGroup struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Items    []string `json:"items"`
}

// ErrEmpty indica que el CV no tiene contenido útil.
var ErrEmpty = errors.New("el CV está vacío: no tiene nombre, resumen, educación, experiencia, proyectos ni skills")

// IsEmpty informa si el CV no tiene ningún contenido.
func (c *CV) IsEmpty() bool {
	return c.Basics.Name == "" && c.Summary == "" &&
		len(c.Education) == 0 && len(c.Experience) == 0 &&
		len(c.Projects) == 0 && len(c.Skills) == 0
}

// Normalize recorta espacios, descarta entradas totalmente vacías y deja los
// slices no nulos (para que el JSON salga con [] y no con null).
func (c *CV) Normalize() {
	b := &c.Basics
	b.Name = strings.TrimSpace(b.Name)
	b.Email = strings.TrimSpace(b.Email)
	b.Phone = strings.TrimSpace(b.Phone)
	b.Location = strings.TrimSpace(b.Location)
	links := []Link{}
	for _, l := range b.Links {
		l.Label, l.URL = strings.TrimSpace(l.Label), strings.TrimSpace(l.URL)
		if l.Label != "" || l.URL != "" {
			links = append(links, l)
		}
	}
	b.Links = links
	c.Summary = strings.TrimSpace(c.Summary)

	edu := []Education{}
	for _, e := range c.Education {
		e.Institution, e.Degree, e.Period = strings.TrimSpace(e.Institution), strings.TrimSpace(e.Degree), strings.TrimSpace(e.Period)
		e.Details = cleanStrings(e.Details)
		if e.Institution != "" || e.Degree != "" || e.Period != "" || len(e.Details) > 0 {
			edu = append(edu, e)
		}
	}
	c.Education = edu

	exp := []Experience{}
	for _, e := range c.Experience {
		e.Company, e.Role, e.Period = strings.TrimSpace(e.Company), strings.TrimSpace(e.Role), strings.TrimSpace(e.Period)
		e.Bullets = cleanBullets(e.Bullets)
		if e.Company != "" || e.Role != "" || e.Period != "" || len(e.Bullets) > 0 {
			exp = append(exp, e)
		}
	}
	c.Experience = exp

	proj := []Project{}
	for _, p := range c.Projects {
		p.Name = strings.TrimSpace(p.Name)
		p.Bullets = cleanBullets(p.Bullets)
		if p.Name != "" || len(p.Bullets) > 0 {
			proj = append(proj, p)
		}
	}
	c.Projects = proj

	skills := []SkillGroup{}
	for _, s := range c.Skills {
		s.Category = strings.TrimSpace(s.Category)
		s.Items = cleanStrings(s.Items)
		if s.Category != "" || len(s.Items) > 0 {
			skills = append(skills, s)
		}
	}
	c.Skills = skills

	c.Unparsed = cleanStrings(c.Unparsed)
}

// AssignIDs regenera todos los IDs por posición: edu-1, exp-1, exp-1-b1,
// proj-1, proj-1-b1, skills-1. Los cambios apuntan a estos IDs.
// Los IDs de un CV guardado no cambian mientras no se edite el maestro.
func (c *CV) AssignIDs() {
	for i := range c.Education {
		c.Education[i].ID = fmt.Sprintf("edu-%d", i+1)
	}
	for i := range c.Experience {
		e := &c.Experience[i]
		e.ID = fmt.Sprintf("exp-%d", i+1)
		for j := range e.Bullets {
			e.Bullets[j].ID = fmt.Sprintf("%s-b%d", e.ID, j+1)
		}
	}
	for i := range c.Projects {
		p := &c.Projects[i]
		p.ID = fmt.Sprintf("proj-%d", i+1)
		for j := range p.Bullets {
			p.Bullets[j].ID = fmt.Sprintf("%s-b%d", p.ID, j+1)
		}
	}
	for i := range c.Skills {
		c.Skills[i].ID = fmt.Sprintf("skills-%d", i+1)
	}
}

// Prepare normaliza, asigna IDs y valida. Es lo que se llama antes de guardar.
func (c *CV) Prepare() error {
	c.Normalize()
	c.AssignIDs()
	if c.IsEmpty() {
		return ErrEmpty
	}
	return nil
}

// Clone devuelve una copia profunda.
func (c *CV) Clone() *CV {
	data, err := json.Marshal(c)
	if err != nil {
		panic(err) // un CV siempre es serializable
	}
	var out CV
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return &out
}

func cleanStrings(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func cleanBullets(in []Bullet) []Bullet {
	out := []Bullet{}
	for _, b := range in {
		if b.Text = strings.TrimSpace(b.Text); b.Text != "" {
			out = append(out, b)
		}
	}
	return out
}
