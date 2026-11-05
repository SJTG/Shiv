package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Provider identifica a quién pertenece una API key.
type Provider string

const (
	ProviderGemini    Provider = "gemini"
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"
	ProviderGroq      Provider = "groq"
)

// Label es el nombre para mostrar.
func (p Provider) Label() string {
	switch p {
	case ProviderGemini:
		return "Gemini"
	case ProviderOpenAI:
		return "OpenAI"
	case ProviderAnthropic:
		return "Anthropic"
	case ProviderGroq:
		return "Groq"
	}
	return string(p)
}

// SupportsPDF informa si el proveedor puede importar un CV en PDF.
func (p Provider) SupportsPDF() bool { return p != ProviderGroq }

var defaultBaseURLs = map[Provider]string{
	ProviderGemini:    "https://generativelanguage.googleapis.com",
	ProviderOpenAI:    "https://api.openai.com",
	ProviderAnthropic: "https://api.anthropic.com",
	ProviderGroq:      "https://api.groq.com/openai",
}

// preferred ordena los modelos recomendados primero; el primero que el
// proveedor ofrezca es el que se elige por defecto. También es la lista de
// respaldo si el listado de modelos viene vacío.
var preferred = map[Provider][]string{
	ProviderGemini:    {"gemini-2.5-flash", "gemini-2.5-pro", "gemini-flash-latest", "gemini-2.0-flash"},
	ProviderOpenAI:    {"gpt-5.1", "gpt-5", "gpt-5-mini", "gpt-4.1", "gpt-4o"},
	ProviderAnthropic: {"claude-sonnet-5-5", "claude-opus-5-5", "claude-haiku-5-5", "claude-fable-5-1"},
	ProviderGroq:      {"llama-3.3-70b-versatile", "openai/gpt-oss-120b", "moonshotai/kimi-k2-instruct"},
}

// ErrUnknownKey indica que el prefijo de la clave no corresponde a ningún
// proveedor soportado.
var ErrUnknownKey = errors.New("no reconozco esa API key: tiene que empezar con AQ (Gemini), sk- (OpenAI), sk-ant- (Anthropic) o gsk (Groq)")

// DetectProvider identifica el proveedor por las primeras letras de la clave.
// El orden importa: "sk-ant-" es de Anthropic aunque también empiece con "sk".
func DetectProvider(key string) (Provider, error) {
	key = strings.TrimSpace(key)
	switch {
	case key == "":
		return "", ErrNoAPIKey
	case strings.HasPrefix(key, "sk-ant-"):
		return ProviderAnthropic, nil
	case strings.HasPrefix(key, "gsk"):
		return ProviderGroq, nil
	case strings.HasPrefix(key, "sk"):
		return ProviderOpenAI, nil
	case strings.HasPrefix(key, "AQ"), strings.HasPrefix(key, "AIza"): // AIza: claves clásicas de Google AI Studio
		return ProviderGemini, nil
	}
	return "", ErrUnknownKey
}

// PickDefault elige el modelo por defecto de una lista.
func PickDefault(p Provider, models []string) string {
	for _, want := range preferred[p] {
		for _, m := range models {
			if m == want {
				return m
			}
		}
	}
	if len(models) > 0 {
		return models[0]
	}
	if pref := preferred[p]; len(pref) > 0 {
		return pref[0]
	}
	return ""
}

// Registry arma clientes y verifica claves. BaseURLs permite apuntar a un
// proxy o a un servidor de pruebas; lo que falte usa la URL oficial.
type Registry struct {
	BaseURLs map[Provider]string
	HTTP     *http.Client
}

