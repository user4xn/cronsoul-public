package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// stubTransport replaces the HTTP call so assertion behaviour can be exercised
// without touching the egress guard (which must keep rejecting loopback).
func stubTransport(api *API, res Result, err error) {
	api.Do = func(context.Context, Request) (Result, error) { return res, err }
}

func (f *runFixture) play(t *testing.T, body string) string {
	t.Helper()
	rec := f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/run", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("play %s -> %d %s", body, rec.Code, rec.Body)
	}
	var out struct {
		RunID  string `json:"run_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.RunID == "" || out.Status != "queued" {
		t.Fatalf("play response: %+v", out)
	}
	return out.RunID
}

func (f *runFixture) settle(t *testing.T) {
	t.Helper()
	if err := f.api.ProcessQueued(context.Background()); err != nil {
		t.Fatalf("process queued: %v", err)
	}
}

func (f *runFixture) runState(t *testing.T, id string) (status string, passed *bool) {
	t.Helper()
	if err := f.db.DB.QueryRow("SELECT status, assertion_passed FROM runs WHERE id=?", id).Scan(&status, &passed); err != nil {
		t.Fatal(err)
	}
	return status, passed
}

func (f *runFixture) assertionRows(t *testing.T, id string) map[string]bool {
	t.Helper()
	rows, err := f.db.DB.Query("SELECT kind, passed FROM assertion_results WHERE run_id=?", id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var kind string
		var ok bool
		if err := rows.Scan(&kind, &ok); err != nil {
			t.Fatal(err)
		}
		out[kind] = ok
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (f *runFixture) setRules(t *testing.T, rules string) {
	t.Helper()
	if _, err := f.db.DB.Exec("UPDATE jobs SET assertions_json=?, assertions_enabled=1 WHERE id=?", rules, f.jobID); err != nil {
		t.Fatal(err)
	}
}

func TestPlayRecordsAssertionFailureSeparatelyFromHTTPStatus(t *testing.T) {
	f := runSetup(t)
	f.setRules(t, `{"enabled":true,"rules":[{"kind":"status_code","value":200},{"kind":"json_path_gte","path":"data.total","value":5}]}`)
	stubTransport(f.api, Result{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"status":"success","data":{"total":2}}`), Duration: 40 * time.Millisecond}, nil)

	id := f.play(t, "")
	f.settle(t)

	status, passed := f.runState(t, id)
	if status != "success" {
		t.Fatalf("HTTP-derived status changed by assertions: %s", status)
	}
	if passed == nil || *passed {
		t.Fatalf("assertion_passed must be false, got %v", passed)
	}
	rows := f.assertionRows(t, id)
	if len(rows) != 2 || !rows["status_code"] || rows["json_path_gte"] {
		t.Fatalf("assertion rows: %+v", rows)
	}
}

