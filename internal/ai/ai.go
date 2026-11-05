// Package ai habla con la API de IA para importar el CV, extraer keywords de
// una oferta y proponer cambios. La IA propone; la validación vive en el
// paquete cv y la decisión final es de la persona.
package ai

import (
	"context"
	"errors"

	"cvtailor/internal/cv"
)

// Analyzer es lo que el resto de la app necesita de la IA. Es una interfaz para
// poder simularla en tests o cambiar de proveedor.
type Analyzer interface {
	// ParseCV convierte un PDF en el CV estructurado. Lo que no pueda ubicar
	// queda en CV.Unparsed.
	ParseCV(ctx context.Context, pdf []byte) (*cv.CV, error)
	// ExtractKeywords saca las keywords de la oferta y las clasifica contra el CV.
	ExtractKeywords(ctx context.Context, jobText string, c *cv.CV) ([]cv.Keyword, error)
	// ProposeChanges propone intercambios para las keywords "replaceable".
	// Las propuestas todavía no están validadas: hay que pasarlas por cv.ValidateChanges.
	ProposeChanges(ctx context.Context, jobText string, c *cv.CV, keywords []cv.Keyword) ([]cv.Change, error)
}

// ErrNoAPIKey indica que falta configurar ANTHROPIC_API_KEY.
var ErrNoAPIKey = errors.New("falta la variable de entorno ANTHROPIC_API_KEY")
