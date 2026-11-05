package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDetectProvider(t *testing.T) {
	tests := map[string]Provider{
		"AQ.Ab8RN6abcdefghijklmnop":   ProviderGemini,
		"AIzaSyAbcdefghijklmnop":      ProviderGemini,
		"sk-proj-abcdefghijklmnop":    ProviderOpenAI,
		"sk-abcdefghijklmnop":         ProviderOpenAI,
		"sk-ant-api03-abcdefghijklmn": ProviderAnthropic,
		"gsk_abcdefghijklmnop":        ProviderGroq,
		"  gsk_conespacios  ":         ProviderGroq,
	}
	for key, want := range tests {
		got, err := DetectProvider(key)
		if err != nil || got != want {
			t.Errorf("DetectProvider(%q) = %q, %v; quería %q", key, got, err, want)
		}
	}
	if _, err := DetectProvider("xyz-123"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("clave desconocida: err = %v", err)
	}
	if _, err := DetectProvider("  "); !errors.Is(err, ErrNoAPIKey) {
		t.Errorf("clave vacía: err = %v", err)
	}
}

func TestPickDefault(t *testing.T) {
	if got := PickDefault(ProviderGroq, []string{"zeta", "llama-3.3-70b-versatile"}); got != "llama-3.3-70b-versatile" {
		t.Errorf("got %q", got)
	}
	if got := PickDefault(ProviderGroq, []string{"zeta", "alfa"}); got != "zeta" {
		t.Errorf("sin recomendados debía tomar el primero, got %q", got)
	}
	if got := PickDefault(ProviderAnthropic, nil); got != DefaultModel {
		t.Errorf("lista vacía: got %q", got)
	}
}

// fakeProvider simula los endpoints de listado y de chat de los proveedores.
type fakeProvider struct {
	wantAuth func(r *http.Request) bool
	status   int
	list     string
	reply    string
	lastPath string
	lastBody map[string]any
}

func (f *fakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.lastPath = r.URL.Path
	raw, _ := io.ReadAll(r.Body)
	f.lastBody = nil
	_ = json.Unmarshal(raw, &f.lastBody)
	if !f.wantAuth(r) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid key"}}`)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		_, _ = io.WriteString(w, `{"error":{"message":"API key not valid. Please pass a valid API key."}}`)
		return
	}
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, f.list)
		return
	}
	_, _ = io.WriteString(w, f.reply)
}

func registryFor(t *testing.T, p Provider, f *fakeProvider) *Registry {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	r := NewRegistry()
	r.BaseURLs[p] = srv.URL
	return r
}

func TestVerifyListsAndFiltersModels(t *testing.T) {
	cases := []struct {
		name string
		p    Provider
		key  string
		auth func(r *http.Request) bool
		path string
		list string
		want []string
	}{
		{"openai", ProviderOpenAI, "sk-proj-0123456789abcdefghij",
			func(r *http.Request) bool {
				return r.Header.Get("authorization") == "Bearer sk-proj-0123456789abcdefghij"
			},
			"/v1/models",
			`{"data":[{"id":"whisper-1"},{"id":"gpt-4o"},{"id":"text-embedding-3-small"},{"id":"gpt-4o-realtime-preview"},{"id":"gpt-5"},{"id":"o3-mini"}]}`,
			[]string{"gpt-5", "gpt-4o", "o3-mini"}},
		{"groq", ProviderGroq, "gsk_0123456789abcdefghij",
			func(r *http.Request) bool { return r.Header.Get("authorization") == "Bearer gsk_0123456789abcdefghij" },
			"/v1/models",
			`{"data":[{"id":"whisper-large-v3","active":true},{"id":"llama-3.3-70b-versatile","active":true},{"id":"viejo","active":false},{"id":"llama-guard-4-12b","active":true},{"id":"qwen/qwen3-32b","active":true}]}`,
			[]string{"llama-3.3-70b-versatile", "qwen/qwen3-32b"}},
		{"anthropic", ProviderAnthropic, "sk-ant-api03-0123456789abcd",
			func(r *http.Request) bool {
				return r.Header.Get("x-api-key") == "sk-ant-api03-0123456789abcd" && r.Header.Get("anthropic-version") == apiVersion
			},
			"/v1/models",
			`{"data":[{"id":"claude-haiku-5-5"},{"id":"claude-sonnet-5-5"}]}`,
			[]string{"claude-sonnet-5-5", "claude-haiku-5-5"}},
		{"gemini", ProviderGemini, "AQ.Ab8RN60123456789abcdef",
			func(r *http.Request) bool { return r.Header.Get("x-goog-api-key") == "AQ.Ab8RN60123456789abcdef" },
			"/v1beta/models",
			`{"models":[{"name":"models/gemini-2.5-pro","supportedGenerationMethods":["generateContent"]},{"name":"models/text-embedding-004","supportedGenerationMethods":["embedContent"]},{"name":"models/gemini-2.5-flash","supportedGenerationMethods":["generateContent","countTokens"]},{"name":"models/gemini-2.5-flash-preview-tts","supportedGenerationMethods":["generateContent"]}]}`,
			[]string{"gemini-2.5-flash", "gemini-2.5-pro"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeProvider{wantAuth: c.auth, list: c.list}
			r := registryFor(t, c.p, f)
			p, models, err := r.Verify(context.Background(), c.key)
			if err != nil {
				t.Fatal(err)
			}
			if p != c.p || strings.Join(models, ",") != strings.Join(c.want, ",") {
				t.Fatalf("got %q %v, quería %q %v", p, models, c.p, c.want)
			}
			if !strings.HasSuffix(f.lastPath, c.path) {
				t.Errorf("path = %q", f.lastPath)
			}
			// Una clave rechazada se informa como tal.
			_, _, err = r.Verify(context.Background(), c.key+"x")
			if err == nil || !strings.Contains(err.Error(), "rechazó") {
				t.Errorf("clave rechazada: err = %v", err)
			}
		})
	}
}

