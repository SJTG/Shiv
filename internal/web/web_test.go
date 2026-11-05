package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"cvtailor/internal/ai"
	"cvtailor/internal/cv"
	"cvtailor/internal/render"
	"cvtailor/internal/store"
)

// ---------- dobles de prueba ----------

type fakeAI struct {
	parseResult *cv.CV
	parseErr    error
	keywords    []cv.Keyword
	changes     []cv.Change
	calls       struct{ parse, keywords, changes int }
	gotPDF      []byte
}

func (f *fakeAI) ParseCV(_ context.Context, pdf []byte) (*cv.CV, error) {
	f.calls.parse++
	f.gotPDF = pdf
	return f.parseResult, f.parseErr
}

func (f *fakeAI) ExtractKeywords(_ context.Context, _ string, _ *cv.CV) ([]cv.Keyword, error) {
	f.calls.keywords++
	return f.keywords, nil
}

func (f *fakeAI) ProposeChanges(_ context.Context, _ string, _ *cv.CV, _ []cv.Keyword) ([]cv.Change, error) {
	f.calls.changes++
	return f.changes, nil
}

type fakePDF struct{ lastHTML []byte }

func (f *fakePDF) PDF(_ context.Context, htmlDoc []byte) ([]byte, error) {
	f.lastHTML = htmlDoc
	return []byte("%PDF-1.4 falso"), nil
}

type env struct {
	h     http.Handler
	store *store.Store
	dir   string
	ai    *fakeAI
	pdf   *fakePDF
}

func newEnv(t *testing.T, opts Options) *env {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{store: st, dir: dir, ai: &fakeAI{}, pdf: &fakePDF{}}
	opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if opts.Lang == "" {
		opts.Lang = "es"
	}
	srv, err := New(st, e.ai, e.pdf, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	e.h = srv.Handler()
	return e
}

func (e *env) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *env) get(path string) *httptest.ResponseRecorder {
	return e.do(httptest.NewRequest(http.MethodGet, path, nil))
}

func (e *env) post(path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return e.do(req)
}

func (e *env) upload(path string, content []byte) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("pdf", "cv.pdf")
	if err != nil {
		panic(err)
	}
	_, _ = fw.Write(content)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return e.do(req)
}

func (e *env) loadSample(t *testing.T) {
	t.Helper()
	if rec := e.post("/cv/sample", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("cargar ejemplo: status %d", rec.Code)
	}
}

func hidden(t *testing.T, body, name string) string {
	t.Helper()
	re := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]*)"`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no encontré el campo oculto %q en:\n%s", name, body)
	}
	return html.UnescapeString(m[1])
}

var (
	skillChange    = cv.Change{TargetID: "skills-2", Before: "Postgres", After: "PostgreSQL", Keyword: "PostgreSQL", Reason: "es el mismo motor"}
	bulletChange   = cv.Change{TargetID: "exp-1-b3", Before: "Coordiné las revisiones", After: "Lideré las revisiones", Keyword: "liderazgo", Reason: "coordinar el equipo es liderarlo"}
	inventedChange = cv.Change{TargetID: "exp-1-b1", Before: "servicios REST en Go", After: "servicios REST en Kubernetes", Keyword: "microservicios", Reason: "inventado"}
)

func sampleKeywords() []cv.Keyword {
	return []cv.Keyword{
		{Term: "Go", Status: cv.StatusPresent},
		{Term: "PostgreSQL", Status: cv.StatusReplaceable},
		{Term: "liderazgo", Status: cv.StatusReplaceable},
		{Term: "Kafka", Status: cv.StatusMissing},
	}
}

// ---------- pruebas ----------

func TestHomeEmptyState(t *testing.T) {
	e := newEnv(t, Options{})
	rec := e.get("/")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Todavía no cargaste tu CV", "falta ANTHROPIC_API_KEY", "Chrome detectado", "Todavía no generaste ninguna versión"} {
		if !strings.Contains(body, want) {
			t.Errorf("la home no contiene %q", want)
		}
	}
}

