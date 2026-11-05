// Package web contiene los handlers HTTP y las pantallas de la app.
package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cvtailor/internal/ai"
	"cvtailor/internal/cv"
	"cvtailor/internal/render"
	"cvtailor/internal/store"
)

//go:embed templates/*.html
var templateFS embed.FS

// staticFS guarda el logo y el script de arranque de la animación de carga.
//
//go:embed static
var staticFS embed.FS

const (
	defaultMaxUpload = 20 << 20 // 20 MiB
	maxFormBody      = 1 << 20  // 1 MiB para los formularios de texto
	maxJobBytes      = 40_000
	maxFieldRunes    = 120
	maxKeywordsInput = 100
	maxChangesInput  = 200
	aiTimeout        = 3 * time.Minute
	pdfTimeout       = 90 * time.Second
)

// Options configura el servidor web.
type Options struct {
	Lang           string       // "es" o "en": idioma de los títulos del CV
	AIConfigured   bool         // solo para mostrar el estado en el inicio
	MaxUploadBytes int64        // 0 usa 20 MiB
	Logger         *slog.Logger // nil usa slog.Default()
}

// Server agrupa las dependencias de los handlers.
type Server struct {
	store  *store.Store
	ai     ai.Analyzer
	pdf    render.PDFRenderer
	pdfErr error
	opts   Options
	pages  map[string]*template.Template
}

// New arma el servidor. pdf puede ser nil si no hay Chrome; en ese caso pdfErr
// explica por qué y las pantallas siguen funcionando sin la descarga del PDF.
func New(st *store.Store, an ai.Analyzer, pdf render.PDFRenderer, pdfErr error, opts Options) (*Server, error) {
	if pdf == nil && pdfErr == nil {
		pdfErr = render.ErrNoChrome
	}
	if opts.MaxUploadBytes <= 0 {
		opts.MaxUploadBytes = defaultMaxUpload
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	s := &Server{store: st, ai: an, pdf: pdf, pdfErr: pdfErr, opts: opts, pages: map[string]*template.Template{}}
	funcs := template.FuncMap{
		"lines":       func(ss []string) string { return strings.Join(ss, "\n") },
		"commas":      func(ss []string) string { return strings.Join(ss, ", ") },
		"bulletLines": bulletLines,
		"toJSON": func(v any) (string, error) {
			b, err := json.Marshal(v)
			return string(b), err
		},
		"fmtTime":     func(t time.Time) string { return t.Local().Format("02/01/2006 15:04") },
		"statusLabel": statusLabel,
	}
	for _, name := range []string{"home", "versions", "cv", "job_new", "analysis", "changes", "version", "error"} {
		t, err := template.New(name).Funcs(funcs).ParseFS(templateFS,
			"templates/layout.html", "templates/partials.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("plantilla %s: %w", name, err)
		}
		s.pages[name] = t
	}
	return s, nil
}

// Handler devuelve el http.Handler con todas las rutas.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("POST /cv/import", s.importCV)
	mux.HandleFunc("POST /cv/sample", s.loadSample)
	mux.HandleFunc("GET /cv", s.editCV)
	mux.HandleFunc("POST /cv", s.saveCV)
	mux.HandleFunc("GET /cv/preview", s.previewMaster)
	mux.HandleFunc("GET /cv/pdf", s.pdfMaster)
	mux.HandleFunc("GET /jobs/new", s.newJob)
	mux.HandleFunc("POST /jobs/analyze", s.analyze)
	mux.HandleFunc("POST /jobs/tailor", s.tailor)
	mux.HandleFunc("GET /versions", s.versions)
	mux.HandleFunc("POST /versions", s.createVersion)
	mux.HandleFunc("GET /versions/{id}", s.showVersion)
	mux.HandleFunc("GET /versions/{id}/preview", s.previewVersion)
	mux.HandleFunc("GET /versions/{id}/pdf", s.pdfVersion)
	mux.Handle("GET /static/", http.FileServerFS(staticFS))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, http.StatusNotFound, "No encontrado", "Esa dirección no existe en la app.", nil)
	})
	return s.secure(mux)
}

