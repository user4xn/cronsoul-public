package jobs

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/store"
)

func jobSetup(t *testing.T, role string) (*store.Store, http.Handler, string) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err = st.Migrate(); err != nil {
		t.Fatal(err)
	}
	h, err := store.HashPassword("strong-password")
	if err != nil {
		t.Fatal(err)
	}
	uid, token := store.ID(), hex.EncodeToString([]byte("01234567890123456789012345678901"+role))
	if _, err = st.DB.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES(?,?,?,?,0)", uid, role+"-user", h, role); err != nil {
		t.Fatal(err)
	}
	raw, _ := hex.DecodeString(token)
	sum := sha256.Sum256(raw)
	if _, err = st.DB.Exec("INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", store.ID(), uid, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	a := &auth.Service{DB: st.DB, TTL: time.Hour}
	return st, New(st.DB, a, []byte("01234567890123456789012345678901"), nil), token
}

func request(t *testing.T, h http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if token != "" {
		r.AddCookie(&http.Cookie{Name: "cs_session", Value: token})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestRegistryRBACAndCRUD(t *testing.T) {
	st, h, token := jobSetup(t, "admin")
	payload := `{"key":"sync-orders","name":"Sync orders","service":"orders","method":"GET","url":"https://example.test/orders","cron":"0 * * * *","timezone":"UTC","timeout_s":25,"body":{"ok":true}}`
	rec := request(t, h, token, http.MethodPost, "/", payload)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body)
	}
	var got Job
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RiskTier != "A" {
		t.Fatalf("risk=%s", got.RiskTier)
	}
	if rec = request(t, h, token, http.MethodGet, "/", ""); rec.Code != 200 {
		t.Fatalf("list=%d", rec.Code)
	}
	update := `{"key":"sync-orders","name":"Updated","service":"orders","method":"POST","url":"https://example.test/orders","cron":"*/5 * * * *","timezone":"UTC","timeout_s":30,"risk_tier":"B"}`
	if rec = request(t, h, token, http.MethodPatch, "/"+got.ID, update); rec.Code != 200 {
		t.Fatalf("update=%d %s", rec.Code, rec.Body)
	}
	if rec = request(t, h, token, http.MethodGet, "/"+got.ID, ""); rec.Code != 200 {
		t.Fatalf("get=%d", rec.Code)
	}
	if rec = request(t, h, token, http.MethodDelete, "/"+got.ID, ""); rec.Code != 204 {
		t.Fatalf("disable=%d", rec.Code)
	}
	if rec = request(t, h, token, http.MethodGet, "/"+got.ID, ""); rec.Code != 404 {
		t.Fatalf("disabled get=%d", rec.Code)
	}
	var audits int
	if err := st.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action LIKE 'job.%' AND actor_name=? AND actor_id IS NOT NULL", "admin-user").Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
}

func TestRegistryAuthAndRoles(t *testing.T) {
	for _, role := range []string{"viewer", "qa"} {
		_, h, token := jobSetup(t, role)
		if rec := request(t, h, "", http.MethodGet, "/", ""); rec.Code != 401 {
			t.Errorf("%s unauth=%d", role, rec.Code)
		}
		if rec := request(t, h, token, http.MethodGet, "/", ""); rec.Code != 200 {
			t.Errorf("%s read=%d", role, rec.Code)
		}
		if rec := request(t, h, token, http.MethodPost, "/", `{}`); rec.Code != 403 {
			t.Errorf("%s create=%d", role, rec.Code)
		}
	}
}

func TestInvalidJobInputs(t *testing.T) {
	_, h, token := jobSetup(t, "admin")
	cases := []string{
		`{"key":"bad","name":"x","service":"s","method":"TRACE","url":"https://x.test","cron":"0 * * * *"}`,
		`{"key":"bad","name":"x","service":"s","method":"GET","url":"javascript:alert(1)","cron":"0 * * * *"}`,
		`{"key":"bad","name":"x","service":"s","method":"GET","url":"https://x.test","cron":"61 * * * *"}`,
		`{"key":"bad","name":"x","service":"s","method":"GET","url":"https://x.test","cron":"0 * * * *","timezone":"Bogus"}`,
		`{"key":"bad","name":"x","service":"s","method":"GET","url":"https://x.test","cron":"0 * * * *","timeout_s":601}`,
		`{"key":"bad","name":"x","service":"s","method":"GET","url":"https://x.test","cron":"0 * * * *","risk_tier":"D"}`,
		`{"key":"bad","name":"x","service":"s","method":"GET","url":"https://x.test","cron":"0 * * * *","body":` + `{"oops"}` + `}`,
	}
	for _, body := range cases {
		if rec := request(t, h, token, http.MethodPost, "/", body); rec.Code != 400 {
			t.Errorf("status=%d body=%s", rec.Code, rec.Body)
		}
	}
	if rec := request(t, h, token, http.MethodGet, "/unknown", ""); rec.Code != 404 {
		t.Errorf("unknown=%d", rec.Code)
	}
	if rec := request(t, h, token, http.MethodPut, "/", ""); rec.Code != 405 {
		t.Errorf("method=%d", rec.Code)
	}
}

var _ *sql.DB

