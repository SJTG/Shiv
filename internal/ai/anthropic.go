package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"cvtailor/internal/cv"
)

const (
	// DefaultModel es el modelo que se usa si no se configura ANTHROPIC_MODEL.
	DefaultModel   = "claude-sonnet-5-5"
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"
	maxKeywords    = 25
)

// Anthropic implementa Analyzer con la Messages API, usando solo net/http.
type Anthropic struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

// NewAnthropic crea el cliente. model vacío usa DefaultModel.
func NewAnthropic(apiKey, model string) *Anthropic {
	if model == "" {
		model = DefaultModel
	}
	return &Anthropic{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: defaultBaseURL,
		HTTP:    &http.Client{Timeout: 3 * time.Minute},
	}
}

type contentBlock struct {
	Type   string  `json:"type"`
	Text   string  `json:"text,omitempty"`
	Source *source `json:"source,omitempty"`
}

type source struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type message struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
}

type response struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
}

type errorBody struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// complete hace una llamada a la Messages API y devuelve el texto de la respuesta.
func (a *Anthropic) complete(ctx context.Context, system string, blocks []contentBlock, maxTokens int) (string, error) {
	if a.APIKey == "" {
		return "", ErrNoAPIKey
	}
	body, err := json.Marshal(request{
		Model:     a.Model,
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []message{{Role: "user", Content: blocks}},
	})
	if err != nil {
		return "", fmt.Errorf("armar el pedido a la IA: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.BaseURL, "/")+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("armar el pedido a la IA: %w", err)
	}
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", apiVersion)
	req.Header.Set("content-type", "application/json")

	client := a.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("la IA tardó demasiado en responder")
		}
		return "", fmt.Errorf("no se pudo conectar con la API de IA: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", fmt.Errorf("leer la respuesta de la IA: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", apiError(resp.StatusCode, raw)
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("la respuesta de la IA no es un JSON válido: %w", err)
	}
	if out.StopReason == "max_tokens" {
		return "", errors.New("la respuesta de la IA se cortó por longitud; probá con un CV u oferta más cortos")
	}
	var text strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errors.New("la IA devolvió una respuesta vacía")
	}
	return text.String(), nil
}

func apiError(status int, raw []byte) error {
	var eb errorBody
	_ = json.Unmarshal(raw, &eb)
	detail := eb.Error.Message
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("la API rechazó la clave (HTTP %d): revisá ANTHROPIC_API_KEY. %s", status, detail)
	case http.StatusTooManyRequests:
		return fmt.Errorf("se alcanzó el límite de uso de la API (HTTP 429): esperá un momento y reintentá. %s", detail)
	case http.StatusNotFound:
		return fmt.Errorf("la API no encontró el recurso (HTTP 404): revisá ANTHROPIC_MODEL. %s", detail)
	}
	if status >= 500 {
		return fmt.Errorf("la API de IA falló o está sobrecargada (HTTP %d): reintentá en un rato. %s", status, detail)
	}
	if detail == "" {
		detail = strings.TrimSpace(string(raw))
		if len(detail) > 300 {
			detail = detail[:300]
		}
	}
	return fmt.Errorf("la API respondió HTTP %d: %s", status, detail)
}

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
func (a *Anthropic) callJSON(ctx context.Context, system string, blocks []contentBlock, maxTokens int, into any, validate func() error) error {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		reflect.ValueOf(into).Elem().SetZero() // el reintento no hereda datos de un intento a medias
		text, err := a.complete(ctx, system, blocks, maxTokens)
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

// ParseCV implementa Analyzer. El PDF viaja como bloque "document" en base64.
func (a *Anthropic) ParseCV(ctx context.Context, pdf []byte) (*cv.CV, error) {
	blocks := []contentBlock{
		{Type: "document", Source: &source{Type: "base64", MediaType: "application/pdf", Data: base64.StdEncoding.EncodeToString(pdf)}},
		{Type: "text", Text: "Convertí este CV al JSON pedido."},
	}
	var out cv.CV
	err := a.callJSON(ctx, systemParseCV, blocks, 8000, &out, out.Prepare)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ExtractKeywords implementa Analyzer.
func (a *Anthropic) ExtractKeywords(ctx context.Context, jobText string, c *cv.CV) ([]cv.Keyword, error) {
	blocks := []contentBlock{{Type: "text", Text: keywordsPrompt(jobText, c)}}
	var out struct {
		Keywords []cv.Keyword `json:"keywords"`
	}
	var cleaned []cv.Keyword
	err := a.callJSON(ctx, systemKeywords, blocks, 4000, &out, func() error {
		var err error
		cleaned, err = cleanKeywords(out.Keywords)
		return err
	})
	if err != nil {
		return nil, err
	}
	return cleaned, nil
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
func (a *Anthropic) ProposeChanges(ctx context.Context, jobText string, c *cv.CV, keywords []cv.Keyword) ([]cv.Change, error) {
	var replaceable []cv.Keyword
	for _, k := range keywords {
		if k.Status == cv.StatusReplaceable {
			replaceable = append(replaceable, k)
		}
	}
	if len(replaceable) == 0 {
		return []cv.Change{}, nil
	}
	blocks := []contentBlock{{Type: "text", Text: changesPrompt(jobText, c, replaceable)}}
	var out struct {
		Changes []cv.Change `json:"changes"`
	}
	if err := a.callJSON(ctx, systemChanges, blocks, 4000, &out, nil); err != nil {
		return nil, err
	}
	changes := make([]cv.Change, 0, len(out.Changes))
	for _, ch := range out.Changes {
		ch.Accepted = false // la aceptación es siempre decisión de la persona
		changes = append(changes, ch)
	}
	return changes, nil
}
