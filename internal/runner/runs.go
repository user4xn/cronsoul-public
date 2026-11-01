package runner

import (
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/store"
)

const (
	defaultRunLimit = 50
	maxRunLimit     = 200
)

var runStatuses = map[string]bool{
	"queued": true, "running": true, "success": true, "failed": true,
	"timeout": true, "failed_after_retries": true, "cancelled": true,
}

var runTriggers = map[string]bool{"manual": true, "schedule": true, "retry": true, "replay": true}

// runItem is the compact projection used by the history list. Bodies and
// attempt rows stay out of the list to keep pages small.
type runItem struct {
	ID                string  `json:"id"`
	JobID             string  `json:"job_id"`
	Trigger           string  `json:"trigger"`
	TriggeredBy       *string `json:"triggered_by"`
	ScheduledFor      *string `json:"scheduled_for"`
	Status            string  `json:"status"`
	HTTPStatus        *int    `json:"http_status"`
	DurationMS        *int    `json:"duration_ms"`
	Attempts          int     `json:"attempts"`
	Simulated         bool    `json:"simulated"`
	SimulatedTime     *string `json:"simulated_time"`
	DryRun            bool    `json:"dry_run"`
	ResponseTruncated bool    `json:"response_truncated"`
	AssertionPassed   *bool   `json:"assertion_passed"`
	StartedAt         *string `json:"started_at"`
	FinishedAt        *string `json:"finished_at"`
	Error             *string `json:"error"`
	CreatedAt         string  `json:"created_at"`
	JobName           string  `json:"job_name,omitempty"`
	JobKey            string  `json:"job_key,omitempty"`
	JobMethod         string  `json:"job_method,omitempty"`
	JobURL            string  `json:"job_url,omitempty"`
}

type attemptItem struct {
	AttemptNo       int     `json:"attempt_no"`
	Status          string  `json:"status"`
	HTTPStatus      *int    `json:"http_status"`
	DurationMS      *int    `json:"duration_ms"`
	Error           *string `json:"error"`
	StartedAt       *string `json:"started_at"`
	FinishedAt      *string `json:"finished_at"`
	ResponseExcerpt *string `json:"response_excerpt"`
	NextAttemptAt   *string `json:"next_attempt_at"`
}

type assertionItem struct {
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Passed   bool   `json:"passed"`
}

type runDetail struct {
	runItem
	RequestExcerpt  *string         `json:"request_excerpt"`
	ResponseExcerpt *string         `json:"response_excerpt"`
	AttemptsDetail  []attemptItem   `json:"attempts_detail"`
	AssertionResult []assertionItem `json:"assertion_results"`
}

const runColumns = `id,job_id,trigger,triggered_by,scheduled_for,status,http_status,duration_ms,attempts,simulated,simulated_time,dry_run,response_truncated,assertion_passed,started_at,finished_at,error,created_at`

func nullableString(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullableInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}

func scanRun(row interface{ Scan(...any) error }) (runItem, error) {
	var (
		x                                 runItem
		triggeredBy, scheduled, simTime   sql.NullString
		httpStatus, duration, assertionOK sql.NullInt64
		simulated, dryRun, truncated      int
		started, finished, errMsg         sql.NullString
	)
	err := row.Scan(&x.ID, &x.JobID, &x.Trigger, &triggeredBy, &scheduled, &x.Status, &httpStatus, &duration, &x.Attempts,
		&simulated, &simTime, &dryRun, &truncated, &assertionOK, &started, &finished, &errMsg, &x.CreatedAt)
	x.TriggeredBy = nullableString(triggeredBy)
	x.ScheduledFor = nullableString(scheduled)
	x.HTTPStatus = nullableInt(httpStatus)
	x.DurationMS = nullableInt(duration)
	x.Simulated = simulated != 0
	x.SimulatedTime = nullableString(simTime)
	x.DryRun = dryRun != 0
	x.ResponseTruncated = truncated != 0
	if assertionOK.Valid {
		passed := assertionOK.Int64 != 0
		x.AssertionPassed = &passed
	}
	x.StartedAt = nullableString(started)
	x.FinishedAt = nullableString(finished)
	x.Error = nullableString(errMsg)
	return x, err
}

