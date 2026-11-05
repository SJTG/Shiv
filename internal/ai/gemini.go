package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Gemini implementa Analyzer con la API generateContent de Google.
type Gemini struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
	engine
}

// NewGemini crea el cliente de Gemini.
func NewGemini(apiKey, model, baseURL string) *Gemini {
	if baseURL == "" {
		baseURL = defaultBaseURLs[ProviderGemini]
	}
	g := &Gemini{APIKey: apiKey, Model: model, BaseURL: baseURL, HTTP: &http.Client{Timeout: 3 * time.Minute}}
	g.engine = engine{g}
	return g
}

type gmBlob struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type gmPart struct {
	Text       string  `json:"text,omitempty"`
	InlineData *gmBlob `json:"inline_data,omitempty"`
}

type gmContent struct {
	Role  string   `json:"role,omitempty"`
	Parts []gmPart `json:"parts"`
}

type gmConfig struct {
	ResponseMimeType string `json:"responseMimeType,omitempty"`
}

type gmRequest struct {
	SystemInstruction *gmContent  `json:"system_instruction,omitempty"`
	Contents          []gmContent `json:"contents"`
	GenerationConfig  *gmConfig   `json:"generationConfig,omitempty"`
}

type gmResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text    string `json:"text"`
				Thought bool   `json:"thought"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
}

func geminiHeaders(key string) map[string]string {
	return map[string]string{"x-goog-api-key": key}
}

// complete implementa completer. Sin tope de tokens explícito: los modelos que
// "piensan" gastan parte de la salida en eso y un tope bajo los corta.
func (g *Gemini) complete(ctx context.Context, system string, parts []part, _ int) (string, error) {
	if g.APIKey == "" {
		return "", ErrNoAPIKey
	}
	gp := make([]gmPart, 0, len(parts))
	for _, p := range parts {
		if p.pdf != nil {
			gp = append(gp, gmPart{InlineData: &gmBlob{MimeType: "application/pdf", Data: base64.StdEncoding.EncodeToString(p.pdf)}})
			continue
		}
		gp = append(gp, gmPart{Text: p.text})
	}
	endpoint := strings.TrimRight(g.BaseURL, "/") + "/v1beta/models/" + url.PathEscape(g.Model) + ":generateContent"
	raw, err := doJSON(ctx, g.HTTP, ProviderGemini, http.MethodPost, endpoint, geminiHeaders(g.APIKey), gmRequest{
		SystemInstruction: &gmContent{Parts: []gmPart{{Text: system}}},
		Contents:          []gmContent{{Role: "user", Parts: gp}},
		GenerationConfig:  &gmConfig{ResponseMimeType: "application/json"},
	})
	if err != nil {
		return "", err
	}
	var out gmResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("la respuesta de la IA no es un JSON válido: %w", err)
	}
	if out.PromptFeedback.BlockReason != "" {
		return "", fmt.Errorf("Gemini bloqueó el pedido (%s)", out.PromptFeedback.BlockReason)
	}
	if len(out.Candidates) == 0 {
		return "", errEmpty
	}
	c := out.Candidates[0]
	if c.FinishReason == "MAX_TOKENS" {
		return "", errTruncated
	}
	var text strings.Builder
	for _, p := range c.Content.Parts {
		if !p.Thought {
			text.WriteString(p.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errEmpty
	}
	return text.String(), nil
}
