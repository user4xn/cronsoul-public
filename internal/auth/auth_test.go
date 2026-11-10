package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/user4xn/cronsoul/internal/store"
)

func setup(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err = st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err = st.Seed(); err != nil {
		t.Fatal(err)
	}
	return &Service{DB: st.DB, TTL: time.Hour, Secure: true}, st.DB
}
func req(t *testing.T, client *http.Client, method, url string, body any) *http.Response {
	t.Helper()
	var b bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&b).Encode(body)
	}
	r, _ := http.NewRequest(method, url, &b)
	r.RemoteAddr = "192.0.2.10:1234"
	r.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
func TestLoginCookieTokenAndMe(t *testing.T) {
	s, db := setup(t)
	ts := httptest.NewServer(s.Router())
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp := req(t, c, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "12345678"})
	if resp.StatusCode != 200 {
		t.Fatalf("login status=%d", resp.StatusCode)
	}
	cookie := resp.Cookies()
	if len(cookie) != 1 || cookie[0].Name != "cs_session" || !cookie[0].HttpOnly || !cookie[0].Secure || cookie[0].SameSite != http.SameSiteLaxMode || cookie[0].MaxAge <= 0 {
		t.Fatalf("bad cookie: %#v", cookie)
	}
	var sessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("sessions=%d err=%v", sessions, err)
	}
	resp = req(t, c, "GET", ts.URL+"/me", nil)
	if resp.StatusCode != 428 {
		t.Fatalf("me before change=%d", resp.StatusCode)
	}
}
func TestForcedPasswordChangeKeepsCurrentAndRevokesOthers(t *testing.T) {
	s, _ := setup(t)
	ts := httptest.NewServer(s.Router())
	defer ts.Close()
	jar1, _ := cookiejar.New(nil)
	c1 := &http.Client{Jar: jar1}
	first := req(t, c1, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "12345678"})
	current := first.Cookies()[0]
	jar2, _ := cookiejar.New(nil)
	c2 := &http.Client{Jar: jar2}
	second := req(t, c2, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "12345678"})
	other := second.Cookies()[0]
	resp := req(t, c1, "POST", ts.URL+"/change-password", map[string]string{"current_password": "12345678", "new_password": "a-strong-password"})
	if resp.StatusCode != 204 {
		t.Fatalf("change=%d", resp.StatusCode)
	}
	if current.Value == other.Value {
		t.Fatal("login tokens were reused")
	}
	if resp = req(t, c1, "GET", ts.URL+"/me", nil); resp.StatusCode != 200 {
		t.Fatalf("current session=%d", resp.StatusCode)
	}
	if resp = req(t, c2, "GET", ts.URL+"/me", nil); resp.StatusCode != 401 {
		t.Fatalf("other session=%d", resp.StatusCode)
	}
}
func TestLogoutRevokesSession(t *testing.T) {
	s, _ := setup(t)
	ts := httptest.NewServer(s.Router())
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	_ = req(t, c, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "12345678"})
	_ = req(t, c, "POST", ts.URL+"/logout", nil)
	if r := req(t, c, "GET", ts.URL+"/me", nil); r.StatusCode != 401 {
		t.Fatalf("after logout=%d", r.StatusCode)
	}
}
func TestLoginRateLimitPerIPAndUsername(t *testing.T) {
	s, _ := setup(t)
	ts := httptest.NewServer(s.Router())
	defer ts.Close()
	c := &http.Client{}
	for i := 0; i < 5; i++ {
		r := req(t, c, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "wrong"})
		if r.StatusCode != 401 {
			t.Fatalf("attempt %d=%d", i+1, r.StatusCode)
		}
	}
	if r := req(t, c, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "wrong"}); r.StatusCode != 429 {
		t.Fatalf("sixth=%d", r.StatusCode)
	}
	if r := req(t, c, "POST", ts.URL+"/login", map[string]string{"username": "other", "password": "wrong"}); r.StatusCode != 401 {
		t.Fatalf("different username=%d", r.StatusCode)
	}
}
func TestRequireRoleRBAC(t *testing.T) {
	for _, tc := range []struct {
		role, need string
		ok         bool
	}{{"viewer", "viewer", true}, {"viewer", "qa", false}, {"qa", "admin", false}, {"admin", "qa", true}, {"superadmin", "admin", true}, {"bogus", "viewer", false}} {
		if got := RoleAtLeast(tc.role, tc.need); got != tc.ok {
			t.Errorf("%s >= %s: got %v", tc.role, tc.need, got)
		}
	}
	s, _ := setup(t)
	ts := httptest.NewServer(s.Router())
	defer ts.Close()
	_ = http.MethodGet
}

// The SPA posts snake_case. Without json tags on the decode struct those fields
// were silently left empty, so the first-login password change always failed
// with 400 and the account stayed locked behind 428.
func TestChangePasswordAcceptsSnakeCaseBody(t *testing.T) {
	s, _ := setup(t)
	ts := httptest.NewServer(s.Router())
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if resp := req(t, c, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "12345678"}); resp.StatusCode != 200 {
		t.Fatalf("login status=%d", resp.StatusCode)
	}
	resp := req(t, c, "POST", ts.URL+"/change-password", map[string]string{
		"current_password": "12345678",
		"new_password":     "release-hardening-1",
	})
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("change-password status=%d body=%v", resp.StatusCode, resp.Body)
	}
	// the account must now be usable, and the old password must stop working
	if resp = req(t, c, "GET", ts.URL+"/me", nil); resp.StatusCode != 200 {
		t.Fatalf("me status=%d", resp.StatusCode)
	}
	if resp = req(t, &http.Client{}, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "12345678"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password still accepted: %d", resp.StatusCode)
	}
	if resp = req(t, &http.Client{}, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "release-hardening-1"}); resp.StatusCode != 200 {
		t.Fatalf("new password rejected: %d", resp.StatusCode)
	}
}

func TestForcedChangeSkipsCurrentButVoluntaryRequiresIt(t *testing.T) {
	s, _ := setup(t)
	ts := httptest.NewServer(s.Router())
	defer ts.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	_ = req(t, c, "POST", ts.URL+"/login", map[string]string{"username": "admin", "password": "12345678"})
	if resp := req(t, c, "POST", ts.URL+"/change-password", map[string]string{"new_password": "first-login-pass"}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("forced change without current=%d", resp.StatusCode)
	}
	if resp := req(t, c, "POST", ts.URL+"/change-password", map[string]string{"new_password": "second-change-pass"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("voluntary change without current=%d", resp.StatusCode)
	}
	if resp := req(t, c, "POST", ts.URL+"/change-password", map[string]string{"current_password": "first-login-pass", "new_password": "first-login-pass"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("reusing the same password=%d", resp.StatusCode)
	}
	if resp := req(t, c, "POST", ts.URL+"/change-password", map[string]string{"current_password": "first-login-pass", "new_password": "second-change-pass"}); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("voluntary change with current=%d", resp.StatusCode)
	}
}