// secure agrega cabeceras de seguridad y rechaza POST que vengan de otro sitio:
// la app usa tu clave de IA, así que una página ajena no debe poder dispararla.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'self'; form-action 'self'; base-uri 'none'")
		if r.Method == http.MethodPost && !sameOrigin(r) {
			s.fail(w, r, http.StatusForbidden, "Pedido bloqueado",
				"El pedido vino de otro origen. Abrí la app directamente en tu navegador y reintentá.", nil)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.opts.Logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // clientes que no son navegadores (curl, tests)
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ---------- vistas ----------

type base struct {
	Title string
	Flash string
}

type homePage struct {
	base
	AIConfigured bool
	PDFReady     bool
	PDFError     string
	HasCV        bool
	Name         string
	CVError      string
}

type versionsPage struct {
	base
	Versions        []store.VersionSummary
	VersionProblems []string
}

type cvPage struct {
	base
	CV    *cv.CV
	Error string
}

type jobPage struct {
	base
	HasCV    bool
	JobTitle string
	Company  string
	Text     string
	Error    string
}

type analysisPage struct {
	base
	Job            cv.Job
	Keywords       []cv.Keyword
	HasReplaceable bool
}

type changeView struct {
	Index   int
	Change  cv.Change
	Context string
}

type changesPage struct {
	base
	Job      cv.Job
	Keywords []cv.Keyword
	Valid    []cv.Change
	Changes  []changeView
	Dropped  []cv.Dropped
}

type versionPage struct {
	base
	Version     *cv.Version
	PDFReady    bool
	PDFError    string
	SkippedNote string
}

type errorPage struct {
	base
	Heading string
	Message string
	Detail  string
}

func (s *Server) page(w http.ResponseWriter, status int, name string, data any) {
	t, ok := s.pages[name]
	if !ok {
		http.Error(w, "plantilla desconocida", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.opts.Logger.Error("renderizar página", "page", name, "err", err)
		http.Error(w, "error interno al armar la página", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// fail muestra una pantalla de error legible. err (si hay) va al log y al detalle técnico.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, status int, heading, msg string, err error) {
	data := errorPage{base: base{Title: heading}, Heading: heading, Message: msg}
	if err != nil {
		s.opts.Logger.Error(heading, "method", r.Method, "path", r.URL.Path, "err", err)
		if err.Error() != msg {
			data.Detail = err.Error()
		}
	}
	s.page(w, status, "error", data)
}

func (s *Server) failAI(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ai.ErrNoAPIKey):
		s.fail(w, r, http.StatusServiceUnavailable, "La IA no está configurada",
			"Falta la variable de entorno ANTHROPIC_API_KEY. Definila y reiniciá la app.", err)
	case errors.Is(err, context.DeadlineExceeded):
		s.fail(w, r, http.StatusGatewayTimeout, "La IA tardó demasiado", "La IA no respondió a tiempo. Reintentá en un momento.", err)
	case errors.Is(err, context.Canceled):
		s.fail(w, r, 499, "Pedido cancelado", "El pedido se canceló antes de terminar.", err)
	default:
		s.fail(w, r, http.StatusBadGateway, "La IA no pudo completar el pedido", err.Error(), err)
	}
}

func (s *Server) loadMaster(w http.ResponseWriter, r *http.Request) (*cv.CV, bool) {
	c, err := s.store.LoadCV()
	switch {
	case err == nil:
		return c, true
	case errors.Is(err, store.ErrNotFound):
		s.fail(w, r, http.StatusNotFound, "Falta el CV maestro",
			"Primero importá un PDF o cargá el CV de ejemplo desde el inicio.", nil)
	default:
		s.fail(w, r, http.StatusInternalServerError, "No se pudo leer el CV maestro", "El archivo cv.json no se pudo leer.", err)
	}
	return nil, false
}

// ---------- inicio y CV maestro ----------

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	data := homePage{base: base{Title: "Inicio"}, AIConfigured: s.opts.AIConfigured, PDFReady: s.pdf != nil}
	if s.pdf == nil {
		data.PDFError = s.pdfErr.Error()
	}
	c, err := s.store.LoadCV()
	switch {
	case err == nil:
		data.HasCV, data.Name = true, c.Basics.Name
		if data.Name == "" {
			data.Name = "(sin nombre)"
		}
	case errors.Is(err, store.ErrNotFound):
	default:
		data.CVError = err.Error()
	}
	s.page(w, http.StatusOK, "home", data)
}

// versions lista las versiones guardadas, una por oferta.
func (s *Server) versions(w http.ResponseWriter, r *http.Request) {
	data := versionsPage{base: base{Title: "Versiones"}}
	versions, problems := s.store.ListVersions()
	data.Versions = versions
	for _, p := range problems {
		data.VersionProblems = append(data.VersionProblems, p.Error())
	}
	s.page(w, http.StatusOK, "versions", data)
}

func (s *Server) importCV(w http.ResponseWriter, r *http.Request) {
	limit := s.opts.MaxUploadBytes
	r.Body = http.MaxBytesReader(w, r.Body, limit+(1<<20)) // margen para el encabezado multipart
	file, _, err := r.FormFile("pdf")
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			s.fail(w, r, http.StatusRequestEntityTooLarge, "El PDF es demasiado grande",
				fmt.Sprintf("El máximo es %d MB.", limit>>20), err)
		case errors.Is(err, http.ErrMissingFile):
			s.fail(w, r, http.StatusBadRequest, "Falta el archivo", "No se recibió ningún PDF.", nil)
		default:
			s.fail(w, r, http.StatusBadRequest, "No se pudo leer el archivo", "El formulario de subida no es válido.", err)
		}
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "No se pudo leer el archivo", "Falló la lectura del PDF.", err)
		return
	}
	if int64(len(data)) > limit {
		s.fail(w, r, http.StatusRequestEntityTooLarge, "El PDF es demasiado grande", fmt.Sprintf("El máximo es %d MB.", limit>>20), nil)
		return
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		s.fail(w, r, http.StatusBadRequest, "No es un PDF", "El archivo subido no parece un PDF.", nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), aiTimeout)
	defer cancel()
	c, err := s.ai.ParseCV(ctx, data)
	if err != nil {
		s.failAI(w, r, err)
		return
	}
	if err := c.Prepare(); err != nil {
		s.fail(w, r, http.StatusUnprocessableEntity, "No se pudo leer el CV",
			"La IA no encontró contenido en el PDF. Si es un PDF escaneado como imagen, probá con otro.", err)
		return
	}
	if err := s.store.BackupCV(); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo respaldar el CV actual", "No se reemplazó nada.", err)
		return
	}
	if err := s.store.SaveCV(c); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo guardar el CV", "No se pudo escribir cv.json.", err)
		return
	}
	http.Redirect(w, r, "/cv?imported=1", http.StatusSeeOther)
}