func TestUnknownPathIs404(t *testing.T) {
	e := newEnv(t, Options{})
	if rec := e.get("/no/existe"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "No encontrado") {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestSampleThenEditPage(t *testing.T) {
	e := newEnv(t, Options{})
	rec := e.post("/cv/sample", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/cv?sample=1" {
		t.Fatalf("status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = e.get("/cv?sample=1")
	body := rec.Body.String()
	for _, want := range []string{"Ada Ejemplo", `name="exp.0.company"`, `name="exp.2.company"`, "datos inventados", "Coordiné las revisiones"} {
		if !strings.Contains(body, want) {
			t.Errorf("la pantalla del CV no contiene %q", want)
		}
	}
}

func TestEditPageWithoutCVAllowsStartingFromScratch(t *testing.T) {
	e := newEnv(t, Options{})
	rec := e.get("/cv")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `name="basics.name"`) {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestSaveCVFromForm(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	form := url.Values{
		"basics.name":       {"  Nuevo Nombre "},
		"exp.0.company":     {"Acme"},
		"exp.0.role":        {"Dev"},
		"exp.0.bullets":     {"uno\r\n\r\ndos"},
		"exp.1.company":     {""}, // fila en blanco: se descarta
		"skill.0.category":  {"Lenguajes"},
		"skill.0.items":     {"Go, Rust,, "},
		"link.0.label":      {"GitHub"},
		"link.0.url":        {"github.com/x"},
		"unparsed":          {""},
		"edu.0.institution": {"Uni"},
		"edu.0.details":     {"detalle"},
	}
	rec := e.post("/cv", form)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/cv?saved=1" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got, err := e.store.LoadCV()
	if err != nil {
		t.Fatal(err)
	}
	if got.Basics.Name != "Nuevo Nombre" || len(got.Experience) != 1 {
		t.Fatalf("CV guardado: %+v", got)
	}
	b := got.Experience[0].Bullets
	if len(b) != 2 || b[0].ID != "exp-1-b1" || b[1].ID != "exp-1-b2" || b[1].Text != "dos" {
		t.Fatalf("bullets = %+v", b)
	}
	if len(got.Skills) != 1 || strings.Join(got.Skills[0].Items, "|") != "Go|Rust" || got.Skills[0].ID != "skills-1" {
		t.Fatalf("skills = %+v", got.Skills)
	}
	if len(got.Basics.Links) != 1 || len(got.Education) != 1 || got.Education[0].Details[0] != "detalle" {
		t.Fatalf("links/education = %+v / %+v", got.Basics.Links, got.Education)
	}
}

func TestSaveCVEmptyIsRejectedAndKeepsPreviousCV(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	rec := e.post("/cv", url.Values{"basics.name": {"  "}})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "está vacío") {
		t.Fatalf("status %d", rec.Code)
	}
	got, err := e.store.LoadCV()
	if err != nil || got.Basics.Name != "Ada Ejemplo" {
		t.Fatalf("el CV anterior no debía cambiar: %v", err)
	}
}

func TestImportCV(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t) // ya hay un CV maestro: debe quedar respaldado
	imported := &cv.CV{Basics: cv.Basics{Name: "Persona Importada"}, Experience: []cv.Experience{{Company: "X", Bullets: []cv.Bullet{{Text: "hizo algo"}}}}}
	e.ai.parseResult = imported

	rec := e.upload("/cv/import", []byte("%PDF-1.7 contenido"))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/cv?imported=1" {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if string(e.ai.gotPDF) != "%PDF-1.7 contenido" {
		t.Fatalf("la IA recibió %q", e.ai.gotPDF)
	}
	got, _ := e.store.LoadCV()
	if got.Basics.Name != "Persona Importada" || got.Experience[0].Bullets[0].ID != "exp-1-b1" {
		t.Fatalf("CV guardado: %+v", got)
	}
	backup, err := os.ReadFile(filepath.Join(e.dir, "cv.backup.json"))
	if err != nil || !strings.Contains(string(backup), "Ada Ejemplo") {
		t.Fatalf("el respaldo del CV anterior falta o es incorrecto: %v", err)
	}
}

func TestImportRejectsNonPDFWithoutCallingAI(t *testing.T) {
	e := newEnv(t, Options{})
	rec := e.upload("/cv/import", []byte("<html>no soy un pdf</html>"))
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "No es un PDF") {
		t.Fatalf("status %d", rec.Code)
	}
	if e.ai.calls.parse != 0 {
		t.Fatal("no debía llamar a la IA")
	}
}

func TestImportRejectsOversizedPDF(t *testing.T) {
	e := newEnv(t, Options{MaxUploadBytes: 1024})
	rec := e.upload("/cv/import", append([]byte("%PDF-"), bytes.Repeat([]byte("x"), 5000)...))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", rec.Code)
	}
	if e.ai.calls.parse != 0 {
		t.Fatal("no debía llamar a la IA")
	}
}

