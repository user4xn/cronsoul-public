package runner

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// Retry keeps one run and one success after 503, 503, 200 — the durable queue
// re-picks the same run, so operators see 3 attempts, not 3 runs.
func TestRetryThreeAttemptsStayOneSuccessfulRun(t *testing.T) {
	f := runSetup(t)
	if _, err := f.db.DB.Exec("UPDATE jobs SET retry_max=2,retry_backoff='fixed' WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}
	statuses := []int{503, 503, 200}
	calls := 0
	f.api.Do = func(_ context.Context, _ Request) (Result, error) {
		code := statuses[len(statuses)-1]
		if calls < len(statuses) {
			code = statuses[calls]
		}
		calls++
		return Result{Status: code, Body: []byte(fmt.Sprintf(`{"attempt":%d}`, calls)), Duration: time.Millisecond}, nil
	}

	id := f.play(t, "")
	for i := 0; i < 4; i++ {
		f.settle(t)
		var status string
		if err := f.db.DB.QueryRow("SELECT status FROM runs WHERE id=?", id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status == "success" {
			break
		}
		// The queue honours next_attempt_at; fast-forward instead of sleeping.
		if _, err := f.db.DB.Exec("UPDATE run_attempts SET next_attempt_at=? WHERE run_id=?", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), id); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatalf("attempts made=%d want 3", calls)
	}
	var status string
	var attempts int
	if err := f.db.DB.QueryRow("SELECT status,attempts FROM runs WHERE id=?", id).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "success" || attempts != 3 {
		t.Fatalf("run status=%s attempts=%d", status, attempts)
	}
	var runs, rows int
	if err := f.db.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := f.db.DB.QueryRow("SELECT COUNT(*) FROM run_attempts WHERE run_id=?", id).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || rows != 3 {
		t.Fatalf("runs=%d attempt rows=%d", runs, rows)
	}
}

func TestRetryStopsAtMaxAndMarksFailedAfterRetries(t *testing.T) {
	f := runSetup(t)
	if _, err := f.db.DB.Exec("UPDATE jobs SET retry_max=1,retry_backoff='fixed' WHERE id=?", f.jobID); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.api.Do = func(_ context.Context, _ Request) (Result, error) {
		calls++
		return Result{Status: http.StatusBadGateway, Duration: time.Millisecond}, nil
	}
	id := f.play(t, "")
	for i := 0; i < 3; i++ {
		f.settle(t)
		if _, err := f.db.DB.Exec("UPDATE run_attempts SET next_attempt_at=? WHERE run_id=?", time.Now().UTC().Add(-time.Minute).Format(time.RFC3339), id); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	if err := f.db.DB.QueryRow("SELECT status FROM runs WHERE id=?", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed_after_retries" {
		t.Fatalf("status=%s want failed_after_retries (calls=%d)", status, calls)
	}
}