func (s *Server) loadSample(w http.ResponseWriter, r *http.Request) {
	if err := s.store.BackupCV(); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo respaldar el CV actual", "No se reemplazó nada.", err)
		return
	}
	if err := s.store.SaveCV(cv.Sample()); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo guardar el CV", "No se pudo escribir cv.json.", err)
		return
	}
	http.Redirect(w, r, "/cv?sample=1", http.StatusSeeOther)
}

func (s *Server) editCV(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.LoadCV()
	if errors.Is(err, store.ErrNotFound) {
		c = &cv.CV{} // se puede armar desde cero
	} else if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo leer el CV maestro", "El archivo cv.json no se pudo leer.", err)
		return
	}
	data := cvPage{base: base{Title: "CV maestro"}, CV: padded(c)}
	switch {
	case r.URL.Query().Get("imported") == "1":
		data.Flash = "CV importado. Revisalo antes de usarlo: la IA puede equivocarse."
	case r.URL.Query().Get("sample") == "1":
		data.Flash = "Cargué un CV de ejemplo con datos inventados."
	case r.URL.Query().Get("saved") == "1":
		data.Flash = "Cambios guardados."
	}
	s.page(w, http.StatusOK, "cv", data)
}

func (s *Server) saveCV(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		s.failForm(w, r, err)
		return
	}
	c := parseCVForm(r.PostForm)
	if err := c.Prepare(); err != nil {
		s.page(w, http.StatusBadRequest, "cv", cvPage{
			base:  base{Title: "CV maestro"},
			CV:    padded(c),
			Error: "No se guardó: el CV está vacío. Completá al menos un dato.",
		})
		return
	}
	if err := s.store.SaveCV(c); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo guardar el CV", "No se pudo escribir cv.json.", err)
		return
	}
	http.Redirect(w, r, "/cv?saved=1", http.StatusSeeOther)
}