func TestImportMissingFile(t *testing.T) {
	e := newEnv(t, Options{})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/cv/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if rec := e.do(req); rec.Code != 400 || !strings.Contains(rec.Body.String(), "Falta el archivo") {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestImportAIFailureKeepsCurrentCVAndExplains(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	e.ai.parseErr = ai.ErrNoAPIKey
	rec := e.upload("/cv/import", []byte("%PDF-1.7 x"))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "ANTHROPIC_API_KEY") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got, err := e.store.LoadCV()
	if err != nil || got.Basics.Name != "Ada Ejemplo" {
		t.Fatalf("el CV maestro no debía cambiar: %v", err)
	}

	e.ai.parseErr = errors.New("se alcanzó el límite de uso de la API (HTTP 429)")
	rec = e.upload("/cv/import", []byte("%PDF-1.7 x"))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "límite de uso") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestImportEmptyResultIsReported(t *testing.T) {
	e := newEnv(t, Options{})
	e.ai.parseResult = &cv.CV{}
	rec := e.upload("/cv/import", []byte("%PDF-1.7 x"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d", rec.Code)
	}
	if _, err := e.store.LoadCV(); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("no debía guardarse un CV vacío")
	}
}

func TestAnalyzeValidatesInput(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	rec := e.post("/jobs/analyze", url.Values{"text": {"   "}, "title": {"Dev"}})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "Pegá el texto") || !strings.Contains(rec.Body.String(), `value="Dev"`) {
		t.Fatalf("status %d", rec.Code)
	}
	rec = e.post("/jobs/analyze", url.Values{"text": {strings.Repeat("a", maxJobBytes+1)}})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "demasiado largo") {
		t.Fatalf("status %d", rec.Code)
	}
	if e.ai.calls.keywords != 0 {
		t.Fatal("no debía llamar a la IA con una oferta inválida")
	}
}

func TestAnalyzeWithoutMasterCV(t *testing.T) {
	e := newEnv(t, Options{})
	rec := e.post("/jobs/analyze", url.Values{"text": {"oferta"}})
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "Falta el CV maestro") {
		t.Fatalf("status %d", rec.Code)
	}
	if e.ai.calls.keywords != 0 {
		t.Fatal("no debía llamar a la IA")
	}
}

