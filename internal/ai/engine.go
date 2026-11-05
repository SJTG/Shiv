package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"cvtailor/internal/cv"
)

const (
	maxKeywords   = 25
	maxMetaRunes  = 120
	maxRespBytes  = 16 << 20
	maxErrorBytes = 300
)

// part es un trozo del mensaje del usuario: texto o un PDF.
type part struct {
	text string
	pdf  []byte
}

// completer es lo único que cambia entre proveedores: mandar un mensaje y
// devolver el texto de la respuesta.
type completer interface {
	complete(ctx context.Context, system string, parts []part, maxTokens int) (string, error)
}

// engine implementa Analyzer sobre cualquier completer: prompts, reintentos y
// validación de las respuestas son iguales para todos los proveedores.
type engine struct{ c completer }

// APIError es un error devuelto por la API del proveedor.
type APIError struct {
	Status int
	Auth   bool // la API rechazó la clave
	msg    string
}

func (e *APIError) Error() string { return e.msg }

// IsAuthError informa si err es un rechazo de la clave.
func IsAuthError(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Auth
}

type errorBody struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func apiError(p Provider, status int, raw []byte) error {
	var eb errorBody
	_ = json.Unmarshal(raw, &eb)
	detail := eb.Error.Message
	if detail == "" {
		detail = strings.TrimSpace(string(raw))
		if len(detail) > maxErrorBytes {
			detail = detail[:maxErrorBytes]
		}
	}
	name := p.Label()
	// Gemini responde 400 (no 401) cuando la clave no sirve.
	auth := status == http.StatusUnauthorized || status == http.StatusForbidden ||
		strings.Contains(strings.ToLower(detail), "api key not valid")
	e := &APIError{Status: status, Auth: auth}
	switch {
	case auth:
		e.msg = fmt.Sprintf("%s rechazó la clave (HTTP %d): revisá tu API key en Cuenta. %s", name, status, detail)
	case status == http.StatusTooManyRequests:
		e.msg = fmt.Sprintf("se alcanzó el límite de uso de la API de %s (HTTP 429): esperá un momento y reintentá. %s", name, detail)
	case status == http.StatusNotFound:
		e.msg = fmt.Sprintf("%s no encontró el recurso (HTTP 404): revisá el modelo elegido. %s", name, detail)
	case status >= 500:
		e.msg = fmt.Sprintf("la API de %s falló o está sobrecargada (HTTP %d): reintentá en un rato. %s", name, status, detail)
	default:
		e.msg = fmt.Sprintf("%s respondió HTTP %d: %s", name, status, detail)
	}
	return e
}

// doJSON manda un pedido HTTP (con cuerpo JSON si body no es nil) y devuelve
// la respuesta cruda si fue 200.
func doJSON(ctx context.Context, client *http.Client, p Provider, method, url string, headers map[string]string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("armar el pedido a la IA: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, fmt.Errorf("armar el pedido a la IA: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("content-type", "application/json")
	}
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("la IA tardó demasiado en responder")
		}
		return nil, fmt.Errorf("no se pudo conectar con la API de %s: %w", p.Label(), err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, fmt.Errorf("leer la respuesta de la IA: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, apiError(p, resp.StatusCode, raw)
	}
	return raw, nil
}

var (
	errTruncated = errors.New("la respuesta de la IA se cortó por longitud; probá con un CV u oferta más cortos")
	errEmpty     = errors.New("la IA devolvió una respuesta vacía")
)

// extractJSON saca el objeto JSON de la respuesta, tolerando bloques ```json.
func extractJSON(text string) (string, error) {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return "", errors.New("la respuesta de la IA no contiene un objeto JSON")
	}
	return text[start : end+1], nil
}

// callJSON llama a la IA y decodifica el JSON en into; validate revisa el
// resultado. Si la respuesta no parsea o no valida, se reintenta una vez.
func (e engine) callJSON(ctx context.Context, system string, parts []part, maxTokens int, into any, validate func() error) error {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		reflect.ValueOf(into).Elem().SetZero() // el reintento no hereda datos de un intento a medias
		text, err := e.c.complete(ctx, system, parts, maxTokens)
		if err != nil {
			return err // los errores de red y de API no se reintentan acá
		}
		js, err := extractJSON(text)
		if err == nil {
			err = json.Unmarshal([]byte(js), into)
		}
		if err == nil && validate != nil {
			err = validate()
		}
		if err == nil {
			return nil
		}
		last = err
	}
	return fmt.Errorf("la IA no devolvió una respuesta válida tras reintentar: %w", last)
}

// ParseCV implementa Analyzer.
func (e engine) ParseCV(ctx context.Context, pdf []byte) (*cv.CV, error) {
	parts := []part{{pdf: pdf}, {text: "Convertí este CV al JSON pedido."}}
	var out cv.CV
	if err := e.callJSON(ctx, systemParseCV, parts, 8000, &out, out.Prepare); err != nil {
		return nil, err
	}
	return &out, nil
}

// AnalyzeJob implementa Analyzer.
func (e engine) AnalyzeJob(ctx context.Context, jobText string, c *cv.CV) (*JobAnalysis, error) {
	parts := []part{{text: keywordsPrompt(jobText, c)}}
	var out JobAnalysis
	var cleaned []cv.Keyword
	err := e.callJSON(ctx, systemKeywords, parts, 4000, &out, func() error {
		var err error
		cleaned, err = cleanKeywords(out.Keywords)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &JobAnalysis{
		Title:    cleanMeta(out.Title),
		Company:  cleanMeta(out.Company),
		Keywords: cleaned,
	}, nil
}

// cleanMeta recorta el puesto o la empresa a una línea corta.
func cleanMeta(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > maxMetaRunes {
		s = string([]rune(s)[:maxMetaRunes])
	}
	return s
}

// cleanKeywords normaliza la lista: recorta, descarta vacías, quita repetidas
// y exige estados válidos. Una lista sin keywords se considera inválida.
func cleanKeywords(in []cv.Keyword) ([]cv.Keyword, error) {
	seen := map[string]bool{}
	out := []cv.Keyword{}
	for _, k := range in {
		k.Term = strings.TrimSpace(k.Term)
		k.Status = strings.TrimSpace(k.Status)
		if k.Term == "" {
			continue
		}
		if !cv.ValidStatus(k.Status) {
			return nil, fmt.Errorf("estado de keyword inválido %q para %q", k.Status, k.Term)
		}
		key := strings.ToLower(k.Term)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil, errors.New("la IA no devolvió ninguna keyword")
	}
	if len(out) > maxKeywords {
		out = out[:maxKeywords]
	}
	return out, nil
}

// ProposeChanges implementa Analyzer. Sin keywords "replaceable" no hay nada
// que proponer y no se gasta una llamada.
func (e engine) ProposeChanges(ctx context.Context, jobText string, c *cv.CV, keywords []cv.Keyword) ([]cv.Change, error) {
	var replaceable []cv.Keyword
	for _, k := range keywords {
		if k.Status == cv.StatusReplaceable {
			replaceable = append(replaceable, k)
		}
	}
	if len(replaceable) == 0 {
		return []cv.Change{}, nil
	}
	parts := []part{{text: changesPrompt(jobText, c, replaceable)}}
	var out struct {
		Changes []cv.Change `json:"changes"`
	}
	if err := e.callJSON(ctx, systemChanges, parts, 4000, &out, nil); err != nil {
		return nil, err
	}
	changes := make([]cv.Change, 0, len(out.Changes))
	for _, ch := range out.Changes {
		ch.Accepted = false // la aceptación es siempre decisión de la persona
		changes = append(changes, ch)
	}
	return changes, nil
}