func (s *Server) failForm(w http.ResponseWriter, r *http.Request, err error) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		s.fail(w, r, http.StatusRequestEntityTooLarge, "Formulario demasiado grande", "El texto enviado supera el máximo permitido.", err)
		return
	}
	s.fail(w, r, http.StatusBadRequest, "Formulario inválido", "No se pudo leer el formulario.", err)
}

// padded devuelve una copia con una fila en blanco al final de cada sección,
// para poder agregar entradas sin JavaScript.
func padded(c *cv.CV) *cv.CV {
	out := c.Clone()
	out.Basics.Links = append(out.Basics.Links, cv.Link{})
	out.Education = append(out.Education, cv.Education{})
	out.Experience = append(out.Experience, cv.Experience{})
	out.Projects = append(out.Projects, cv.Project{})
	out.Skills = append(out.Skills, cv.SkillGroup{})
	return out
}

var fieldRe = regexp.MustCompile(`^(link|edu|exp|proj|skill)\.(\d{1,3})\.([a-z]+)$`)

// parseCVForm arma un CV desde los campos indexados del formulario (exp.0.company, etc.).
func parseCVForm(f url.Values) *cv.CV {
	type row map[string]string
	rows := map[string]map[int]row{}
	for key, vals := range f {
		m := fieldRe.FindStringSubmatch(key)
		if m == nil || len(vals) == 0 {
			continue
		}
		idx, _ := strconv.Atoi(m[2])
		if rows[m[1]] == nil {
			rows[m[1]] = map[int]row{}
		}
		if rows[m[1]][idx] == nil {
			rows[m[1]][idx] = row{}
		}
		rows[m[1]][idx][m[3]] = vals[0]
	}
	ordered := func(kind string) []row {
		idxs := make([]int, 0, len(rows[kind]))
		for i := range rows[kind] {
			idxs = append(idxs, i)
		}
		sort.Ints(idxs)
		out := make([]row, 0, len(idxs))
		for _, i := range idxs {
			out = append(out, rows[kind][i])
		}
		return out
	}

	c := &cv.CV{}
	c.Basics = cv.Basics{
		Name: f.Get("basics.name"), Email: f.Get("basics.email"),
		Phone: f.Get("basics.phone"), Location: f.Get("basics.location"),
	}
	for _, r := range ordered("link") {
		c.Basics.Links = append(c.Basics.Links, cv.Link{Label: r["label"], URL: r["url"]})
	}
	c.Summary = f.Get("summary")
	for _, r := range ordered("edu") {
		c.Education = append(c.Education, cv.Education{
			Institution: r["institution"], Degree: r["degree"], Period: r["period"], Details: splitLines(r["details"]),
		})
	}
	for _, r := range ordered("exp") {
		c.Experience = append(c.Experience, cv.Experience{
			Company: r["company"], Role: r["role"], Period: r["period"], Bullets: toBullets(splitLines(r["bullets"])),
		})
	}
	for _, r := range ordered("proj") {
		c.Projects = append(c.Projects, cv.Project{Name: r["name"], Bullets: toBullets(splitLines(r["bullets"]))})
	}
	for _, r := range ordered("skill") {
		c.Skills = append(c.Skills, cv.SkillGroup{Category: r["category"], Items: splitItems(r["items"])})
	}
	c.Unparsed = splitLines(f.Get("unparsed"))
	return c
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	out := []string{}
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitItems(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", ",")
	s = strings.ReplaceAll(s, "\n", ",")
	out := []string{}
	for _, it := range strings.Split(s, ",") {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}

func toBullets(lines []string) []cv.Bullet {
	out := make([]cv.Bullet, len(lines))
	for i, l := range lines {
		out[i] = cv.Bullet{Text: l}
	}
	return out
}

func bulletLines(bs []cv.Bullet) string {
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = b.Text
	}
	return strings.Join(parts, "\n")
}

func statusLabel(st string) string {
	switch st {
	case cv.StatusPresent:
		return "Ya está en tu CV"
	case cv.StatusReplaceable:
		return "Reemplazable por sinónimo"
	case cv.StatusMissing:
		return "Falta en tu CV (no se agrega)"
	}
	return st
}

// ---------- oferta, keywords y cambios ----------

func (s *Server) newJob(w http.ResponseWriter, r *http.Request) {
	// La oferta nueva se carga desde el inicio.
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// readJob lee y valida los datos de la oferta del formulario. Si devuelve un
// mensaje, la oferta no es válida.
func (s *Server) readJob(w http.ResponseWriter, r *http.Request) (cv.Job, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		return cv.Job{}, "", err
	}
	job := cv.Job{
		Title:   truncateRunes(strings.TrimSpace(r.PostForm.Get("title")), maxFieldRunes),
		Company: truncateRunes(strings.TrimSpace(r.PostForm.Get("company")), maxFieldRunes),
		Text:    strings.TrimSpace(r.PostForm.Get("text")),
	}
	switch {
	case job.Text == "":
		return job, "Pegá el texto de la oferta.", nil
	case len(job.Text) > maxJobBytes:
		return job, fmt.Sprintf("El texto de la oferta es demasiado largo (máximo %d caracteres aproximadamente).", maxJobBytes), nil
	}
	return job, "", nil
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func (s *Server) analyze(w http.ResponseWriter, r *http.Request) {
	job, msg, err := s.readJob(w, r)
	if err != nil {
		s.failForm(w, r, err)
		return
	}
	if msg != "" {
		s.page(w, http.StatusBadRequest, "job_new", jobPage{
			base: base{Title: "Nueva oferta"}, HasCV: true, JobTitle: job.Title, Company: job.Company, Text: job.Text, Error: msg,
		})
		return
	}
	master, ok := s.loadMaster(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), aiTimeout)
	defer cancel()
	keywords, err := s.ai.ExtractKeywords(ctx, job.Text, master)
	if err != nil {
		s.failAI(w, r, err)
		return
	}
	keywords = sortedKeywords(keywords)
	replaceable := false
	for _, k := range keywords {
		if k.Status == cv.StatusReplaceable {
			replaceable = true
		}
	}
	s.page(w, http.StatusOK, "analysis", analysisPage{
		base: base{Title: "Keywords"}, Job: job, Keywords: keywords, HasReplaceable: replaceable,
	})
}

func sortedKeywords(in []cv.Keyword) []cv.Keyword {
	rank := map[string]int{cv.StatusReplaceable: 0, cv.StatusMissing: 1, cv.StatusPresent: 2}
	out := append([]cv.Keyword(nil), in...)
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Status] < rank[out[j].Status] })
	return out
}

