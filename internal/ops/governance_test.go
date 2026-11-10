package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"crypto/sha256"
	"encoding/hex"
	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/store"
	"time"
)

func governanceRequest(method, path string, body any) *http.Request {
	var b bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&b).Encode(body)
	}
	return httptest.NewRequest(method, path, &b)
}

func TestCreateUserCommitsUserAndAuditTogether(t *testing.T) {
	db := opsDB(t)
	a := &API{DB: db}
	r := governanceRequest(http.MethodPost, "/users", map[string]any{"username": "new-user", "password": "a-strong-password", "role": "viewer"})
	w := httptest.NewRecorder()
	a.createUser(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM users WHERE username='new-user'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("user count=%d err=%v", n, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='user.create'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit count=%d err=%v", n, err)
	}
}

func TestDeleteAllowAuditsWithinTransaction(t *testing.T) {
	db := opsDB(t)
	if _, err := db.Exec("INSERT INTO allowlist(id,pattern) VALUES('allow-1','example.com')"); err != nil {
		t.Fatal(err)
	}
	a := &API{DB: db}
	r := governanceRequest(http.MethodDelete, "/allowlist/allow-1", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "allow-1")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	a.deleteAllow(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM allowlist WHERE id='allow-1'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("allowlist count=%d err=%v", n, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='allowlist.delete'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit count=%d err=%v", n, err)
	}
}

func TestSecurityUsesEnvironmentAndExpires(t *testing.T) {
	db := opsDB(t)
	prod := &API{DB: db, Env: "production"}
	w := httptest.NewRecorder()
	prod.security(w, governanceRequest(http.MethodPut, "/security/insecure-http", map[string]string{"duration": "1h"}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("production status=%d", w.Code)
	}
	if _, err := db.Exec("UPDATE settings SET value='true' WHERE key='allow_insecure_targets'; UPDATE settings SET value=? WHERE key='allow_insecure_targets_until'", time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	dev := &API{DB: db, Env: "dev"}
	w = httptest.NewRecorder()
	dev.security(w, governanceRequest(http.MethodGet, "/security", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get status=%d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["allow_insecure_targets"] != false {
		t.Fatalf("expired setting active: %#v", got)
	}
}

// F1: the scheduler switch works in production and is audited.
func TestUpdateSchedulerAllowedInProductionAndAudited(t *testing.T) {
	db := opsDB(t)
	h, _ := store.HashPassword("a-strong-password")
	uid := store.ID()
	if _, err := db.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES(?,?,?,?,0)", uid, "sched-root", h, "superadmin"); err != nil {
		t.Fatal(err)
	}
	raw := []byte("0123456789abcdef0123456789abcdef")
	sum := sha256.Sum256(raw)
	if _, err := db.Exec("INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", store.ID(), uid, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	RegisterRoutes(r, db, &auth.Service{DB: db, TTL: time.Hour}, "production")
	req := governanceRequest(http.MethodPatch, "/settings/scheduler", map[string]any{"enabled": true})
	req.AddCookie(&http.Cookie{Name: "cs_session", Value: hex.EncodeToString(raw)})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("production status=%d body=%s", w.Code, w.Body)
	}
	var value string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='scheduler.enabled'").Scan(&value); err != nil || value != "true" {
		t.Fatalf("scheduler.enabled=%q err=%v", value, err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM audit_log WHERE action='settings.scheduler.update'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows=%d err=%v", n, err)
	}
}

func TestUpdateSchedulerRejectsInvalidBody(t *testing.T) {
	db := opsDB(t)
	dev := &API{DB: db, Env: "dev"}
	w := httptest.NewRecorder()
	dev.updateScheduler(w, governanceRequest(http.MethodPatch, "/settings/scheduler", map[string]any{"enabled": "yes"}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid body status=%d body=%s", w.Code, w.Body)
	}
	var value string
	if err := db.QueryRow("SELECT value FROM settings WHERE key='scheduler.enabled'").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "false" {
		t.Fatalf("scheduler changed without authorized actor: %q", value)
	}
}

func TestNormalizePatternAcceptsURLAndHost(t *testing.T) {
	const host = "57ae-2001-448a-b0b0-c0d8-e4da-e4a2-8fe-9704.ngrok-free.app"
	for in, want := range map[string]string{
		host:                              host,
		"https://" + host + "/":           host,
		"https://" + host + "/api/v1?x=1": host,
		"HTTP://" + host:                  host,
		host + "/health":                  host,
		" api.example.com:8443 ":          "api.example.com:8443",
		"*.example.com":                   "*.example.com",
		"10.0.0.5":                        "10.0.0.5",
		"::1":                             "[::1]",
		"[::1]:8443":                      "[::1]:8443",
	} {
		got, ok := normalizePattern(in)
		if !ok || got != want {
			t.Errorf("normalize(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "https://", "user:pw@host", "host:99999", "no spaces allowed", "/just/a/path"} {
		if got, ok := normalizePattern(bad); ok {
			t.Errorf("normalize(%q) accepted as %q", bad, got)
		}
	}
}

func TestAllowlistStoresNormalizedHostAndRejectsDuplicate(t *testing.T) {
	db := opsDB(t)
	a := &API{DB: db}
	w := httptest.NewRecorder()
	req := governanceRequest(http.MethodPost, "/allowlist", map[string]any{"pattern": "https://57ae-2001-448a-b0b0-c0d8-e4da-e4a2-8fe-9704.ngrok-free.app/api/v1", "note": "ngrok"})
	a.allowlist(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body)
	}
	var stored string
	if err := db.QueryRow(`SELECT pattern FROM allowlist`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "57ae-2001-448a-b0b0-c0d8-e4da-e4a2-8fe-9704.ngrok-free.app" {
		t.Fatalf("stored %q", stored)
	}
	w = httptest.NewRecorder()
	a.allowlist(w, governanceRequest(http.MethodPost, "/allowlist", map[string]any{"pattern": "57ae-2001-448a-b0b0-c0d8-e4da-e4a2-8fe-9704.ngrok-free.app"}))
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", w.Code, w.Body)
	}
}

func TestDisplayTimezonePersistsAndRejectsUnknownZones(t *testing.T) {
	db := opsDB(t)
	a := &API{DB: db}
	// The handler is authorised by role, so exercise it through the real
	// middleware with a seeded session rather than calling it directly.
	hash, err := store.HashPassword("strong-password")
	if err != nil {
		t.Fatal(err)
	}
	uid := store.ID()
	if _, err = db.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES(?,?,?,?,0)", uid, "root", hash, "superadmin"); err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	sum := sha256.Sum256(raw)
	if _, err = db.Exec("INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", store.ID(), uid, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	authSvc := &auth.Service{DB: db, TTL: time.Hour}
	h := auth.RequireRole(authSvc, "superadmin")(http.HandlerFunc(a.updateDisplay))
	call := func(tz string) *httptest.ResponseRecorder {
		req := governanceRequest(http.MethodPatch, "/settings/display", map[string]any{"timezone": tz})
		req.AddCookie(&http.Cookie{Name: "cs_session", Value: hex.EncodeToString(raw)})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	var stored string
	if rec := call("Asia/Jakarta"); rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='display.timezone'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "Asia/Jakarta" {
		t.Fatalf("stored %q", stored)
	}
	if rec := call("Mars/Olympus"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bogus zone status=%d", rec.Code)
	}
	_ = db.QueryRow(`SELECT value FROM settings WHERE key='display.timezone'`).Scan(&stored)
	if stored != "Asia/Jakarta" {
		t.Fatalf("stored value changed to %q after a rejected update", stored)
	}
	if rec := call("UTC"); rec.Code != http.StatusOK {
		t.Fatalf("second update status=%d", rec.Code)
	}
	_ = db.QueryRow(`SELECT value FROM settings WHERE key='display.timezone'`).Scan(&stored)
	if stored != "UTC" {
		t.Fatalf("stored %q after second update", stored)
	}
}
