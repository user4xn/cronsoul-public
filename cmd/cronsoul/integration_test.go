package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/runner"
	"github.com/user4xn/cronsoul/internal/store"
)

// integrationEnv is the real router plus the same auth/session plumbing used
// by the binary. It deliberately keeps the production wiring (embedded UI,
// jobs subtree, governance, runner policy) instead of mounting handlers
// directly, so route-shadowing regressions are caught here.
type integrationEnv struct {
	st     *store.Store
	api    *runner.API
	router chi.Router
	tokens map[string]string
}

var integrationTokenSeeds = map[string]string{
	"viewer":     "11111111111111111111111111111111",
	"qa":         "22222222222222222222222222222222",
	"admin":      "33333333333333333333333333333333",
	"superadmin": "44444444444444444444444444444444",
}

func newIntegrationEnv(t *testing.T) *integrationEnv {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err = st.Migrate(); err != nil {
		t.Fatal(err)
	}
	hash, err := store.HashPassword("strong-password")
	if err != nil {
		t.Fatal(err)
	}
	env := &integrationEnv{st: st, tokens: map[string]string{}}
	for role, seed := range integrationTokenSeeds {
		uid := store.ID()
		if _, err = st.DB.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES(?,?,?,?,0)", uid, role+"-user", hash, role); err != nil {
			t.Fatal(err)
		}
		raw, err := hex.DecodeString(seed)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if _, err = st.DB.Exec("INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", store.ID(), uid, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
		env.tokens[role] = seed
	}
	env.api = &runner.API{DB: st.DB, SecretKey: []byte("01234567890123456789012345678901")}
	env.router = newRouter(&auth.Service{DB: st.DB, TTL: time.Hour}, st.DB, env.api)
	return env
}

// do sends a request as role. An empty role sends no cookie.
func (e *integrationEnv) do(t *testing.T, role, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		req.AddCookie(&http.Cookie{Name: "cs_session", Value: e.tokens[role]})
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func TestMainHealthAndEmbeddedAssets(t *testing.T) {
	e := newIntegrationEnv(t)

	health := e.do(t, "", http.MethodGet, "/healthz", "")
	if health.Code != http.StatusOK {
		t.Fatalf("healthz status=%d body=%s", health.Code, health.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal(health.Body.Bytes(), &payload); err != nil {
		t.Fatalf("healthz json: %v (%s)", err, health.Body)
	}
	if payload["status"] != "ok" || payload["db"] != "ok" {
		t.Fatalf("healthz payload: %+v", payload)
	}

	root := e.do(t, "", http.MethodGet, "/", "")
	if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), "/ui/app.js") {
		t.Fatalf("index status=%d body=%s", root.Code, root.Body)
	}
	for path, want := range map[string]string{
		"/ui/app.js":            "function api(",
		"/ui/style.css":         ".",
		"/ui/cronsoul-mark.png": "\x89PNG",
	} {
		rec := e.do(t, "", http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s body missing %q: %.80s", path, want, rec.Body.String())
		}
	}
}

func TestMainGovernanceRoutesEnforceRoles(t *testing.T) {
	e := newIntegrationEnv(t)

	for path, want := range map[string]int{
		"/api/v1/runs":      http.StatusOK,
		"/api/v1/alerts":    http.StatusOK,
		"/api/v1/outbox":    http.StatusOK,
		"/api/v1/settings":  http.StatusOK,
		"/api/v1/users":     http.StatusForbidden,
		"/api/v1/audit":     http.StatusForbidden,
		"/api/v1/allowlist": http.StatusForbidden,
		"/api/v1/security":  http.StatusForbidden,
	} {
		if rec := e.do(t, "viewer", http.MethodGet, path, ""); rec.Code != want {
			t.Fatalf("viewer GET %s -> %d want %d body=%s", path, rec.Code, want, rec.Body)
		}
	}
	if rec := e.do(t, "viewer", http.MethodPost, "/api/v1/jobs", `{}`); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer POST jobs -> %d", rec.Code)
	}
	if rec := e.do(t, "qa", http.MethodPost, "/api/v1/jobs", `{}`); rec.Code != http.StatusForbidden {
		t.Fatalf("qa POST jobs -> %d", rec.Code)
	}
	if rec := e.do(t, "admin", http.MethodGet, "/api/v1/allowlist", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("admin allowlist -> %d", rec.Code)
	}
	if rec := e.do(t, "admin", http.MethodGet, "/api/v1/users", ""); rec.Code != http.StatusOK {
		t.Fatalf("admin users -> %d body=%s", rec.Code, rec.Body)
	} else {
		var list struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Data) != len(integrationTokenSeeds) {
			t.Fatalf("admin users payload: %v %s", err, rec.Body)
		}
	}
	if rec := e.do(t, "", http.MethodGet, "/api/v1/runs", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous runs -> %d", rec.Code)
	}
}

