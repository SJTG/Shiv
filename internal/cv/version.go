package cv

import "time"

// Estados posibles de una keyword de la oferta respecto del CV.
const (
	StatusPresent     = "present"     // el CV ya la dice
	StatusReplaceable = "replaceable" // el CV dice lo mismo con otras palabras
	StatusMissing     = "missing"     // el CV no tiene respaldo para ella
)

// ValidStatus informa si s es uno de los tres estados conocidos.
func ValidStatus(s string) bool {
	return s == StatusPresent || s == StatusReplaceable || s == StatusMissing
}

type Keyword struct {
	Term   string `json:"term"`
	Status string `json:"status"`
}

type Job struct {
	Title   string `json:"title"`
	Company string `json:"company"`
	Text    string `json:"text"`
}

// Version es un CV adaptado a una oferta. CV es la foto con los cambios
// aceptados ya aplicados: es lo que se renderiza al PDF.
type Version struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Job       Job       `json:"job"`
	Keywords  []Keyword `json:"keywords"`
	Changes   []Change  `json:"changes"`
	CV        CV        `json:"cv"`
}
