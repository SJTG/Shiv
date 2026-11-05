// Package ai habla con la API de IA para importar el CV, analizar una oferta
// y proponer cambios. La IA propone; la validación vive en el paquete cv y la
// decisión final es de la persona.
//
// La app es BYOK (bring your own key): cada persona pega su propia clave y el
// proveedor (Gemini, OpenAI, Anthropic o Groq) se detecta por su prefijo.
package ai

import (
	"context"
	"errors"

	"cvtailor/internal/cv"
)

// Analyzer es lo que el resto de la app necesita de la IA. Es una interfaz para
// poder simularla en tests y para tener un cliente por proveedor.
type Analyzer interface {
	// ParseCV convierte un PDF en el CV estructurado. Lo que no pueda ubicar
	// queda en CV.Unparsed.
	ParseCV(ctx context.Context, pdf []byte) (*cv.CV, error)
	// AnalyzeJob saca de la oferta el puesto, la empresa y las keywords, y
	// clasifica cada keyword contra el CV.
	AnalyzeJob(ctx context.Context, jobText string, c *cv.CV) (*JobAnalysis, error)
	// ProposeChanges propone intercambios para las keywords "replaceable".
	// Las propuestas todavía no están validadas: hay que pasarlas por cv.ValidateChanges.
	ProposeChanges(ctx context.Context, jobText string, c *cv.CV, keywords []cv.Keyword) ([]cv.Change, error)
}

// JobAnalysis es lo que la IA extrae de una oferta.
type JobAnalysis struct {
	Title    string       `json:"title"`
	Company  string       `json:"company"`
	Keywords []cv.Keyword `json:"keywords"`
}

// ErrNoAPIKey indica que la sesión no tiene una API key cargada.
var ErrNoAPIKey = errors.New("no hay una API key cargada: iniciá sesión con tu clave")

// ErrPDFUnsupported indica que el proveedor elegido no puede leer PDFs.
var ErrPDFUnsupported = errors.New("este proveedor no puede leer PDFs")
