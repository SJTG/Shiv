package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	// DefaultModel es el modelo de Anthropic que se usa si no se elige otro.
	DefaultModel = "claude-sonnet-5-5"
	apiVersion   = "2023-06-01"
)

// Anthropic implementa Analyzer con la Messages API, usando solo net/http.
type Anthropic struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
	engine
}

// NewAnthropic crea el cliente. model vacío usa DefaultModel.
func NewAnthropic(apiKey, model string) *Anthropic {
	if model == "" {
		model = DefaultModel
	}
	a := &Anthropic{
		APIKey:  apiKey,
		Model:   model,
		BaseURL: defaultBaseURLs[ProviderAnthropic],
		HTTP:    &http.Client{Timeout: 3 * time.Minute},
	}
	a.engine = engine{a}
	return a
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

func anthropicHeaders(key string) map[string]string {
	return map[string]string{"x-api-key": key, "anthropic-version": apiVersion}
}

// complete hace una llamada a la Messages API y devuelve el texto de la respuesta.
func (a *Anthropic) complete(ctx context.Context, system string, parts []part, maxTokens int) (string, error) {
	if a.APIKey == "" {
		return "", ErrNoAPIKey
	}
	blocks := make([]contentBlock, 0, len(parts))
	for _, p := range parts {
		if p.pdf != nil {
			blocks = append(blocks, contentBlock{Type: "document", Source: &source{
				Type: "base64", MediaType: "application/pdf", Data: base64.StdEncoding.EncodeToString(p.pdf),
			}})
			continue
		}
		blocks = append(blocks, contentBlock{Type: "text", Text: p.text})
	}
	raw, err := doJSON(ctx, a.HTTP, ProviderAnthropic, http.MethodPost,
		strings.TrimRight(a.BaseURL, "/")+"/v1/messages", anthropicHeaders(a.APIKey),
		request{Model: a.Model, MaxTokens: maxTokens, System: system, Messages: []message{{Role: "user", Content: blocks}}})
	if err != nil {
		return "", err
	}
	var out response
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("la respuesta de la IA no es un JSON válido: %w", err)
	}
	if out.StopReason == "max_tokens" {
		return "", errTruncated
	}
	var text strings.Builder
	for _, b := range out.Content {
		if b.Type == "text" {
			text.WriteString(b.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errEmpty
	}
	return text.String(), nil
}