func (s *Server) tailor(w http.ResponseWriter, r *http.Request) {
	job, msg, err := s.readJob(w, r)
	if err != nil {
		s.failForm(w, r, err)
		return
	}
	if msg != "" {
		s.fail(w, r, http.StatusBadRequest, "Oferta inválida", msg, nil)
		return
	}
	keywords, err := parseKeywords(r.PostForm.Get("keywords"))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Keywords inválidas", "Volvé a analizar la oferta.", err)
		return
	}
	master, ok := s.loadMaster(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), aiTimeout)
	defer cancel()
	proposals, err := s.ai.ProposeChanges(ctx, job.Text, master, keywords)
	if err != nil {
		s.failAI(w, r, err)
		return
	}
	valid, dropped := cv.ValidateChanges(master, proposals)
	views := make([]changeView, len(valid))
	for i, ch := range valid {
		views[i] = changeView{Index: i, Change: ch, Context: master.TargetText(ch.TargetID)}
	}
	s.page(w, http.StatusOK, "changes", changesPage{
		base: base{Title: "Cambios"}, Job: job, Keywords: keywords, Valid: valid, Changes: views, Dropped: dropped,
	})
}

func (s *Server) createVersion(w http.ResponseWriter, r *http.Request) {
	job, msg, err := s.readJob(w, r)
	if err != nil {
		s.failForm(w, r, err)
		return
	}
	if msg != "" {
		s.fail(w, r, http.StatusBadRequest, "Oferta inválida", msg, nil)
		return
	}
	keywords, err := parseKeywords(r.PostForm.Get("keywords"))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Keywords inválidas", "Volvé a analizar la oferta.", err)
		return
	}
	changes, err := parseChanges(r.PostForm.Get("changes"))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Cambios inválidos", "Volvé a generar las propuestas.", err)
		return
	}
	for i := range changes {
		changes[i].Accepted = false // solo vale lo que se tildó
	}
	for _, v := range r.PostForm["accept"] {
		i, err := strconv.Atoi(v)
		if err != nil || i < 0 || i >= len(changes) {
			s.fail(w, r, http.StatusBadRequest, "Selección inválida", "Volvé a generar las propuestas.", nil)
			return
		}
		changes[i].Accepted = true
	}
	master, ok := s.loadMaster(w, r)
	if !ok {
		return
	}

	// La validación anti-invención se repite acá: nada de lo que llega del
	// formulario se aplica sin pasar de nuevo por Go.
	valid, dropped := cv.ValidateChanges(master, changes)
	if len(dropped) > 0 {
		reasons := make([]string, len(dropped))
		for i, d := range dropped {
			reasons[i] = d.Change.TargetID + ": " + d.Why
		}
		s.fail(w, r, http.StatusConflict, "Las propuestas ya no coinciden con tu CV",
			"El CV maestro cambió desde que se generaron, o el formulario fue alterado. Volvé a generar las propuestas.",
			errors.New(strings.Join(reasons, "; ")))
		return
	}
	applied, skipped := cv.ApplyAccepted(master, valid)
	for i := range valid {
		for _, sk := range skipped {
			if sk.Change.TargetID == valid[i].TargetID && sk.Change.Before == valid[i].Before && sk.Change.After == valid[i].After {
				valid[i].Accepted = false
			}
		}
	}

	version := &cv.Version{Job: job, Keywords: keywords, Changes: valid, CV: *applied}
	if err := s.store.SaveVersion(version); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo guardar la versión", "No se pudo escribir el archivo de la versión.", err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/versions/%s?skipped=%d", version.ID, len(skipped)), http.StatusSeeOther)
}