// Auth config is write-only: encrypted at rest, never echoed back.
func TestJobAuthConfigEncryptedAndRedacted(t *testing.T) {
	st, h, token := jobSetup(t, "admin")
	payload := `{"key":"secured","name":"Secured","service":"svc","method":"POST","url":"https://example.test/x","cron":"0 * * * *","auth":{"type":"bearer","config":{"token":"super-secret-token"}},"retry":{"max":2,"backoff":"fixed","on":["http_5xx"]},"notify":{"on":["failure"],"emails":["ops@example.test"]}}`
	rec := request(t, h, token, http.MethodPost, "/", payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "super-secret-token") {
		t.Fatal("auth secret leaked in create response")
	}
	var cipherText string
	if err := st.DB.QueryRow(`SELECT auth_cipher FROM jobs WHERE key='secured'`).Scan(&cipherText); err != nil {
		t.Fatal(err)
	}
	if cipherText == "" || strings.Contains(cipherText, "super-secret-token") {
		t.Fatalf("auth_cipher not encrypted: %q", cipherText)
	}
	plain, err := decryptAuth([]byte("01234567890123456789012345678901"), cipherText)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), "super-secret-token") {
		t.Fatalf("encrypted auth did not round-trip: %s", plain)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	list := request(t, h, token, http.MethodGet, "/", "")
	if strings.Contains(list.Body.String(), "super-secret-token") {
		t.Fatal("auth secret leaked in list response")
	}
	var got Job
	if err := json.Unmarshal(list.Body.Bytes(), &struct {
		Data *[]Job `json:"data"`
	}{}); err == nil {
	}
	detail := request(t, h, token, http.MethodGet, "/"+created.ID, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d", detail.Code)
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.AuthType != "bearer" || got.Retry.Max != 2 || got.Retry.Backoff != "fixed" || len(got.Notify.Emails) != 1 {
		t.Fatalf("runner config not returned: %+v", got)
	}
	if strings.Contains(detail.Body.String(), "super-secret-token") {
		t.Fatal("auth secret leaked in detail response")
	}
}

func TestJobRejectsInvalidRunnerConfig(t *testing.T) {
	_, h, token := jobSetup(t, "admin")
	base := `{"key":"bad-cfg","name":"Bad","service":"svc","method":"GET","url":"https://example.test","cron":"0 * * * *"`
	for _, tc := range []struct{ name, extra string }{
		{"auth type", `,"auth":{"type":"magic"}`},
		{"retry max", `,"retry":{"max":9}`},
		{"retry backoff", `,"retry":{"max":1,"backoff":"linear"}}`},
		{"retry event", `,"retry":{"max":1,"on":["nope"]}}`},
		{"notify event", `,"notify":{"on":["nope"]}}`},
		{"dry run param", `,"dry_run_param":"bad param"}`},
		{"header name", `,"simulated_time_header":"bad header"}`},
	} {
		body := base + tc.extra + "}"
		if rec := request(t, h, token, http.MethodPost, "/", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s", tc.name, rec.Code, rec.Body)
		}
	}
}

func TestJobListFiltersAndPaginates(t *testing.T) {
	_, h, token := jobSetup(t, "admin")
	for _, key := range []string{"alpha-one", "alpha-two", "beta-one"} {
		body := `{"key":"` + key + `","name":"` + key + `","service":"` + strings.Split(key, "-")[0] + `","method":"GET","url":"https://example.test/` + key + `","cron":"0 * * * *"}`
		if rec := request(t, h, token, http.MethodPost, "/", body); rec.Code != http.StatusCreated {
			t.Fatalf("seed %s: %d %s", key, rec.Code, rec.Body)
		}
	}
	var page struct {
		Data       []Job   `json:"data"`
		NextCursor *string `json:"next_cursor"`
	}
	rec := request(t, h, token, http.MethodGet, "/?limit=2", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 2 || page.NextCursor == nil {
		t.Fatalf("limit=2 returned %d rows cursor=%v", len(page.Data), page.NextCursor)
	}
	rec = request(t, h, token, http.MethodGet, "/?service=beta", "")
	page.Data = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 1 || page.Data[0].Key != "beta-one" {
		t.Fatalf("service filter returned %+v", page.Data)
	}
	rec = request(t, h, token, http.MethodGet, "/?q=alpha", "")
	page.Data = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data) != 2 {
		t.Fatalf("q filter returned %d rows", len(page.Data))
	}
}

func TestUpcomingReturnsSortedSlotsInsideTheWindow(t *testing.T) {
	st, h, token := jobSetup(t, "admin")
	body := `{"key":"every-five","name":"Every five","service":"svc","method":"GET","url":"https://example.test/health","cron":"*/5 * * * *","timezone":"Asia/Jakarta"}`
	if rec := request(t, h, token, http.MethodPost, "/", body); rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body)
	}
	rec := request(t, h, token, http.MethodGet, "/upcoming?minutes=60", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("upcoming status=%d body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Data []struct {
			JobKey   string `json:"job_key"`
			At       string `json:"at"`
			InSecond int    `json:"in_seconds"`
		} `json:"data"`
		Minutes          int  `json:"minutes"`
		SchedulerEnabled bool `json:"scheduler_enabled"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Minutes != 60 {
		t.Fatalf("minutes=%d", out.Minutes)
	}
	// A five-minute schedule produces between 11 and 13 slots in a 60 minute window.
	if len(out.Data) < 11 || len(out.Data) > 13 {
		t.Fatalf("slots=%d, want 11..13", len(out.Data))
	}
	for i := 1; i < len(out.Data); i++ {
		if out.Data[i-1].At > out.Data[i].At {
			t.Fatalf("slots are not sorted: %s before %s", out.Data[i-1].At, out.Data[i].At)
		}
	}
	if out.Data[0].InSecond < 0 || out.Data[0].InSecond > 300 {
		t.Fatalf("first slot in %ds, want within 5 minutes", out.Data[0].InSecond)
	}
	// Paused jobs must not appear.
	if _, err := st.DB.Exec("UPDATE jobs SET enabled=0"); err != nil {
		t.Fatal(err)
	}
	rec = request(t, h, token, http.MethodGet, "/upcoming?minutes=60", "")
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Fatalf("paused job leaked into upcoming: %s", rec.Body)
	}
}