func TestMainAllowlistChangeReachesLiveRunnerPolicy(t *testing.T) {
	e := newIntegrationEnv(t)

	before := e.api.Policy()
	if err := before.ValidateURL("https://fresh.example.test/", time.Now()); err == nil {
		t.Fatal("empty allowlist must reject the target before the change")
	}
	rec := e.do(t, "superadmin", http.MethodPost, "/api/v1/allowlist", `{"pattern":"fresh.example.test","note":"integration"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("allowlist create -> %d body=%s", rec.Code, rec.Body)
	}
	policy := e.api.Policy()
	if len(policy.Allowlist) != 1 || policy.Allowlist[0] != "fresh.example.test" {
		t.Fatalf("live policy did not reload: %+v", policy.Allowlist)
	}
	if err := policy.ValidateURL("https://fresh.example.test/health", time.Now()); err != nil {
		t.Fatalf("newly allowlisted host rejected: %v", err)
	}
	if err := policy.ValidateURL("https://other.example.test/", time.Now()); err == nil {
		t.Fatal("non-allowlisted host must stay rejected")
	}
	// A run started after the change must use the same live configuration.
	job := e.do(t, "admin", http.MethodPost, "/api/v1/jobs", `{"key":"policy-job","name":"Policy","service":"svc","method":"GET","url":"https://fresh.example.test/health","cron":"0 2 * * *"}`)
	if job.Code != http.StatusCreated {
		t.Fatalf("job create -> %d body=%s", job.Code, job.Body)
	}
}

func TestMainJobAPISmoke(t *testing.T) {
	e := newIntegrationEnv(t)
	const body = `{"key":"smoke-job","name":"Smoke","service":"svc","method":"GET","url":"https://example.test/health","cron":"0 1 * * *","timeout_s":15,"assertions":{"enabled":true,"rules":[{"kind":"status_code","value":200}]}}`

	created := e.do(t, "admin", http.MethodPost, "/api/v1/jobs", body)
	if created.Code != http.StatusCreated {
		t.Fatalf("create -> %d body=%s", created.Code, created.Body)
	}
	var job struct {
		ID   string `json:"id"`
		Key  string `json:"key"`
		Cron string `json:"cron"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &job); err != nil || job.ID == "" {
		t.Fatalf("create payload: %v %s", err, created.Body)
	}
	if job.Key != "smoke-job" || job.Cron != "0 1 * * *" {
		t.Fatalf("create echo: %+v", job)
	}
	if dup := e.do(t, "admin", http.MethodPost, "/api/v1/jobs", body); dup.Code != http.StatusConflict {
		t.Fatalf("duplicate key -> %d body=%s", dup.Code, dup.Body)
	}

	listed := e.do(t, "viewer", http.MethodGet, "/api/v1/jobs/", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list -> %d body=%s", listed.Code, listed.Body)
	}
	var page struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil || len(page.Data) != 1 {
		t.Fatalf("list payload: %v %s", err, listed.Body)
	}
	if rec := e.do(t, "viewer", http.MethodGet, "/api/v1/jobs/"+job.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("detail -> %d body=%s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "admin", http.MethodPatch, "/api/v1/jobs/"+job.ID, strings.Replace(body, `"Smoke"`, `"Smoke v2"`, 1)); rec.Code != http.StatusOK {
		t.Fatalf("update -> %d body=%s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "admin", http.MethodDelete, "/api/v1/jobs/"+job.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete -> %d body=%s", rec.Code, rec.Body)
	}
	if rec := e.do(t, "viewer", http.MethodGet, "/api/v1/jobs/"+job.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("detail after delete -> %d", rec.Code)
	}
}

func TestMainPlayRejectsConcurrentRunOfSameJob(t *testing.T) {
	e := newIntegrationEnv(t)
	created := e.do(t, "admin", http.MethodPost, "/api/v1/jobs", `{"key":"single-job","name":"Single","service":"svc","method":"GET","url":"https://example.test/x","cron":"*/5 * * * *"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create -> %d body=%s", created.Code, created.Body)
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB.Exec(`INSERT INTO runs(id,job_id,trigger,status,triggered_by) VALUES('active','` + job.ID + `','manual','queued',(SELECT id FROM users WHERE role='qa'))`); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "qa", http.MethodPost, "/api/v1/jobs/"+job.ID+"/run", ""); rec.Code != http.StatusConflict {
		t.Fatalf("second play -> %d body=%s", rec.Code, rec.Body)
	}
}

// The endpoint form now posts the runner configuration the schema already
// supported. This pins the full payload and proves the fields persist.
func TestMainJobCreateAcceptsUIPayload(t *testing.T) {
	e := newIntegrationEnv(t)
	payload := `{"name":"UI endpoint","key":"ui-endpoint","service":"svc","method":"GET","url":"https://example.test/x","cron":"*/5 * * * *","timezone":"Asia/Jakarta","timeout_s":30,"risk_tier":"A","enabled":true,"clock_aware":true,"simulated_time_header":"X-Test-Clock","dry_run_param":"dry_run","idempotent":true,"singleton":true,"retry":{"max":2,"backoff":"exponential","on":["http_5xx","timeout"]},"notify":{"on":["failure"],"emails":["ops@example.test"],"telegram":["42"]},"tags":["komship","nightly"]}`
	rec := e.do(t, "admin", http.MethodPost, "/api/v1/jobs", payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("UI payload rejected: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		ID                  string   `json:"id"`
		ClockAware          bool     `json:"clock_aware"`
		SimulatedTimeHeader string   `json:"simulated_time_header"`
		DryRunParam         string   `json:"dry_run_param"`
		Idempotent          bool     `json:"idempotent"`
		Tags                []string `json:"tags"`
		Retry               struct {
			Max int `json:"max"`
		} `json:"retry"`
		Notify struct {
			Emails []string `json:"emails"`
		} `json:"notify"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.ClockAware || got.SimulatedTimeHeader != "X-Test-Clock" || got.DryRunParam != "dry_run" || !got.Idempotent || got.Retry.Max != 2 || len(got.Tags) != 2 || len(got.Notify.Emails) != 1 {
		t.Fatalf("runner config not persisted: %s", rec.Body)
	}
	var clock int
	var header, dry string
	if err := e.st.DB.QueryRow(`SELECT clock_aware,simulated_time_header,COALESCE(dry_run_param,'') FROM jobs WHERE id=?`, got.ID).Scan(&clock, &header, &dry); err != nil {
		t.Fatal(err)
	}
	if clock != 1 || header != "X-Test-Clock" || dry != "dry_run" {
		t.Fatalf("stored clock config = %d/%q/%q", clock, header, dry)
	}
}