func parseKeywords(s string) ([]cv.Keyword, error) {
	var ks []cv.Keyword
	if err := json.Unmarshal([]byte(s), &ks); err != nil {
		return nil, fmt.Errorf("keywords: %w", err)
	}
	if len(ks) > maxKeywordsInput {
		return nil, errors.New("demasiadas keywords")
	}
	for i := range ks {
		ks[i].Term = strings.TrimSpace(ks[i].Term)
		if ks[i].Term == "" || !cv.ValidStatus(ks[i].Status) {
			return nil, fmt.Errorf("keyword inválida en la posición %d", i)
		}
	}
	return ks, nil
}

func parseChanges(s string) ([]cv.Change, error) {
	var cs []cv.Change
	if err := json.Unmarshal([]byte(s), &cs); err != nil {
		return nil, fmt.Errorf("cambios: %w", err)
	}
	if len(cs) > maxChangesInput {
		return nil, errors.New("demasiados cambios")
	}
	return cs, nil
}

// ---------- versiones, vistas previas y PDF ----------

func (s *Server) loadVersion(w http.ResponseWriter, r *http.Request) (*cv.Version, bool) {
	v, err := s.store.LoadVersion(r.PathValue("id"))
	switch {
	case err == nil:
		return v, true
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrBadID):
		s.fail(w, r, http.StatusNotFound, "Versión no encontrada", "Esa versión no existe.", nil)
	default:
		s.fail(w, r, http.StatusInternalServerError, "No se pudo leer la versión", "El archivo de la versión no se pudo leer.", err)
	}
	return nil, false
}

