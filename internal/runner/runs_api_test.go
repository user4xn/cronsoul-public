package runner

import (
	"context"
	"crypto/sha256"
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

type runFixture struct {
	db    *store.Store
	api   *API
	route http.Handler
	token string
	jobID string
}

func runSetup(t *testing.T) *runFixture {
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
	uid := store.ID()
	if _, err = st.DB.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES(?,?,?,?,0)", uid, "qa-user", h, "qa"); err != nil {
		t.Fatal(err)
	}
	raw := []byte("01234567890123456789012345678901")
	sum := sha256.Sum256(raw)
	token := hex.EncodeToString(raw)
	if _, err = st.DB.Exec("INSERT INTO sessions(id,user_id,token_hash,expires_at) VALUES(?,?,?,?)", store.ID(), uid, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	jobID := store.ID()
	if _, err = st.DB.Exec(`INSERT INTO jobs(id,key,name,service,method,url,cron,assertions_json,assertions_enabled) VALUES(?,?,?,?,?,?,?,?,1)`, jobID, "job-a", "Job A", "svc", "GET", "https://example.test/a", "0 * * * *", `{"enabled":true,"rules":[{"kind":"status_code","value":200}]}`); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: st.DB, Egress: EgressConfig{Allowlist: []string{"example.test"}}}
	a := &auth.Service{DB: st.DB, TTL: time.Hour}
	return &runFixture{db: st, api: api, route: NewAPIFromAPI(api, a), token: token, jobID: jobID}
}

func (f *runFixture) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: "cs_session", Value: f.token})
	rec := httptest.NewRecorder()
	f.route.ServeHTTP(rec, r)
	return rec
}

