package cv

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Change es un intercambio puntual propuesto por la IA: reemplazar el texto
// literal Before por After dentro del elemento TargetID.
type Change struct {
	TargetID string `json:"target_id"`
	Before   string `json:"before"`
	After    string `json:"after"`
	Keyword  string `json:"keyword"`
	Reason   string `json:"reason"`
	Accepted bool   `json:"accepted"`
}

// Dropped es un cambio descartado, con el motivo, para mostrárselo a la persona.
type Dropped struct {
	Change Change `json:"change"`
	Why    string `json:"why"`
}

// texts devuelve punteros a los textos editables del elemento id. Un bullet o
// el resumen es un solo texto; un grupo de skills son sus items.
func (c *CV) texts(id string) []*string {
	if id == "summary" {
		return []*string{&c.Summary}
	}
	for i := range c.Experience {
		for j := range c.Experience[i].Bullets {
			if b := &c.Experience[i].Bullets[j]; b.ID == id {
				return []*string{&b.Text}
			}
		}
	}
	for i := range c.Projects {
		for j := range c.Projects[i].Bullets {
			if b := &c.Projects[i].Bullets[j]; b.ID == id {
				return []*string{&b.Text}
			}
		}
	}
	for i := range c.Skills {
		if c.Skills[i].ID == id {
			out := make([]*string, len(c.Skills[i].Items))
			for j := range c.Skills[i].Items {
				out[j] = &c.Skills[i].Items[j]
			}
			return out
		}
	}
	return nil
}

// TargetText devuelve el texto del elemento id, para mostrar el contexto de un
// cambio. En un grupo de skills son los items separados por coma.
func (c *CV) TargetText(id string) string {
	ts := c.texts(id)
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = *t
	}
	return strings.Join(parts, ", ")
}

// ValidateChanges aplica las reglas anti-invención a cada cambio contra el CV
// original. Devuelve los que pasan y los descartados con su motivo.
func ValidateChanges(c *CV, changes []Change) (ok []Change, dropped []Dropped) {
	ok, dropped = []Change{}, []Dropped{}
	for _, ch := range changes {
		if err := checkChange(c, ch); err != nil {
			dropped = append(dropped, Dropped{Change: ch, Why: err.Error()})
			continue
		}
		ok = append(ok, ch)
	}
	return ok, dropped
}

// ApplyAccepted devuelve una copia del CV con los cambios aceptados aplicados,
// en orden. Cada cambio se vuelve a validar contra el estado actual; si uno ya
// no es aplicable (por ejemplo, otro cambio previo tocó el mismo texto), se
// omite y se informa en el segundo valor.
func ApplyAccepted(c *CV, changes []Change) (*CV, []Dropped) {
	out := c.Clone()
	skipped := []Dropped{}
	for _, ch := range changes {
		if !ch.Accepted {
			continue
		}
		if err := checkChange(out, ch); err != nil {
			skipped = append(skipped, Dropped{Change: ch, Why: err.Error()})
			continue
		}
		for _, t := range out.texts(ch.TargetID) {
			if strings.Contains(*t, ch.Before) {
				*t = strings.Replace(*t, ch.Before, ch.After, 1)
				break
			}
		}
	}
	return out, skipped
}

func checkChange(c *CV, ch Change) error {
	if strings.TrimSpace(ch.TargetID) == "" {
		return fmt.Errorf("falta el target_id")
	}
	targets := c.texts(ch.TargetID)
	if len(targets) == 0 {
		return fmt.Errorf("el target_id %q no existe en el CV", ch.TargetID)
	}
	if strings.TrimSpace(ch.Keyword) == "" {
		return fmt.Errorf("el cambio no indica la keyword que busca incorporar")
	}
	if ch.Before == "" || ch.After == "" {
		return fmt.Errorf("el texto \"antes\" o \"después\" está vacío")
	}
	if ch.Before == ch.After {
		return fmt.Errorf("el cambio no modifica nada")
	}
	found := false
	for _, t := range targets {
		if strings.Contains(*t, ch.Before) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("el texto \"antes\" no aparece literal en %s", ch.TargetID)
	}
	if added := newDigitRuns(ch.Before, ch.After); len(added) > 0 {
		return fmt.Errorf("agrega números que no estaban en el texto original: %s", strings.Join(added, ", "))
	}
	if added := newProperNouns(ch.Before, ch.After, ch.Keyword); len(added) > 0 {
		return fmt.Errorf("agrega nombres propios que no estaban en el texto original ni son la keyword: %s", strings.Join(added, ", "))
	}
	return nil
}

var digitRun = regexp.MustCompile(`\d+`)

// newDigitRuns devuelve los números de after que no están (con su multiplicidad) en before.
func newDigitRuns(before, after string) []string {
	have := map[string]int{}
	for _, d := range digitRun.FindAllString(before, -1) {
		have[d]++
	}
	var added []string
	for _, d := range digitRun.FindAllString(after, -1) {
		if have[d] > 0 {
			have[d]--
			continue
		}
		added = append(added, d)
	}
	return added
}

func tokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

func startsUpper(tok string) bool {
	for _, r := range tok {
		return unicode.IsUpper(r)
	}
	return false
}

// newProperNouns devuelve las palabras en mayúscula de after que no estaban en
// before ni forman parte de la keyword. Es una heurística conservadora: ante la
// duda descarta el cambio (queda informado y la persona puede aplicarlo a mano).
func newProperNouns(before, after, keyword string) []string {
	known := map[string]bool{}
	bt := tokens(before)
	for _, t := range bt {
		known[strings.ToLower(t)] = true
	}
	for _, t := range tokens(keyword) {
		known[strings.ToLower(t)] = true
	}
	at := tokens(after)
	var added []string
	for i, t := range at {
		if !startsUpper(t) || known[strings.ToLower(t)] {
			continue
		}
		// La primera palabra va en mayúscula por gramática si el original también empezaba así.
		if i == 0 && len(bt) > 0 && startsUpper(bt[0]) {
			continue
		}
		added = append(added, t)
	}
	return added
}