func (s *Server) showVersion(w http.ResponseWriter, r *http.Request) {
	v, ok := s.loadVersion(w, r)
	if !ok {
		return
	}
	data := versionPage{base: base{Title: "Versión"}, Version: v, PDFReady: s.pdf != nil}
	if s.pdf == nil {
		data.PDFError = s.pdfErr.Error()
	}
	if n, _ := strconv.Atoi(r.URL.Query().Get("skipped")); n > 0 {
		data.SkippedNote = fmt.Sprintf("Se omitieron %d cambio(s) porque se pisaban con otro sobre el mismo texto.", n)
	}
	s.page(w, http.StatusOK, "version", data)
}

func (s *Server) previewVersion(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.loadVersion(w, r); ok {
		s.servePreview(w, r, &v.CV)
	}
}

func (s *Server) pdfVersion(w http.ResponseWriter, r *http.Request) {
	if v, ok := s.loadVersion(w, r); ok {
		s.servePDF(w, r, &v.CV, "cv-"+v.ID+".pdf")
	}
}

func (s *Server) previewMaster(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.loadMaster(w, r); ok {
		s.servePreview(w, r, c)
	}
}

func (s *Server) pdfMaster(w http.ResponseWriter, r *http.Request) {
	if c, ok := s.loadMaster(w, r); ok {
		s.servePDF(w, r, c, "cv-maestro.pdf")
	}
}

func (s *Server) servePreview(w http.ResponseWriter, r *http.Request, c *cv.CV) {
	html, err := render.HTML(c, s.opts.Lang)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo armar la vista previa", "Falló la plantilla del CV.", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'self'")
	_, _ = w.Write(html)
}

func (s *Server) servePDF(w http.ResponseWriter, r *http.Request, c *cv.CV, filename string) {
	if s.pdf == nil {
		s.fail(w, r, http.StatusServiceUnavailable, "No se puede generar el PDF", s.pdfErr.Error(), s.pdfErr)
		return
	}
	html, err := render.HTML(c, s.opts.Lang)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "No se pudo armar el PDF", "Falló la plantilla del CV.", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pdfTimeout)
	defer cancel()
	pdf, err := s.pdf.PDF(ctx, html)
	if err != nil {
		s.fail(w, r, http.StatusBadGateway, "No se pudo generar el PDF", err.Error(), err)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(pdf)))
	_, _ = w.Write(pdf)
}