func (f *runFixture) addRun(t *testing.T, id, trigger, status, created string, extra map[string]any) {
	t.Helper()
	if _, err := f.db.DB.Exec(`INSERT INTO runs(id,job_id,trigger,status,created_at,attempts) VALUES(?,?,?,?,?,1)`, id, f.jobID, trigger, status, created); err != nil {
		t.Fatal(err)
	}
	for col, val := range extra {
		if _, err := f.db.DB.Exec("UPDATE runs SET "+col+"=? WHERE id=?", val, id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunListFiltersAndCursorPagination(t *testing.T) {
	f := runSetup(t)
	f.addRun(t, "r1", "manual", "success", "2026-10-01T00:00:01Z", map[string]any{"http_status": 200, "duration_ms": 12, "assertion_passed": 1})
	f.addRun(t, "r2", "schedule", "failed", "2026-10-01T00:00:02Z", map[string]any{"http_status": 500})
	f.addRun(t, "r3", "manual", "success", "2026-10-01T00:00:03Z", nil)
	f.addRun(t, "r4", "manual", "success", "2026-10-09T00:00:04Z", nil)

	var page struct {
		Data       []map[string]any `json:"data"`
		NextCursor *string          `json:"next_cursor"`
	}
	decode := func(t *testing.T, path string, want int) {
		t.Helper()
		rec := f.do(t, http.MethodGet, path, "")
		if rec.Code != want {
			t.Fatalf("%s -> %d %s", path, rec.Code, rec.Body)
		}
		page.Data, page.NextCursor = nil, nil
		if want == 200 {
			if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
	}

	// newest first
	decode(t, "/runs", 200)
	if len(page.Data) != 4 || page.Data[0]["id"] != "r4" {
		t.Fatalf("ordering: %+v", page.Data)
	}
	first := page.Data[0]
	for _, key := range []string{"id", "job_id", "trigger", "status", "http_status", "duration_ms", "attempts", "assertion_passed"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("list run missing %q: %+v", key, first)
		}
	}
	if _, leaked := first["response_excerpt"]; leaked {
		t.Fatalf("list must not carry response bodies: %+v", first)
	}

	decode(t, "/runs?status=success", 200)
	if len(page.Data) != 3 {
		t.Fatalf("status filter: %+v", page.Data)
	}
	decode(t, "/runs?trigger=schedule", 200)
	if len(page.Data) != 1 || page.Data[0]["id"] != "r2" {
		t.Fatalf("trigger filter: %+v", page.Data)
	}
	decode(t, "/runs?job_id="+f.jobID, 200)
	if len(page.Data) != 4 {
		t.Fatalf("job filter: %+v", page.Data)
	}
	decode(t, "/runs?from=2026-10-02T00:00:00Z", 200)
	if len(page.Data) != 1 || page.Data[0]["id"] != "r4" {
		t.Fatalf("from filter: %+v", page.Data)
	}
	decode(t, "/runs?from=2026-10-01T00:00:00Z&to=2026-10-01T00:00:02Z", 200)
	if len(page.Data) != 2 || page.Data[0]["id"] != "r2" {
		t.Fatalf("range filter: %+v", page.Data)
	}
	// inclusive upper bound keeps the boundary row
	decode(t, "/runs?to=2026-10-01T00:00:01Z", 200)
	if len(page.Data) != 1 || page.Data[0]["id"] != "r1" {
		t.Fatalf("inclusive to: %+v", page.Data)
	}

	decode(t, "/runs?limit=2", 200)
	if len(page.Data) != 2 || page.NextCursor == nil {
		t.Fatalf("first page: %d %+v", len(page.Data), page.NextCursor)
	}
	cursor := *page.NextCursor
	decode(t, "/runs?limit=2&cursor="+cursor, 200)
	if len(page.Data) != 2 || page.Data[0]["id"] != "r2" || page.NextCursor != nil {
		t.Fatalf("second page: %+v next=%v", page.Data, page.NextCursor)
	}
	decode(t, "/runs?limit=1&cursor="+cursor, 200)
	if len(page.Data) != 1 || page.Data[0]["id"] != "r2" {
		t.Fatalf("page after cursor: %+v", page.Data)
	}

	decode(t, "/runs?status=bogus", 400)
	decode(t, "/runs?trigger=bogus", 400)
	decode(t, "/runs?from=yesterday", 400)
	decode(t, "/runs?limit=0", 400)
	decode(t, "/runs?limit=201", 400)
	decode(t, "/runs?cursor=!!!", 400)
}

func TestRunListEmptyIsArray(t *testing.T) {
	f := runSetup(t)
	rec := f.do(t, http.MethodGet, "/runs", "")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var page struct {
		Data       []map[string]any `json:"data"`
		NextCursor *string          `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Data == nil || len(page.Data) != 0 {
		t.Fatalf("data must be [] not null: %s", rec.Body)
	}
	if page.NextCursor != nil {
		t.Fatalf("next_cursor=%v", page.NextCursor)
	}
}

func TestRunDetailSerializesAttemptsAssertionsAndExcerpts(t *testing.T) {
	f := runSetup(t)
	f.addRun(t, "d1", "manual", "success", "2026-10-01T00:00:01Z", map[string]any{
		"started_at": "2026-10-01T00:00:01Z", "finished_at": "2026-10-01T00:00:03Z",
		"http_status": 200, "duration_ms": 250, "attempts": 2, "assertion_passed": 0,
		"response_truncated": 1, "request_excerpt": "POST https://example.test/a", "response_excerpt": `{"ok":false}`,
		"error": "assertion failed",
	})
	if _, err := f.db.DB.Exec(`INSERT INTO run_attempts(id,run_id,attempt_no,started_at,finished_at,status,http_status,duration_ms,error,response_excerpt) VALUES(?,?,?,?,?,?,?,?,?,?)`, "a1", "d1", 1, "2026-10-01T00:00:01Z", "2026-10-01T00:00:02Z", "failed", 500, 90, "boom", "err body"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.DB.Exec(`INSERT INTO run_attempts(id,run_id,attempt_no,started_at,finished_at,status,http_status,duration_ms) VALUES(?,?,?,?,?,?,?,?)`, "a2", "d1", 2, "2026-10-01T00:00:02Z", "2026-10-01T00:00:03Z", "success", 200, 160); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.DB.Exec(`INSERT INTO assertion_results(id,run_id,kind,target,expected,actual,passed) VALUES(?,?,?,?,?,?,?)`, "x1", "d1", "status_code", "", "200", "200", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.DB.Exec(`INSERT INTO assertion_results(id,run_id,kind,target,expected,actual,passed) VALUES(?,?,?,?,?,?,?)`, "x2", "d1", "json_path_equals", "data.ok", "true", "false", 0); err != nil {
		t.Fatal(err)
	}

	rec := f.do(t, http.MethodGet, "/runs/d1", "")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		ID                string  `json:"id"`
		JobID             string  `json:"job_id"`
		Trigger           string  `json:"trigger"`
		Status            string  `json:"status"`
		HTTPStatus        *int    `json:"http_status"`
		DurationMS        *int    `json:"duration_ms"`
		Attempts          int     `json:"attempts"`
		AssertionPassed   *bool   `json:"assertion_passed"`
		ResponseTruncated bool    `json:"response_truncated"`
		RequestExcerpt    *string `json:"request_excerpt"`
		ResponseExcerpt   *string `json:"response_excerpt"`
		StartedAt         *string `json:"started_at"`
		FinishedAt        *string `json:"finished_at"`
		Error             *string `json:"error"`
		AttemptsDetail    []struct {
			AttemptNo  int     `json:"attempt_no"`
			Status     string  `json:"status"`
			HTTPStatus *int    `json:"http_status"`
			DurationMS *int    `json:"duration_ms"`
			Error      *string `json:"error"`
		} `json:"attempts_detail"`
		AssertionResults []struct {
			Kind     string `json:"kind"`
			Target   string `json:"target"`
			Expected string `json:"expected"`
			Actual   string `json:"actual"`
			Passed   bool   `json:"passed"`
		} `json:"assertion_results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if got.ID != "d1" || got.JobID != f.jobID || got.Status != "success" || got.Attempts != 2 {
		t.Fatalf("run core: %+v", got)
	}
	if got.HTTPStatus == nil || *got.HTTPStatus != 200 || got.DurationMS == nil || *got.DurationMS != 250 {
		t.Fatalf("http/duration: %+v", got)
	}
	if got.AssertionPassed == nil || *got.AssertionPassed {
		t.Fatalf("assertion_passed must be false and distinct from HTTP success: %v", got.AssertionPassed)
	}
	if !got.ResponseTruncated {
		t.Fatal("response_truncated lost")
	}
	if got.RequestExcerpt == nil || *got.RequestExcerpt != "POST https://example.test/a" || got.ResponseExcerpt == nil || *got.ResponseExcerpt != `{"ok":false}` {
		t.Fatalf("excerpts: %+v", got)
	}
	if len(got.AttemptsDetail) != 2 || got.AttemptsDetail[0].AttemptNo != 1 || got.AttemptsDetail[1].Status != "success" {
		t.Fatalf("attempts_detail: %+v", got.AttemptsDetail)
	}
	if got.AttemptsDetail[0].Error == nil || *got.AttemptsDetail[0].Error != "boom" {
		t.Fatalf("attempt error: %+v", got.AttemptsDetail[0])
	}
	if len(got.AssertionResults) != 2 || got.AssertionResults[0].Passed != true || got.AssertionResults[1].Passed != false {
		t.Fatalf("assertion_results: %+v", got.AssertionResults)
	}
	if got.AssertionResults[1].Target != "data.ok" || got.AssertionResults[1].Actual != "false" {
		t.Fatalf("assertion detail: %+v", got.AssertionResults[1])
	}

	if rec = f.do(t, http.MethodGet, "/runs/missing", ""); rec.Code != 404 {
		t.Fatalf("missing run: %d", rec.Code)
	}
}

func TestRunDetailWithoutAssertionsOmitsNullStatus(t *testing.T) {
	f := runSetup(t)
	f.addRun(t, "d2", "manual", "failed", "2026-10-01T00:00:01Z", nil)
	rec := f.do(t, http.MethodGet, "/runs/d2", "")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["assertion_passed"]; !ok || v != nil {
		t.Fatalf("assertion_passed must be present and null: %+v", got)
	}
	if _, ok := got["attempts_detail"]; !ok {
		t.Fatalf("attempts_detail must be present: %+v", got)
	}
	if list, ok := got["attempts_detail"].([]any); !ok || len(list) != 0 {
		t.Fatalf("attempts_detail must be []: %+v", got["attempts_detail"])
	}
	if list, ok := got["assertion_results"].([]any); !ok || len(list) != 0 {
		t.Fatalf("assertion_results must be []: %+v", got["assertion_results"])
	}
}

func TestRunSimulationOptionsPersistAndValidate(t *testing.T) {
	f := runSetup(t)
	_, err := f.db.DB.Exec(`UPDATE jobs SET clock_aware=1 WHERE id=?`, f.jobID)
	if err != nil {
		t.Fatal(err)
	}
	rec := f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/run", `{"simulated_time":"2026-11-30T23:59:00+07:00"}`)
	if rec.Code != 202 {
		t.Fatalf("simulation status=%d body=%s", rec.Code, rec.Body)
	}
	var n int
	if err := f.db.DB.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("created runs=%d err=%v", n, err)
	}
}

// Preview must exercise the guarded request path without persisting a run.
func TestPreviewDoesNotCreateRun(t *testing.T) {
	f := runSetup(t)
	called := false
	f.api.Do = func(_ context.Context, req Request) (Result, error) {
		called = true
		if req.URL != "https://example.test/a" {
			t.Fatalf("preview hit %q", req.URL)
		}
		return Result{Status: 200, Body: []byte(`{"alive":true}`), Duration: 15 * time.Millisecond}, nil
	}
	rec := f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/preview", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", rec.Code, rec.Body)
	}
	if !called {
		t.Fatal("preview did not perform the request")
	}
	var res struct {
		OK          bool   `json:"ok"`
		Status      int    `json:"status"`
		BodyExcerpt string `json:"body_excerpt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Status != 200 || !strings.Contains(res.BodyExcerpt, "alive") {
		t.Fatalf("preview result = %+v", res)
	}
	var runs int
	if err := f.db.DB.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("preview created %d run rows", runs)
	}
}

func TestFailedRunNotifiesSubscribedChannelsEvenWithoutRetries(t *testing.T) {
	f := runSetup(t)
	if _, err := f.db.DB.Exec(`INSERT INTO channels(id,kind,name,config_cipher,events_json) VALUES('ch-1','telegram','ops','cipher','["failure"]')`); err != nil {
		t.Fatal(err)
	}
	// A 400 is a terminal error: retry_max is 0 by default, so the run ends as
	// "failed" rather than "failed_after_retries".
	stubTransport(f.api, Result{Status: 400, Body: []byte(`{"error":"bad request"}`)}, nil)
	runID := f.play(t, `{}`)
	f.settle(t)
	if status, _ := f.runState(t, runID); status != "failed" {
		t.Fatalf("run status=%s, want failed", status)
	}
	var rows int
	var msg string
	if err := f.db.DB.QueryRow(`SELECT COUNT(*) FROM outbox WHERE kind='failure' AND run_id=?`, runID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("outbox rows=%d, want 1 failure notification", rows)
	}
	if err := f.db.DB.QueryRow(`SELECT payload_json FROM outbox WHERE run_id=?`, runID).Scan(&msg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "HTTP 400") {
		t.Fatalf("payload should carry the HTTP status: %s", msg)
	}
}

func TestFailedRunDoesNotNotifyChannelsThatDoNotSubscribe(t *testing.T) {
	f := runSetup(t)
	if _, err := f.db.DB.Exec(`INSERT INTO channels(id,kind,name,config_cipher,events_json) VALUES('ch-2','telegram','ops','cipher','["missed_run"]')`); err != nil {
		t.Fatal(err)
	}
	stubTransport(f.api, Result{Status: 500, Body: []byte(`boom`)}, nil)
	f.settle(t)
	runID := f.play(t, `{}`)
	f.settle(t)
	var rows int
	if err := f.db.DB.QueryRow(`SELECT COUNT(*) FROM outbox WHERE run_id=?`, runID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("outbox rows=%d, want 0 for an unsubscribed event", rows)
	}
}

func TestFailureAlertCarriesEndpointRequestAndResponseDetail(t *testing.T) {
	f := runSetup(t)
	if _, err := f.db.DB.Exec(`INSERT INTO channels(id,kind,name,config_cipher,events_json) VALUES('ch-3','telegram','ops','cipher','["failure"]')`); err != nil {
		t.Fatal(err)
	}
	stubTransport(f.api, Result{Status: 400, Body: []byte(`{"status":"failed","code":400,"message":"Unauthenticated"}`)}, nil)
	runID := f.play(t, `{}`)
	f.settle(t)
	var payload string
	if err := f.db.DB.QueryRow(`SELECT payload_json FROM outbox WHERE run_id=?`, runID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var p map[string]string
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	msg := p["message"]
	for _, want := range []string{"Job A", "job-a", "GET https://example.test/a", "HTTP 400", "Trigger:", "Run:", "Unauthenticated"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("alert message missing %q:\n%s", want, msg)
		}
	}
	if len(msg) > 3500 {
		t.Fatalf("alert message too long for a chat: %d bytes", len(msg))
	}
}
