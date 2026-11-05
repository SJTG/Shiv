package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"cvtailor/internal/ai"
)

// Los formularios que llaman a la IA se mandan con fetch desde app.js y la
// cabecera X-Shiv-Async. Si algo falla, el servidor responde un JSON con el
// error en vez de una pantalla completa: la página queda como estaba (con el
// texto de la oferta) y el error se muestra al lado del botón.
const asyncHeader = "X-Shiv-Async"

func isAsync(r *http.Request) bool { return r.Header.Get(asyncHeader) == "1" }

type errAction struct {
	Href  string `json:"href"`
	Label string `json:"label"`
}

type asyncError struct {
	Status  int        `json:"status"`
	Heading string     `json:"heading"`
	Message string     `json:"message"`
	Detail  string     `json:"detail,omitempty"`
	Action  *errAction `json:"action,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// actionFor sugiere qué hacer ante errores de la clave: ir a Cuenta.
func actionFor(err error) *errAction {
	if err != nil && (errors.Is(err, ai.ErrNoAPIKey) || ai.IsAuthError(err)) {
		return &errAction{Href: "/account", Label: "Cambiar API key"}
	}
	return nil
}

// errorField saca el campo Error de una página que se re-renderiza con un
// mensaje de validación (login, cuenta, oferta).
func errorField(data any) string {
	v := reflect.ValueOf(data)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	if f := v.FieldByName("Error"); f.IsValid() && f.Kind() == reflect.String {
		return f.String()
	}
	return ""
}

// ---------- verificación de la clave mientras se escribe ----------

type verifyResult struct {
	OK       bool     `json:"ok"`
	Provider string   `json:"provider,omitempty"`
	Label    string   `json:"label,omitempty"`
	Models   []string `json:"models,omitempty"`
	Default  string   `json:"default,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// verifyKey prueba una clave contra su proveedor y devuelve los modelos, para
// llenar el desplegable sin salir del formulario.
func (s *Server) verifyKey(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, verifyResult{Error: "Pedido inválido."})
		return
	}
	key := strings.TrimSpace(r.PostForm.Get("apikey"))
	ctx, cancel := context.WithTimeout(r.Context(), verifyTimeout)
	defer cancel()
	p, models, err := s.ai.Verify(ctx, key)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, verifyResult{Provider: string(p), Label: labelOf(p), Error: upperFirst(err.Error())})
		return
	}
	writeJSON(w, http.StatusOK, verifyResult{
		OK: true, Provider: string(p), Label: p.Label(), Models: models, Default: ai.PickDefault(p, models),
	})
}

func labelOf(p ai.Provider) string {
	if p == "" {
		return ""
	}
	return p.Label()
}