type runFilter struct {
	jobID   string
	status  string
	trigger string
	from    string
	to      string
	cursor  *runCursor
	limit   int
}

type runCursor struct {
	createdAt string
	id        string
}

func encodeCursor(c runCursor) string {
	return base64.StdEncoding.EncodeToString([]byte(c.createdAt + "|" + c.id))
}

func decodeCursor(raw string) (*runCursor, bool) {
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, false
	}
	createdAt, id, ok := strings.Cut(string(decoded), "|")
	if !ok || createdAt == "" || id == "" {
		return nil, false
	}
	if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, false
	}
	return &runCursor{createdAt: createdAt, id: id}, true
}

func normTimestamp(raw string) (string, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	return t.UTC().Format(time.RFC3339), true
}

func parseRunFilter(q url.Values) (runFilter, bool) {
	f := runFilter{jobID: q.Get("job_id"), limit: defaultRunLimit}
	f.status = q.Get("status")
	if f.status != "" && !runStatuses[f.status] {
		return f, false
	}
	f.trigger = q.Get("trigger")
	if f.trigger != "" && !runTriggers[f.trigger] {
		return f, false
	}
	if raw := q.Get("from"); raw != "" {
		v, ok := normTimestamp(raw)
		if !ok {
			return f, false
		}
		f.from = v
	}
	if raw := q.Get("to"); raw != "" {
		v, ok := normTimestamp(raw)
		if !ok {
			return f, false
		}
		f.to = v
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxRunLimit {
			return f, false
		}
		f.limit = n
	}
	if raw := q.Get("cursor"); raw != "" {
		c, ok := decodeCursor(raw)
		if !ok {
			return f, false
		}
		f.cursor = c
	}
	return f, true
}

// attachJobs adds the endpoint identity to runs so the console can show which
// endpoint a run belongs to without a second round trip.
func attachJobs(db *sql.DB, runs []runItem) {
	if len(runs) == 0 {
		return
	}
	ids := make([]any, 0, len(runs))
	seen := map[string]bool{}
	for _, r := range runs {
		if !seen[r.JobID] {
			seen[r.JobID] = true
			ids = append(ids, r.JobID)
		}
	}
	q := "SELECT id,name,key,method,url FROM jobs WHERE id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")"
	rows, err := db.Query(q, ids...)
	if err != nil {
		return
	}
	defer rows.Close()
	type meta struct{ name, key, method, url string }
	found := map[string]meta{}
	for rows.Next() {
		var id string
		var m meta
		if rows.Scan(&id, &m.name, &m.key, &m.method, &m.url) == nil {
			found[id] = m
		}
	}
	for i := range runs {
		if m, ok := found[runs[i].JobID]; ok {
			runs[i].JobName, runs[i].JobKey, runs[i].JobMethod, runs[i].JobURL = m.name, m.key, m.method, m.url
		}
	}
}