// NewRegistry crea un Registry con las URLs oficiales.
func NewRegistry() *Registry {
	return &Registry{BaseURLs: map[Provider]string{}, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (r *Registry) baseURL(p Provider) string {
	if u := r.BaseURLs[p]; u != "" {
		return strings.TrimRight(u, "/")
	}
	return defaultBaseURLs[p]
}

// Analyzer devuelve el cliente para ese proveedor, clave y modelo.
func (r *Registry) Analyzer(p Provider, apiKey, model string) Analyzer {
	if model == "" {
		model = PickDefault(p, nil)
	}
	switch p {
	case ProviderAnthropic:
		a := NewAnthropic(apiKey, model)
		a.BaseURL = r.baseURL(p)
		return a
	case ProviderGemini:
		return NewGemini(apiKey, model, r.baseURL(p))
	case ProviderOpenAI, ProviderGroq:
		return NewOpenAI(p, apiKey, model, r.baseURL(p))
	}
	return NewAnthropic("", model) // no debería pasar: falla con ErrNoAPIKey
}

// Verify detecta el proveedor por el prefijo de la clave y la prueba contra
// la API listando sus modelos. Devuelve los modelos de texto disponibles.
func (r *Registry) Verify(ctx context.Context, apiKey string) (Provider, []string, error) {
	apiKey = strings.TrimSpace(apiKey)
	p, err := DetectProvider(apiKey)
	if err != nil {
		return "", nil, err
	}
	if len(apiKey) < 20 || len(apiKey) > 512 || strings.ContainsAny(apiKey, " \t\r\n") {
		return p, nil, fmt.Errorf("la clave parece de %s pero tiene un formato inválido", p.Label())
	}
	models, err := r.listModels(ctx, p, apiKey)
	if err != nil {
		if IsAuthError(err) {
			return p, nil, fmt.Errorf("la clave parece de %s, pero %s la rechazó: revisá que esté completa y activa", p.Label(), p.Label())
		}
		return p, nil, fmt.Errorf("no se pudo verificar la clave con %s: %w", p.Label(), err)
	}
	if len(models) == 0 {
		models = append([]string(nil), preferred[p]...)
	}
	return p, orderModels(p, models), nil
}

func (r *Registry) listModels(ctx context.Context, p Provider, key string) ([]string, error) {
	base := r.baseURL(p)
	switch p {
	case ProviderAnthropic:
		raw, err := doJSON(ctx, r.HTTP, p, http.MethodGet, base+"/v1/models?limit=1000", anthropicHeaders(key), nil)
		if err != nil {
			return nil, err
		}
		var out struct {
			Data []struct{ ID string } `json:"data"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("respuesta inválida al listar modelos: %w", err)
		}
		var ids []string
		for _, m := range out.Data {
			ids = append(ids, m.ID)
		}
		return ids, nil

	case ProviderOpenAI, ProviderGroq:
		raw, err := doJSON(ctx, r.HTTP, p, http.MethodGet, base+"/v1/models", bearer(key), nil)
		if err != nil {
			return nil, err
		}
		var out struct {
			Data []struct {
				ID     string `json:"id"`
				Active *bool  `json:"active"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("respuesta inválida al listar modelos: %w", err)
		}
		var ids []string
		for _, m := range out.Data {
			if m.Active != nil && !*m.Active {
				continue
			}
			if isChatModel(p, m.ID) {
				ids = append(ids, m.ID)
			}
		}
		return ids, nil

	case ProviderGemini:
		raw, err := doJSON(ctx, r.HTTP, p, http.MethodGet, base+"/v1beta/models?pageSize=1000", geminiHeaders(key), nil)
		if err != nil {
			return nil, err
		}
		var out struct {
			Models []struct {
				Name    string   `json:"name"`
				Methods []string `json:"supportedGenerationMethods"`
			} `json:"models"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("respuesta inválida al listar modelos: %w", err)
		}
		var ids []string
		for _, m := range out.Models {
			id := strings.TrimPrefix(m.Name, "models/")
			generates := false
			for _, meth := range m.Methods {
				generates = generates || meth == "generateContent"
			}
			if generates && isChatModel(p, id) {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
	return nil, ErrUnknownKey
}

// isChatModel deja afuera los modelos que no sirven para texto: audio,
// imágenes, embeddings, moderación, etc.
func isChatModel(p Provider, id string) bool {
	low := strings.ToLower(id)
	for _, bad := range []string{"embed", "tts", "whisper", "transcribe", "audio", "realtime", "image", "dall-e",
		"moderation", "guard", "search", "live", "veo", "imagen", "aqa", "babbage", "davinci", "playai", "orpheus", "computer-use"} {
		if strings.Contains(low, bad) {
			return false
		}
	}
	switch p {
	case ProviderOpenAI:
		return strings.HasPrefix(low, "gpt-") || strings.HasPrefix(low, "chatgpt-") ||
			strings.HasPrefix(low, "o1") || strings.HasPrefix(low, "o3") || strings.HasPrefix(low, "o4")
	case ProviderGemini:
		return strings.HasPrefix(low, "gemini") // gemma no acepta instrucciones de sistema
	}
	return true
}

// orderModels pone primero los recomendados y después el resto en orden alfabético.
func orderModels(p Provider, models []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, want := range preferred[p] {
		for _, m := range models {
			if m == want && !seen[m] {
				out = append(out, m)
				seen[m] = true
			}
		}
	}
	var rest []string
	for _, m := range models {
		if !seen[m] {
			rest = append(rest, m)
			seen[m] = true
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}