func TestPlayAllPassingAssertionsSetsPassed(t *testing.T) {
	f := runSetup(t)
	f.setRules(t, `{"enabled":true,"rules":[{"kind":"status_code","value":200}]}`)
	stubTransport(f.api, Result{Status: 200, Body: []byte(`{"ok":true}`), Duration: time.Millisecond}, nil)

	id := f.play(t, "")
	f.settle(t)
	if _, passed := f.runState(t, id); passed == nil || !*passed {
		t.Fatalf("expected assertion pass, got %v", passed)
	}
	if rows := f.assertionRows(t, id); len(rows) != 1 || !rows["status_code"] {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestPlayUseAssertionsOverridesSingleRunOnly(t *testing.T) {
	f := runSetup(t)
	f.setRules(t, `{"enabled":true,"rules":[{"kind":"status_code","value":200}]}`)
	stubTransport(f.api, Result{Status: 200, Body: []byte(`{"ok":true}`), Duration: time.Millisecond}, nil)

	id := f.play(t, `{"use_assertions":false}`)
	f.settle(t)
	if rows := f.assertionRows(t, id); len(rows) != 0 {
		t.Fatalf("use_assertions=false must skip evaluation: %+v", rows)
	}
	if _, passed := f.runState(t, id); passed != nil {
		t.Fatalf("assertion_passed must stay null when skipped: %v", *passed)
	}
	var enabled int
	if err := f.db.DB.QueryRow("SELECT assertions_enabled FROM jobs WHERE id=?", f.jobID).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatal("per-run override must not persist into job config")
	}

	id = f.play(t, `{"use_assertions":true}`)
	f.settle(t)
	if rows := f.assertionRows(t, id); len(rows) != 1 {
		t.Fatalf("use_assertions=true rows: %+v", rows)
	}
}

func TestPlayEnablesAssertionsForRunWhenJobDisabled(t *testing.T) {
	f := runSetup(t)
	if _, err := f.db.DB.Exec("UPDATE jobs SET assertions_enabled=0 WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}
	stubTransport(f.api, Result{Status: 200, Body: []byte(`{"ok":true}`), Duration: time.Millisecond}, nil)

	id := f.play(t, "")
	f.settle(t)
	if rows := f.assertionRows(t, id); len(rows) != 0 {
		t.Fatalf("disabled job must not evaluate: %+v", rows)
	}
	id = f.play(t, `{"use_assertions":true}`)
	f.settle(t)
	if rows := f.assertionRows(t, id); len(rows) != 1 {
		t.Fatalf("explicit per-run opt-in rows: %+v", rows)
	}
}

func TestPlayWithoutRulesNeverInventsVerdict(t *testing.T) {
	f := runSetup(t)
	if _, err := f.db.DB.Exec("UPDATE jobs SET assertions_json=NULL, assertions_enabled=0 WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}
	stubTransport(f.api, Result{Status: 200, Body: []byte(`{"ok":true}`), Duration: time.Millisecond}, nil)

	for _, body := range []string{"", `{"use_assertions":true}`} {
		id := f.play(t, body)
		f.settle(t)
		if rows := f.assertionRows(t, id); len(rows) != 0 {
			t.Fatalf("rows=%+v", rows)
		}
		if _, passed := f.runState(t, id); passed != nil {
			t.Fatalf("assertion_passed must stay null: %v", *passed)
		}
	}
}

func TestPlayRejectsMalformedRunOptions(t *testing.T) {
	f := runSetup(t)
	for _, body := range []string{`{"use_assertions":"yes"}`, `{"bogus":1}`, `{`} {
		rec := f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/run", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s -> %d %s", body, rec.Code, rec.Body)
		}
	}
	// an empty body stays valid for clients that predate run options
	f.play(t, "")
}

func TestPlayRejectsUnsupportedRunOptions(t *testing.T) {
	f := runSetup(t)
	for _, body := range []string{`{"dry_run":true}`, `{"simulated_time":"2026-10-31T23:59:00Z"}`} {
		rec := f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/run", body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s -> %d %s", body, rec.Code, rec.Body)
		}
	}
	// explicit false/empty values are harmless and stay accepted
	for _, body := range []string{`{"dry_run":false}`, `{"simulated_time":""}`, `{}`} {
		if rec := f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/run", body); rec.Code != http.StatusAccepted {
			t.Fatalf("%s -> %d %s", body, rec.Code, rec.Body)
		}
		f.settle(t)
	}
}

func TestClockInjectionEndToEndAndProdGuard(t *testing.T) {
	f := runSetup(t)
	const simulated = "2026-11-30T23:59:00+07:00"
	if _, err := f.db.DB.Exec(`UPDATE jobs SET clock_aware=1,simulated_time_header='X-Test-Clock',body_json='{"date":"{{date}}","now":"{{now}}","unix":"{{unix}}","sim":"{{simulated_time}}"}' WHERE id=?`, f.jobID); err != nil {
		t.Fatal(err)
	}
	var got Request
	f.api.Egress = EgressConfig{Allowlist: []string{"receiver.example.test"}}
	f.api.Do = func(_ context.Context, req Request) (Result, error) {
		got = req
		return Result{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"data":{"simulated_time":"2026-11-30T23:59:00+07:00"}}`), Duration: time.Millisecond}, nil
	}
	id := f.play(t, `{"simulated_time":"`+simulated+`"}`)
	f.settle(t)
	if got.SimulatedTime != simulated || got.Headers["X-Test-Clock"] != simulated {
		t.Fatalf("simulation not sent: %+v", got)
	}
	wantBody := `{"date":"2026-11-30","now":"2026-11-30T23:59:00+07:00","sim":"2026-11-30T23:59:00+07:00","unix":"1796057940"}`
	if string(got.Body) != wantBody {
		t.Fatalf("expanded body=%s want=%s", got.Body, wantBody)
	}
	var simulatedFlag int
	var storedTime string
	if err := f.db.DB.QueryRow("SELECT simulated,simulated_time FROM runs WHERE id=?", id).Scan(&simulatedFlag, &storedTime); err != nil {
		t.Fatal(err)
	}
	if simulatedFlag != 1 || storedTime != simulated {
		t.Fatalf("persisted simulation=%d %q", simulatedFlag, storedTime)
	}

	f.api.Env = "prod"
	if _, err := f.db.DB.Exec("UPDATE jobs SET allow_simulated_time_prod=0 WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}
	before := 0
	_ = f.db.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&before)
	rec := f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/run", `{"simulated_time":"`+simulated+`"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("prod simulation status=%d %s", rec.Code, rec.Body)
	}
	after := 0
	_ = f.db.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&after)
	if before != after {
		t.Fatalf("forbidden prod simulation created run: %d -> %d", before, after)
	}
	// A clock-unaware job is rejected even with the production opt-in.
	if _, err := f.db.DB.Exec("UPDATE jobs SET allow_simulated_time_prod=1,clock_aware=0 WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}
	if rec = f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/run", `{"simulated_time":"`+simulated+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("non-clock-aware prod job accepted simulation: %d %s", rec.Code, rec.Body)
	}
	if _, err := f.db.DB.Exec("UPDATE jobs SET clock_aware=1 WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}

	if _, err := f.db.DB.Exec("UPDATE jobs SET allow_simulated_time_prod=1 WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}
	if rec = f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/run", `{"simulated_time":"`+simulated+`"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("allowed prod simulation: %d %s", rec.Code, rec.Body)
	}
	var prodRun struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prodRun); err != nil {
		t.Fatal(err)
	}
	f.settle(t)
	var status string
	var storedFlag int
	if err := f.db.DB.QueryRow("SELECT status,simulated FROM runs WHERE id=?", prodRun.RunID).Scan(&status, &storedFlag); err != nil {
		t.Fatal(err)
	}
	if status != "success" || storedFlag != 1 {
		t.Fatalf("prod opt-in run status=%s simulated=%d", status, storedFlag)
	}
}