// stats answers the dashboard KPI row: volume, success rate with its change,
// and how much needs attention right now.
func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	window := func(from, to time.Time) (total, ok, failed, retried int) {
		_ = a.DB.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(status='success'),0),
			COALESCE(SUM(status IN ('failed','failed_after_retries','timeout','error')),0),
			COALESCE(SUM(attempts>1),0)
			FROM runs WHERE created_at>=? AND created_at<?`,
			from.Format(time.RFC3339), to.Format(time.RFC3339)).Scan(&total, &ok, &failed, &retried)
		return
	}
	total, ok, failed, retried := window(now.Add(-24*time.Hour), now)
	prevTotal, prevOk, _, _ := window(now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	rate := func(o, t int) float64 {
		if t == 0 {
			return 0
		}
		return float64(o) / float64(t) * 100
	}
	var attention int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM (
		SELECT job_id, status, ROW_NUMBER() OVER (PARTITION BY job_id ORDER BY created_at DESC) rn FROM runs
	) WHERE rn=1 AND status IN ('failed','failed_after_retries','timeout','error')`).Scan(&attention)
	var openAlerts int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM alerts WHERE resolved_at IS NULL`).Scan(&openAlerts)
	var activeJobs int
	_ = a.DB.QueryRow(`SELECT COUNT(*) FROM jobs WHERE enabled=1`).Scan(&activeJobs)

	out(w, http.StatusOK, map[string]any{
		"runs_24h":          total,
		"success_24h":       ok,
		"failed_24h":        failed,
		"retried_24h":       retried,
		"success_rate":      rate(ok, total),
		"success_rate_prev": rate(prevOk, prevTotal),
		"runs_24h_prev":     prevTotal,
		"active_jobs":       activeJobs,
		"needs_attention":   attention,
		"open_alerts":       openAlerts,
	})
}

// list implements GET /runs: filtered history with keyset pagination.
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	f, ok := parseRunFilter(r.URL.Query())
	if !ok {
		out(w, http.StatusBadRequest, map[string]string{"error": "invalid run filter"})
		return
	}
	var where []string
	var args []any
	if f.jobID != "" {
		where = append(where, "job_id = ?")
		args = append(args, f.jobID)
	}
	if f.status != "" {
		where = append(where, "status = ?")
		args = append(args, f.status)
	}
	if f.trigger != "" {
		where = append(where, "trigger = ?")
		args = append(args, f.trigger)
	}
	if f.from != "" {
		where = append(where, "created_at >= ?")
		args = append(args, f.from)
	}
	if f.to != "" {
		where = append(where, "created_at <= ?")
		args = append(args, f.to)
	}
	if f.cursor != nil {
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, f.cursor.createdAt, f.cursor.createdAt, f.cursor.id)
	}
	query := "SELECT " + runColumns + " FROM runs"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, f.limit+1)

	rows, err := a.DB.Query(query, args...)
	if err != nil {
		out(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	data := []runItem{}
	for rows.Next() {
		x, err := scanRun(rows)
		if err != nil {
			out(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
			return
		}
		data = append(data, x)
	}
	if err := rows.Err(); err != nil {
		out(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	attachJobs(a.DB, data)
	var next *string
	if len(data) > f.limit {
		data = data[:f.limit]
		last := data[len(data)-1]
		c := encodeCursor(runCursor{createdAt: last.CreatedAt, id: last.ID})
		next = &c
	}
	out(w, http.StatusOK, map[string]any{"data": data, "next_cursor": next})
}

// detail implements GET /runs/{id}: run row, every attempt, both excerpts and
// the per-rule assertion outcomes.
func (a *API) detail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var (
		requestExcerpt, responseExcerpt sql.NullString
		base                            runItem
	)
	var (
		triggeredBy, scheduled, simTime   sql.NullString
		httpStatus, duration, assertionOK sql.NullInt64
		simulated, dryRun, truncated      int
		started, finished, errMsg         sql.NullString
	)
	err := a.DB.QueryRow(`SELECT `+runColumns+`,request_excerpt,response_excerpt FROM runs WHERE id=?`, id).
		Scan(&base.ID, &base.JobID, &base.Trigger, &triggeredBy, &scheduled, &base.Status, &httpStatus, &duration, &base.Attempts,
			&simulated, &simTime, &dryRun, &truncated, &assertionOK, &started, &finished, &errMsg, &base.CreatedAt,
			&requestExcerpt, &responseExcerpt)
	if err != nil {
		out(w, http.StatusNotFound, map[string]string{"error": "run not found"})
		return
	}
	base.TriggeredBy = nullableString(triggeredBy)
	base.ScheduledFor = nullableString(scheduled)
	base.HTTPStatus = nullableInt(httpStatus)
	base.DurationMS = nullableInt(duration)
	base.Simulated = simulated != 0
	base.SimulatedTime = nullableString(simTime)
	base.DryRun = dryRun != 0
	base.ResponseTruncated = truncated != 0
	if assertionOK.Valid {
		passed := assertionOK.Int64 != 0
		base.AssertionPassed = &passed
	}
	base.StartedAt = nullableString(started)
	base.FinishedAt = nullableString(finished)
	base.Error = nullableString(errMsg)

	identity := []runItem{base}
	attachJobs(a.DB, identity)
	base = identity[0]
	detail := runDetail{
		runItem:         base,
		RequestExcerpt:  nullableString(requestExcerpt),
		ResponseExcerpt: nullableString(responseExcerpt),
		AttemptsDetail:  []attemptItem{},
		AssertionResult: []assertionItem{},
	}

	attempts, err := a.DB.Query(`SELECT attempt_no,status,http_status,duration_ms,error,started_at,finished_at,response_excerpt,next_attempt_at FROM run_attempts WHERE run_id=? ORDER BY attempt_no`, id)
	if err != nil {
		out(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	for attempts.Next() {
		var (
			x                                        attemptItem
			httpStatus, duration                     sql.NullInt64
			errText, started, finished, body, nextAt sql.NullString
		)
		if err := attempts.Scan(&x.AttemptNo, &x.Status, &httpStatus, &duration, &errText, &started, &finished, &body, &nextAt); err != nil {
			attempts.Close()
			out(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
			return
		}
		x.HTTPStatus = nullableInt(httpStatus)
		x.DurationMS = nullableInt(duration)
		x.Error = nullableString(errText)
		x.StartedAt = nullableString(started)
		x.FinishedAt = nullableString(finished)
		x.ResponseExcerpt = nullableString(body)
		x.NextAttemptAt = nullableString(nextAt)
		detail.AttemptsDetail = append(detail.AttemptsDetail, x)
	}
	if err := attempts.Err(); err != nil {
		attempts.Close()
		out(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	attempts.Close()

	results, err := a.DB.Query(`SELECT kind,target,expected,actual,passed FROM assertion_results WHERE run_id=? ORDER BY rowid`, id)
	if err != nil {
		out(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	for results.Next() {
		var (
			x       assertionItem
			t, e, a sql.NullString
			passed  int
		)
		if err := results.Scan(&x.Kind, &t, &e, &a, &passed); err != nil {
			results.Close()
			out(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
			return
		}
		x.Target, x.Expected, x.Actual, x.Passed = t.String, e.String, a.String, passed != 0
		detail.AssertionResult = append(detail.AssertionResult, x)
	}
	if err := results.Err(); err != nil {
		results.Close()
		out(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	results.Close()

	out(w, http.StatusOK, detail)
}

// storeAssertionOutcomes replaces any previous results for the run and records
// the verdict without touching the HTTP-derived run status.
func (a *API) storeAssertionOutcomes(runID string, outcomes []Outcome) error {
	tx, err := a.DB.Begin()
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM assertion_results WHERE run_id=?`, runID); err != nil {
		tx.Rollback()
		return err
	}
	passed := true
	for _, o := range outcomes {
		flag := 0
		if o.Passed {
			flag = 1
		} else {
			passed = false
		}
		if _, err = tx.Exec(`INSERT INTO assertion_results(id,run_id,kind,target,expected,actual,passed) VALUES(?,?,?,?,?,?,?)`,
			store.ID(), runID, o.Kind, nullIfEmpty(o.Target), nullIfEmpty(o.Expected), nullIfEmpty(o.Actual), flag); err != nil {
			tx.Rollback()
			return err
		}
	}
	var verdict any
	if len(outcomes) > 0 {
		if passed {
			verdict = 1
		} else {
			verdict = 0
		}
	}
	if _, err = tx.Exec(`UPDATE runs SET assertion_passed=? WHERE id=?`, verdict, runID); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