func TestVerifyGeminiBadKeyIs400(t *testing.T) {
	f := &fakeProvider{wantAuth: func(*http.Request) bool { return true }, status: 400}
	r := registryFor(t, ProviderGemini, f)
	_, _, err := r.Verify(context.Background(), "AQ.0123456789abcdefghijkl")
	if err == nil || !strings.Contains(err.Error(), "rechazó") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyRejectsUnknownAndMalformedKeys(t *testing.T) {
	r := NewRegistry()
	if _, _, err := r.Verify(context.Background(), "hola-1234567890123456789"); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("err = %v", err)
	}
	if _, _, err := r.Verify(context.Background(), "sk-corta"); err == nil || !strings.Contains(err.Error(), "formato") {
		t.Errorf("err = %v", err)
	}
}

func TestOpenAIRequestShape(t *testing.T) {
	f := &fakeProvider{wantAuth: func(*http.Request) bool { return true },
		reply: `{"choices":[{"message":{"content":"{\"title\":\"Dev\",\"company\":\"\",\"keywords\":[{\"term\":\"Go\",\"status\":\"present\"}]}"},"finish_reason":"stop"}]}`}
	r := registryFor(t, ProviderOpenAI, f)
	an := r.Analyzer(ProviderOpenAI, "sk-test", "gpt-4o")
	res, err := an.AnalyzeJob(context.Background(), "oferta con Go", nil)
	if err != nil || res.Title != "Dev" || len(res.Keywords) != 1 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
	if f.lastPath != "/v1/chat/completions" || f.lastBody["model"] != "gpt-4o" {
		t.Fatalf("path %q body %v", f.lastPath, f.lastBody)
	}
	msgs := f.lastBody["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" || !strings.Contains(msgs[1].(map[string]any)["content"].(string), "<oferta>") {
		t.Fatalf("messages = %v", msgs)
	}

	// El PDF viaja como parte "file" con data URL.
	f.reply = `{"choices":[{"message":{"content":"{\"basics\":{\"name\":\"Ana\"}}"},"finish_reason":"stop"}]}`
	if _, err := an.ParseCV(context.Background(), []byte("%PDF-1.4")); err != nil {
		t.Fatal(err)
	}
	parts := f.lastBody["messages"].([]any)[1].(map[string]any)["content"].([]any)
	file := parts[0].(map[string]any)["file"].(map[string]any)
	if !strings.HasPrefix(file["file_data"].(string), "data:application/pdf;base64,") {
		t.Fatalf("file = %v", file)
	}

	f.reply = `{"choices":[{"message":{"content":"{\"bas"},"finish_reason":"length"}]}`
	if _, err := an.ParseCV(context.Background(), []byte("%PDF-1.4")); !errors.Is(err, errTruncated) {
		t.Fatalf("err = %v", err)
	}
}

func TestGroqRejectsPDFWithoutCalling(t *testing.T) {
	f := &fakeProvider{wantAuth: func(*http.Request) bool { return true }}
	r := registryFor(t, ProviderGroq, f)
	_, err := r.Analyzer(ProviderGroq, "gsk_test", "llama-3.3-70b-versatile").ParseCV(context.Background(), []byte("%PDF-"))
	if !errors.Is(err, ErrPDFUnsupported) || f.lastPath != "" {
		t.Fatalf("err = %v, path = %q", err, f.lastPath)
	}
}

func TestGeminiRequestShape(t *testing.T) {
	f := &fakeProvider{wantAuth: func(r *http.Request) bool { return r.Header.Get("x-goog-api-key") == "AQ.test" },
		reply: `{"candidates":[{"content":{"parts":[{"text":"pensando...","thought":true},{"text":"{\"basics\":{\"name\":\"Ana\"}}"}]},"finishReason":"STOP"}]}`}
	r := registryFor(t, ProviderGemini, f)
	got, err := r.Analyzer(ProviderGemini, "AQ.test", "gemini-2.5-flash").ParseCV(context.Background(), []byte("%PDF-1.4"))
	if err != nil || got.Basics.Name != "Ana" {
		t.Fatalf("got = %+v err = %v", got, err)
	}
	if f.lastPath != "/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("path = %q", f.lastPath)
	}
	parts := f.lastBody["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	blob := parts[0].(map[string]any)["inline_data"].(map[string]any)
	if blob["mime_type"] != "application/pdf" || f.lastBody["system_instruction"] == nil {
		t.Fatalf("body = %v", f.lastBody)
	}
}
