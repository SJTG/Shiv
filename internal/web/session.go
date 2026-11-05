package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cvtailor/internal/ai"
)

const (
	sessionCookie   = "shiv_session"
	maxUsernameRune = 40
	verifyTimeout   = 30 * time.Second
)

// AIProvider es lo que la web necesita para el BYOK: verificar una clave
// (detectando su proveedor) y armar un cliente con ella.
type AIProvider interface {
	Verify(ctx context.Context, apiKey string) (ai.Provider, []string, error)
	Analyzer(p ai.Provider, apiKey, model string) ai.Analyzer
}

// Account es la "cuenta" temporal: un nombre y una API key. Vive solo en la
// memoria del servidor; la cookie lleva un token al azar, nunca la clave.
type Account struct {
	Username string
	Provider ai.Provider
	APIKey   string
	Model    string
	Models   []string
}

// ProviderLabel es el nombre del proveedor para mostrar.
func (a *Account) ProviderLabel() string { return a.Provider.Label() }

// SupportsPDF informa si con esta clave se puede importar un PDF.
func (a *Account) SupportsPDF() bool { return a.Provider.SupportsPDF() }

// MaskedKey muestra el principio y el final de la clave, nunca la clave entera.
func (a *Account) MaskedKey() string {
	k := a.APIKey
	if len(k) <= 12 {
		return strings.Repeat("•", 8)
	}
	return k[:6] + "…" + k[len(k)-4:]
}

func (a *Account) hasModel(m string) bool {
	for _, x := range a.Models {
		if x == m {
			return true
		}
	}
	return false
}

type sessionStore struct {
	mu sync.RWMutex
	m  map[string]Account
}

func newSessionStore() *sessionStore { return &sessionStore{m: map[string]Account{}} }

func (s *sessionStore) get(id string) (Account, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.m[id]
	return a, ok
}

func (s *sessionStore) set(id string, a Account) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = a
}

func (s *sessionStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
}

func newSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand no falla en sistemas soportados
	}
	return hex.EncodeToString(b)
}

type ctxKey struct{}

type sessionCtx struct {
	id  string
	acc Account
}

// session devuelve la sesión del pedido (solo en rutas protegidas).
func session(r *http.Request) (string, *Account) {
	if sc, ok := r.Context().Value(ctxKey{}).(*sessionCtx); ok {
		return sc.id, &sc.acc
	}
	return "", nil
}

func (s *Server) lookup(r *http.Request) (string, Account, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", Account{}, false
	}
	a, ok := s.sessions.get(c.Value)
	return c.Value, a, ok
}

// requireAccount manda a /login a quien no tenga una sesión con clave.
func (s *Server) requireAccount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, acc, ok := s.lookup(r)
		if !ok {
			if isAsync(r) {
				writeJSON(w, http.StatusUnauthorized, asyncError{
					Status: http.StatusUnauthorized, Heading: "Tu sesión expiró",
					Message: "Volvé a entrar con tu API key. Lo que escribiste queda guardado en este navegador.",
					Action:  &errAction{Href: "/login", Label: "Entrar de nuevo"},
				})
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKey{}, &sessionCtx{id: id, acc: acc})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, id string) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

// cleanUsername valida el nombre temporal. Devuelve un mensaje si no sirve.
func cleanUsername(s string) (string, string) {
	s = strings.Join(strings.Fields(s), " ")
	switch {
	case s == "":
		return "", "Elegí un nombre de usuario."
	case utf8.RuneCountInString(s) > maxUsernameRune:
		return s, "El nombre de usuario es demasiado largo (máximo 40 caracteres)."
	}
	return s, ""
}

// safeNext evita redirigir fuera de la app.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

// ---------- handlers de cuenta ----------

type loginPage struct {
	base
	Username string
	Error    string
}

type accountPage struct {
	base
	Error string
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.lookup(r); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.page(w, r, http.StatusOK, "login", loginPage{base: base{Title: "Entrar"}})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		s.failForm(w, r, err)
		return
	}
	name, msg := cleanUsername(r.PostForm.Get("username"))
	key := strings.TrimSpace(r.PostForm.Get("apikey"))
	if msg == "" && key == "" {
		msg = "Pegá tu API key."
	}
	var acc Account
	if msg == "" {
		ctx, cancel := context.WithTimeout(r.Context(), verifyTimeout)
		defer cancel()
		p, models, err := s.ai.Verify(ctx, key)
		if err != nil {
			msg = upperFirst(err.Error())
		} else {
			acc = Account{Username: name, Provider: p, APIKey: key, Models: models, Model: ai.PickDefault(p, models)}
			// El modelo elegido en el desplegable del ingreso, si es válido.
			if m := strings.TrimSpace(r.PostForm.Get("model")); acc.hasModel(m) {
				acc.Model = m
			}
		}
	}
	if msg != "" {
		s.page(w, r, http.StatusBadRequest, "login", loginPage{base: base{Title: "Entrar"}, Username: name, Error: msg})
		return
	}
	// Si ya había una sesión en este navegador, se reemplaza.
	if old, _, ok := s.lookup(r); ok {
		s.sessions.delete(old)
	}
	id := newSessionID()
	s.sessions.set(id, acc)
	setSessionCookie(w, r, id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) setModel(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		s.failForm(w, r, err)
		return
	}
	id, acc := session(r)
	model := strings.TrimSpace(r.PostForm.Get("model"))
	if !acc.hasModel(model) {
		s.fail(w, r, http.StatusBadRequest, "Modelo inválido", "Ese modelo no está disponible para tu clave.", nil)
		return
	}
	acc.Model = model
	s.sessions.set(id, *acc)
	http.Redirect(w, r, safeNext(r.PostForm.Get("next")), http.StatusSeeOther)
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	data := accountPage{base: base{Title: "Cuenta"}}
	if r.URL.Query().Get("saved") == "1" {
		data.Flash = "Cuenta actualizada."
	}
	s.page(w, r, http.StatusOK, "account", data)
}

func (s *Server) saveAccount(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		s.failForm(w, r, err)
		return
	}
	id, cur := session(r)
	acc := *cur
	name, msg := cleanUsername(r.PostForm.Get("username"))
	key := strings.TrimSpace(r.PostForm.Get("apikey"))
	model := strings.TrimSpace(r.PostForm.Get("model"))
	if msg == "" {
		acc.Username = name
		if key != "" && key != acc.APIKey {
			ctx, cancel := context.WithTimeout(r.Context(), verifyTimeout)
			defer cancel()
			p, models, err := s.ai.Verify(ctx, key)
			if err != nil {
				msg = upperFirst(err.Error()) + " Tu clave anterior sigue activa."
			} else {
				changed := p != acc.Provider
				acc.Provider, acc.APIKey, acc.Models = p, key, models
				if changed || !acc.hasModel(model) {
					model = ai.PickDefault(p, models)
				}
			}
		}
	}
	if msg == "" && !acc.hasModel(model) {
		msg = "Ese modelo no está disponible para tu clave."
	}
	if msg != "" {
		s.page(w, r, http.StatusBadRequest, "account", accountPage{base: base{Title: "Cuenta"}, Error: msg})
		return
	}
	acc.Model = model
	s.sessions.set(id, acc)
	http.Redirect(w, r, "/account?saved=1", http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	id, _ := session(r)
	s.sessions.delete(id)
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return strings.ToUpper(string(r)) + s[n:]
}
