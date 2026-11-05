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

// OpenAI implementa Analyzer con la Chat Completions API. Sirve también para
// Groq, que expone la misma API (pero no acepta PDFs).
type OpenAI struct {
	Provider Provider // ProviderOpenAI o ProviderGroq
	APIKey   string
	Model    string
	BaseURL  string // sin /v1: https://api.openai.com o https://api.groq.com/openai
	HTTP     *http.Client
	engine
}

// NewOpenAI crea un cliente para OpenAI o Groq.
func NewOpenAI(p Provider, apiKey, model, baseURL string) *OpenAI {
	if baseURL == "" {
		baseURL = defaultBaseURLs[p]
	}
	o := &OpenAI{Provider: p, APIKey: apiKey, Model: model, BaseURL: baseURL, HTTP: &http.Client{Timeout: 3 * time.Minute}}
	o.engine = engine{o}
	return o
}

type oaFile struct {
	Filename string `json:"filename"`
	FileData string `json:"file_data"`
}

type oaPart struct {
	Type string  `json:"type"`
	Text string  `json:"text,omitempty"`
	File *oaFile `json:"file,omitempty"`
}

type oaMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string, o []oaPart cuando viaja un PDF
}

type oaRequest struct {
	Model    string      `json:"model"`
	Messages []oaMessage `json:"messages"`
}

type oaResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func bearer(key string) map[string]string {
	return map[string]string{"authorization": "Bearer " + key}
}

// complete implementa completer. No se fija un tope de tokens: cada modelo
// usa su máximo, así los modelos que "razonan" no se quedan sin espacio.
func (o *OpenAI) complete(ctx context.Context, system string, parts []part, _ int) (string, error) {
	if o.APIKey == "" {
		return "", ErrNoAPIKey
	}
	var user any
	hasPDF := false
	for _, p := range parts {
		hasPDF = hasPDF || p.pdf != nil
	}
	if hasPDF {
		if o.Provider == ProviderGroq {
			return "", fmt.Errorf("%w: Groq no acepta archivos PDF. Importá el CV con una clave de Gemini, OpenAI o Anthropic, o completalo a mano en CV maestro", ErrPDFUnsupported)
		}
		ps := make([]oaPart, 0, len(parts))
		for _, p := range parts {
			if p.pdf != nil {
				ps = append(ps, oaPart{Type: "file", File: &oaFile{
					Filename: "cv.pdf", FileData: "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(p.pdf),
				}})
				continue
			}
			ps = append(ps, oaPart{Type: "text", Text: p.text})
		}
		user = ps
	} else {
		texts := make([]string, len(parts))
		for i, p := range parts {
			texts[i] = p.text
		}
		user = strings.Join(texts, "\n\n")
	}
	raw, err := doJSON(ctx, o.HTTP, o.Provider, http.MethodPost,
		strings.TrimRight(o.BaseURL, "/")+"/v1/chat/completions", bearer(o.APIKey),
		oaRequest{Model: o.Model, Messages: []oaMessage{{Role: "system", Content: system}, {Role: "user", Content: user}}})
	if err != nil {
		return "", err
	}
	var out oaResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("la respuesta de la IA no es un JSON válido: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errEmpty
	}
	c := out.Choices[0]
	if c.FinishReason == "length" {
		return "", errTruncated
	}
	if strings.TrimSpace(c.Message.Content) == "" {
		return "", errEmpty
	}
	return c.Message.Content, nil
}