func TestDryRunUsesConfiguredParameterOnly(t *testing.T) {
	f := runSetup(t)
	if _, err := f.db.DB.Exec("UPDATE jobs SET dry_run_param='dry_run' WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}
	var got Request
	f.api.Do = func(_ context.Context, req Request) (Result, error) {
		got = req
		return Result{Status: 200, Duration: time.Millisecond}, nil
	}
	id := f.play(t, `{"dry_run":true}`)
	f.settle(t)
	if got.Query["dry_run"] != "true" {
		t.Fatalf("dry_run query not sent: %+v", got.Query)
	}
	var flag int
	if err := f.db.DB.QueryRow("SELECT dry_run FROM runs WHERE id=?", id).Scan(&flag); err != nil {
		t.Fatal(err)
	}
	if flag != 1 {
		t.Fatalf("dry_run flag=%d", flag)
	}
	if _, err := f.db.DB.Exec("UPDATE jobs SET dry_run_param=NULL WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}
	before := 0
	_ = f.db.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&before)
	rec := f.do(t, http.MethodPost, "/jobs/"+f.jobID+"/run", `{"dry_run":true}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfigured dry run=%d %s", rec.Code, rec.Body)
	}
	after := 0
	_ = f.db.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&after)
	if before != after {
		t.Fatal("invalid dry run created a run")
	}
}

func TestPlayHTTPFailureStillEvaluatesAssertions(t *testing.T) {
	f := runSetup(t)
	f.setRules(t, `{"enabled":true,"rules":[{"kind":"status_code","value":404}]}`)
	stubTransport(f.api, Result{Status: 404, Body: []byte(`{"status":"error"}`), Duration: time.Millisecond}, nil)

	id := f.play(t, "")
	f.settle(t)
	status, passed := f.runState(t, id)
	if status != "failed" {
		t.Fatalf("HTTP status stays failed, got %s", status)
	}
	if passed == nil || !*passed {
		t.Fatalf("assertion verdict independent of HTTP: %v", passed)
	}
}

func TestRunDetailShowsPerRunAssertionVerdict(t *testing.T) {
	f := runSetup(t)
	f.setRules(t, `{"enabled":true,"rules":[{"kind":"status_code","value":200}]}`)
	stubTransport(f.api, Result{Status: 200, Body: []byte(`{"ok":true}`), Duration: time.Millisecond}, nil)

	id := f.play(t, "")
	f.settle(t)
	rec := f.do(t, http.MethodGet, "/runs/"+id, "")
	if rec.Code != 200 {
		t.Fatalf("detail=%d %s", rec.Code, rec.Body)
	}
	var got struct {
		Status           string `json:"status"`
		AssertionPassed  *bool  `json:"assertion_passed"`
		AssertionResults []struct {
			Kind   string `json:"kind"`
			Passed bool   `json:"passed"`
		} `json:"assertion_results"`
		AttemptsDetail []struct {
			AttemptNo int `json:"attempt_no"`
		} `json:"attempts_detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "success" || got.AssertionPassed == nil || !*got.AssertionPassed {
		t.Fatalf("detail: %+v", got)
	}
	if len(got.AssertionResults) != 1 || !got.AssertionResults[0].Passed {
		t.Fatalf("results: %+v", got.AssertionResults)
	}
	if len(got.AttemptsDetail) != 1 {
		t.Fatalf("attempts: %+v", got.AttemptsDetail)
	}
}