// El recorrido completo: oferta, keywords, propuesta, versión, vista previa y PDF.
func TestFullFlowAndAntiInventionGuarantees(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	e.ai.keywords = sampleKeywords()
	e.ai.changes = []cv.Change{skillChange, bulletChange, inventedChange}
	jobText := "Buscamos <script>alert(1)</script> con PostgreSQL y liderazgo"

	// 1. Analizar: keywords ordenadas (reemplazables primero) y texto escapado.
	rec := e.post("/jobs/analyze", url.Values{"title": {"Backend Dev"}, "company": {"Acme"}, "text": {jobText}})
	if rec.Code != 200 {
		t.Fatalf("analyze status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert") {
		t.Fatal("el texto de la oferta no se escapó")
	}
	if strings.Index(body, "PostgreSQL") > strings.Index(body, "Kafka") {
		t.Error("las keywords reemplazables deberían ir antes que las faltantes")
	}
	for _, want := range []string{"Reemplazable por sinónimo", "Falta en tu CV (no se agrega)", "Ya está en tu CV"} {
		if !strings.Contains(body, want) {
			t.Errorf("falta la etiqueta %q", want)
		}
	}
	keywordsJSON := hidden(t, body, "keywords")
	if e.ai.calls.keywords != 1 {
		t.Fatalf("llamadas a keywords = %d", e.ai.calls.keywords)
	}

	// 2. Proponer cambios: el inventado se descarta con su motivo y no se ofrece.
	rec = e.post("/jobs/tailor", url.Values{"title": {"Backend Dev"}, "company": {"Acme"}, "text": {jobText}, "keywords": {keywordsJSON}})
	if rec.Code != 200 {
		t.Fatalf("tailor status %d: %s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	if !strings.Contains(body, "Descartados por la validación") || !strings.Contains(body, "nombres propios") {
		t.Error("el cambio inventado debía aparecer como descartado, con su motivo")
	}
	if !strings.Contains(body, `name="accept" value="0"`) || !strings.Contains(body, `name="accept" value="1"`) ||
		strings.Contains(body, `name="accept" value="2"`) {
		t.Error("solo los dos cambios válidos deben poder aceptarse")
	}
	changesJSON := hidden(t, body, "changes")
	var offered []cv.Change
	if err := json.Unmarshal([]byte(changesJSON), &offered); err != nil || len(offered) != 2 {
		t.Fatalf("cambios ofrecidos = %+v (%v)", offered, err)
	}

	// 3. Crear la versión aceptando solo el cambio de skills (índice 0).
	rec = e.post("/versions", url.Values{
		"title": {"Backend Dev"}, "company": {"Acme"}, "text": {jobText},
		"keywords": {keywordsJSON}, "changes": {changesJSON}, "accept": {"0"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("versions status %d: %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/versions/") || !strings.HasSuffix(loc, "?skipped=0") {
		t.Fatalf("location = %q", loc)
	}
	id := strings.TrimSuffix(strings.TrimPrefix(loc, "/versions/"), "?skipped=0")
	v, err := e.store.LoadVersion(id)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.CV.Skills[1].Items[2]; got != "PostgreSQL" {
		t.Fatalf("el cambio aceptado no se aplicó: %q", got)
	}
	if got := v.CV.Experience[0].Bullets[2].Text; !strings.Contains(got, "Coordiné") {
		t.Fatalf("un cambio no tildado se aplicó: %q", got)
	}
	if v.Job.Company != "Acme" || len(v.Keywords) != 4 || len(v.Changes) != 2 || !v.Changes[0].Accepted || v.Changes[1].Accepted {
		t.Fatalf("versión guardada: %+v", v)
	}
	master, _ := e.store.LoadCV()
	if got := master.Skills[1].Items[2]; got != "Postgres" {
		t.Fatalf("el CV maestro no debe cambiar: %q", got)
	}

	// 4. Pantalla de la versión, vista previa y PDF.
	rec = e.get(loc)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "aplicado") || !strings.Contains(rec.Body.String(), "no aplicado") ||
		!strings.Contains(rec.Body.String(), "/versions/"+id+"/pdf") {
		t.Fatalf("version status %d", rec.Code)
	}
	rec = e.get("/versions/" + id + "/preview")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "PostgreSQL") ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("preview status %d csp %q", rec.Code, rec.Header().Get("Content-Security-Policy"))
	}
	rec = e.get("/versions/" + id + "/pdf")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename="cv-`+id+`.pdf"` || !strings.HasPrefix(rec.Body.String(), "%PDF-") {
		t.Fatalf("pdf status %d headers %v", rec.Code, rec.Header())
	}
	if !strings.Contains(string(e.pdf.lastHTML), "Git, Docker, PostgreSQL") {
		t.Fatal("el PDF se renderizó con el CV sin adaptar")
	}

	// 5. La home lista la versión.
	if body := e.get("/").Body.String(); !strings.Contains(body, "Backend Dev") || !strings.Contains(body, "/versions/"+id) {
		t.Fatal("la home no lista la versión")
	}
}

func TestTamperedChangesAreRejectedAtVersionCreation(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	kw, _ := json.Marshal(sampleKeywords())
	tampered, _ := json.Marshal([]cv.Change{skillChange, inventedChange})

	rec := e.post("/versions", url.Values{"text": {"oferta"}, "keywords": {string(kw)}, "changes": {string(tampered)}, "accept": {"0", "1"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "ya no coinciden") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if list, _ := e.store.ListVersions(); len(list) != 0 {
		t.Fatalf("no debía guardarse ninguna versión: %+v", list)
	}
}

func TestVersionInputValidation(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	kw, _ := json.Marshal(sampleKeywords())
	ch, _ := json.Marshal([]cv.Change{skillChange})
	base := func() url.Values {
		return url.Values{"text": {"oferta"}, "keywords": {string(kw)}, "changes": {string(ch)}}
	}

	f := base()
	f.Set("accept", "7")
	if rec := e.post("/versions", f); rec.Code != 400 {
		t.Errorf("índice fuera de rango: status %d", rec.Code)
	}
	f = base()
	f.Set("keywords", "no es json")
	if rec := e.post("/versions", f); rec.Code != 400 {
		t.Errorf("keywords inválidas: status %d", rec.Code)
	}
	f = base()
	f.Set("keywords", `[{"term":"x","status":"inventado"}]`)
	if rec := e.post("/versions", f); rec.Code != 400 {
		t.Errorf("estado inválido: status %d", rec.Code)
	}
	f = base()
	f.Set("changes", "{")
	if rec := e.post("/versions", f); rec.Code != 400 {
		t.Errorf("cambios inválidos: status %d", rec.Code)
	}
}

func TestVersionWithoutChangesWhenNothingIsReplaceable(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	e.ai.keywords = []cv.Keyword{{Term: "Go", Status: cv.StatusPresent}}
	e.ai.changes = nil
	kw, _ := json.Marshal(e.ai.keywords)
	rec := e.post("/jobs/tailor", url.Values{"text": {"oferta"}, "keywords": {string(kw)}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "No hay cambios válidos") {
		t.Fatalf("status %d", rec.Code)
	}
	rec = e.post("/versions", url.Values{"text": {"oferta"}, "keywords": {string(kw)}, "changes": {hidden(t, rec.Body.String(), "changes")}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestConflictingChangesAreSkippedAndReported(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	first := cv.Change{TargetID: "exp-1-b1", Before: "servicios REST en Go", After: "APIs REST en Go", Keyword: "APIs"}
	second := cv.Change{TargetID: "exp-1-b1", Before: "servicios REST", After: "web services REST", Keyword: "web services"}
	kw, _ := json.Marshal(sampleKeywords())
	ch, _ := json.Marshal([]cv.Change{first, second})
	rec := e.post("/versions", url.Values{"text": {"oferta"}, "keywords": {string(kw)}, "changes": {string(ch)}, "accept": {"0", "1"}})
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "?skipped=1") {
		t.Fatalf("status %d location %q", rec.Code, rec.Header().Get("Location"))
	}
	page := e.get(rec.Header().Get("Location"))
	if !strings.Contains(page.Body.String(), "Se omitieron 1 cambio(s)") {
		t.Fatal("la pantalla debía avisar del cambio omitido")
	}
}

func TestCrossOriginPostsAreBlocked(t *testing.T) {
	e := newEnv(t, Options{})
	tests := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"origen ajeno", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		{"origen null", map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"fetch cross-site", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"mismo origen", map[string]string{"Origin": "http://example.com", "Sec-Fetch-Site": "same-origin"}, http.StatusSeeOther},
		{"sin cabeceras (curl)", nil, http.StatusSeeOther},
	}
	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodPost, "/cv/sample", nil) // httptest usa Host example.com
		for k, v := range tt.headers {
			req.Header.Set(k, v)
		}
		if rec := e.do(req); rec.Code != tt.want {
			t.Errorf("%s: status %d, quería %d", tt.name, rec.Code, tt.want)
		}
	}
}

func TestVersionIDsCannotEscapeTheDataDir(t *testing.T) {
	e := newEnv(t, Options{})
	e.loadSample(t)
	for _, path := range []string{"/versions/nope", "/versions/a%2Fb", "/versions/..%5Ccv", "/versions/%2e%2e%2fcv"} {
		for _, suffix := range []string{"", "/preview", "/pdf"} {
			rec := e.get(path + suffix)
			if rec.Code == 200 {
				t.Errorf("%s%s devolvió 200", path, suffix)
			}
		}
	}
	if rec := e.get("/versions/nope"); rec.Code != 404 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestUserTextIsEscapedInVersionPage(t *testing.T) {
	e := newEnv(t, Options{})
	v := &cv.Version{Job: cv.Job{Title: `<b>negrita</b>`, Company: `<img src=x onerror=1>`}, CV: *cv.Sample(),
		Keywords: []cv.Keyword{{Term: `<i>k</i>`, Status: cv.StatusMissing}},
		Changes:  []cv.Change{{TargetID: "summary", Before: "<u>a</u>", After: "<u>b</u>", Keyword: "<s>k</s>", Accepted: true}}}
	if err := e.store.SaveVersion(v); err != nil {
		t.Fatal(err)
	}
	body := e.get("/versions/" + v.ID).Body.String()
	for _, raw := range []string{"<b>negrita", "<img src=x", "<i>k</i>", "<u>a</u>", "<s>k</s>"} {
		if strings.Contains(body, raw) {
			t.Errorf("HTML sin escapar: %q", raw)
		}
	}
}

func TestPDFUnavailableDegradesGracefully(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	st, _ := store.New(dir)
	srv, err := New(st, &fakeAI{}, nil, render.ErrNoChrome, Options{Lang: "es", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{h: srv.Handler(), store: st, dir: dir}
	e.loadSample(t)

	if rec := e.get("/cv/pdf"); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "Chrome") {
		t.Fatalf("pdf status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.get("/cv/preview"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Ada Ejemplo") {
		t.Fatalf("la vista previa debe funcionar sin Chrome: status %d", rec.Code)
	}
	if body := e.get("/").Body.String(); !strings.Contains(body, "no se encontró Chrome") {
		t.Fatal("la home debía avisar que falta Chrome")
	}
}

func TestMasterPDFAndPreview(t *testing.T) {
	e := newEnv(t, Options{Lang: "en"})
	e.loadSample(t)
	rec := e.get("/cv/pdf")
	if rec.Code != 200 || rec.Header().Get("Content-Disposition") != `attachment; filename="cv-maestro.pdf"` {
		t.Fatalf("status %d headers %v", rec.Code, rec.Header())
	}
	if !strings.Contains(string(e.pdf.lastHTML), "Experience") {
		t.Fatal("el idioma configurado no llegó a la plantilla")
	}
}

func TestParseCVFormIgnoresMalformedKeys(t *testing.T) {
	c := parseCVForm(url.Values{
		"exp.0.company":    {"Real"},
		"exp.x.company":    {"ignorada"},
		"exp.0.":           {"ignorada"},
		"exp.99999.role":   {"ignorada"}, // índice demasiado largo
		"hack.0.company":   {"ignorada"},
		"exp.1.company":    {"Segunda"},
		"exp.1.unexpected": {"ignorada"},
	})
	if len(c.Experience) != 2 || c.Experience[0].Company != "Real" || c.Experience[1].Company != "Segunda" {
		t.Fatalf("experience = %+v", c.Experience)
	}
}
