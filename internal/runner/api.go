package runner

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/notify"
	"github.com/user4xn/cronsoul/internal/store"
)

type API struct {
	DB        *sql.DB
	Egress    EgressConfig
	SecretKey []byte
	Env       string
	// Policy is read immediately before each outbound request. It prevents
	// governance changes from being stranded in boot-time state.
	Policy func() EgressConfig
	// Do optionally replaces the network call (tests, future simulators). The
	// default remains the guarded egress client.
	Do func(context.Context, Request) (Result, error)
}

func NewAPI(db *sql.DB, e EgressConfig, a *auth.Service, secretKey ...string) http.Handler {
	x := &API{DB: db, Egress: e}
	if len(secretKey) > 0 {
		x.SecretKey = []byte(secretKey[0])
	}
	return NewAPIFromAPI(x, a)
}

// RegisterRoutes mounts runner endpoints on a parent chi router. This permits
// sibling API groups, such as notifications, to register without duplicate root mounts.
func RegisterRoutes(r chi.Router, x *API, a *auth.Service) {
	r.With(auth.RequireRole(a, "viewer")).Get("/runs", x.list)
	r.With(auth.RequireRole(a, "viewer")).Get("/stats", x.stats)
	r.With(auth.RequireRole(a, "viewer")).Get("/timeline", x.timeline)
	r.With(auth.RequireRole(a, "viewer")).Get("/runs/{id}", x.detail)
	r.With(auth.RequireRole(a, "qa")).Post("/jobs/{id}/run", x.run)
	r.With(auth.RequireRole(a, "qa")).Post("/jobs/{id}/preview", x.preview)
}

// NewAPIFromAPI preserves a standalone router for tests and embedders.
func NewAPIFromAPI(x *API, a *auth.Service) http.Handler {
	r := chi.NewRouter()
	RegisterRoutes(r, x, a)
	return r
}

// RunHandler exposes Play so a parent router that owns /jobs can mount it
// without double-wrapping the auth middleware.
func (a *API) RunHandler() http.HandlerFunc { return a.run }
func out(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// runOptions is the optional Play body. An absent body keeps the historical
// "no options" behaviour; assertion evaluation then follows the job setting.
type runOptions struct {
	UseAssertions *bool   `json:"use_assertions"`
	DryRun        *bool   `json:"dry_run"`
	SimulatedTime *string `json:"simulated_time"`
}

func parseRunOptions(w http.ResponseWriter, r *http.Request) (runOptions, bool) {
	var opts runOptions
	if r.Body == nil || r.ContentLength == 0 {
		return opts, true
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&opts); err != nil {
		if err.Error() == "EOF" {
			return opts, true
		}
		out(w, http.StatusBadRequest, map[string]string{"error": "invalid run options"})
		return opts, false
	}
	return opts, true
}

func (a *API) run(w http.ResponseWriter, r *http.Request) {
	opts, ok := parseRunOptions(w, r)
	if !ok {
		return
	}
	job := chi.URLParam(r, "id")
	var clockAware, allowProd int
	var dryParam, simHeader sql.NullString
	if err := a.DB.QueryRow(`SELECT clock_aware,allow_simulated_time_prod,dry_run_param,simulated_time_header FROM jobs WHERE id=?`, job).Scan(&clockAware, &allowProd, &dryParam, &simHeader); err != nil {
		out(w, 404, map[string]string{"error": "job not found"})
		return
	}
	if opts.SimulatedTime != nil && *opts.SimulatedTime != "" {
		if _, err := time.Parse(time.RFC3339, *opts.SimulatedTime); err != nil {
			out(w, 400, map[string]string{"error": "simulated_time must be RFC3339"})
			return
		}
		if clockAware == 0 || (a.Env == "prod" && allowProd == 0) {
			out(w, 422, map[string]string{"error": "simulation not permitted"})
			return
		}
	}
	if opts.DryRun != nil && *opts.DryRun && !dryParam.Valid {
		out(w, 422, map[string]string{"error": "dry_run not configured"})
		return
	}
	id := store.ID()
	now := time.Now().UTC().Format(time.RFC3339)
	tx, e := a.DB.Begin()
	if e != nil {
		out(w, 500, map[string]string{"error": "database error"})
		return
	}
	actorName, actorID, ok := auth.UserFromContext(r.Context())
	if !ok || actorID == "" {
		out(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		tx.Rollback()
		return
	}
	var override any
	if opts.UseAssertions != nil {
		if *opts.UseAssertions {
			override = 1
		} else {
			override = 0
		}
	}
	_, e = tx.Exec(`INSERT INTO runs(id,job_id,trigger,status,triggered_by,created_at,assertions_override,simulated,simulated_time,dry_run) VALUES(?,?, 'manual','queued',?,?,?,?,?,?)`, id, job, actorID, now, override, boolInt(opts.SimulatedTime != nil && *opts.SimulatedTime != ""), nullableOptString(opts.SimulatedTime), boolInt(opts.DryRun != nil && *opts.DryRun))
	if e != nil {
		tx.Rollback()
		if isConstraint(e) {
			out(w, 409, map[string]string{"error": "job not found or another run is active"})
		} else {
			out(w, 500, map[string]string{"error": "database error"})
		}
		return
	}
	_, e = tx.Exec(`INSERT INTO audit_log(id,actor_id,actor_name,action,target_type,target_id,ip) VALUES(?,?,?,?,?,?,?)`, store.ID(), actorID, actorName, "job.run", "run", id, r.RemoteAddr)
	if e != nil {
		tx.Rollback()
		out(w, 500, map[string]string{"error": "database error"})
		return
	}
	if e = tx.Commit(); e != nil {
		out(w, 500, map[string]string{"error": "database error"})
		return
	}
	out(w, http.StatusAccepted, map[string]string{"run_id": id, "status": "queued"})
}
func (a *API) markRunning(id string) error {
	_, err := a.DB.Exec(`UPDATE runs SET status='running',started_at=?,attempts=attempts+1 WHERE id=? AND status='queued'`, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// ProcessQueued executes durable queued work, including manual runs after restart.
func (a *API) ProcessQueued(ctx context.Context) error {
	rows, err := a.DB.QueryContext(ctx, `SELECT r.id,r.job_id,COALESCE(r.triggered_by,'') FROM runs r WHERE r.status='queued' AND NOT EXISTS (SELECT 1 FROM run_attempts a WHERE a.run_id=r.id AND a.next_attempt_at IS NOT NULL AND a.next_attempt_at > ?) ORDER BY r.created_at LIMIT 100`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	type item struct{ id, job, actor string }
	var items []item
	for rows.Next() {
		var x item
		if err = rows.Scan(&x.id, &x.job, &x.actor); err != nil {
			rows.Close()
			return err
		}
		items = append(items, x)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, x := range items {
		if err = ctx.Err(); err != nil {
			return err
		}
		a.execute(ctx, x.id, x.job, x.actor)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func nullableOptString(p *string) any {
	if p == nil || *p == "" {
		return nil
	}
	return *p
}

func isConstraint(e error) bool {
	return e != nil && (len(e.Error()) > 0 && (contains(e.Error(), "UNIQUE") || contains(e.Error(), "FOREIGN KEY")))
}
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func decryptAuth(key []byte, encoded string) (AuthConfig, error) {
	var out AuthConfig
	if len(key) != 32 {
		return out, fmt.Errorf("secret key must be 32 bytes")
	}
	packed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return out, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return out, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return out, err
	}
	if len(packed) < gcm.NonceSize() {
		return out, fmt.Errorf("invalid encrypted auth configuration")
	}
	plain, err := gcm.Open(nil, packed[:gcm.NonceSize()], packed[gcm.NonceSize():], nil)
	if err != nil {
		return out, fmt.Errorf("auth configuration decryption failed")
	}
	err = json.Unmarshal(plain, &out)
	return out, err
}

// requestExcerpt records what was called without leaking credentials: userinfo,
// query string and headers are dropped, only method and target are kept.

// withQuery returns the URL as it will actually be called, so a run can prove
// which parameters were attached — including the dry-run marker.
func withQuery(rawURL string, query map[string]string) string {
	if len(query) == 0 {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	for k, v := range query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func requestExcerpt(method, rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return method
	}
	u.Fragment = ""
	u.User = nil
	return method + " " + u.String()
}

func (a *API) do(ctx context.Context, req Request) (Result, error) {
	if a.Do != nil {
		return a.Do(ctx, req)
	}
	if a.Policy != nil {
		return a.Policy().Do(ctx, req)
	}
	return a.Egress.Do(ctx, req)
}

// assertionsFor decides whether this run evaluates assertions. A per-run
// override wins; otherwise the stored job flag applies. Rules must exist.
func assertionsFor(storedJSON sql.NullString, jobEnabled int, override sql.NullInt64) []AssertionRule {
	enabled := jobEnabled == 1
	if override.Valid {
		enabled = override.Int64 != 0
	}
	if !enabled || !storedJSON.Valid {
		return nil
	}
	rules, err := ParseAssertionRules(storedJSON.String)
	if err != nil {
		return nil
	}
	return rules
}

func expandMap(m map[string]string, v map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	for k, x := range m {
		m[k] = expandString(x, v)
	}
	return m
}

func (a *API) execute(ctx context.Context, id, jobID, actorID string) {
	var completedAttempts int
	var pending sql.NullString
	if err := a.DB.QueryRow(`SELECT COUNT(*), MAX(next_attempt_at) FROM run_attempts WHERE run_id=?`, id).Scan(&completedAttempts, &pending); err != nil {
		return
	}
	if pending.Valid {
		when, err := time.Parse(time.RFC3339, pending.String)
		if err != nil || time.Now().UTC().Before(when) {
			return
		}
	}
	var assertionsOverride sql.NullInt64
	_ = a.DB.QueryRow(`SELECT assertions_override FROM runs WHERE id=?`, id).Scan(&assertionsOverride)
	if _, err := a.DB.Exec(`UPDATE runs SET status='running',started_at=?,attempts=? WHERE id=? AND status='queued'`, time.Now().UTC().Format(time.RFC3339), completedAttempts+1, id); err != nil {
		return
	}
	if completedAttempts > 0 {
		// The durable queue has already recorded the prior attempt; this execution is its next attempt.
	}
	var method, rawURL, authType string
	var idempotent int
	var retryMax int
	var backoff string
	var retryJSON sql.NullString
	var authCipher sql.NullString
	var timeout int
	var headersJSON, queryJSON, bodyJSON, assertionsJSON sql.NullString
	var simulatedTime sql.NullString
	var simulated, dryRun int
	var assertionsEnabled int
	if a.DB.QueryRow(`SELECT method,url,auth_type,auth_cipher,timeout_s,headers_json,query_json,body_json,idempotent,retry_max,retry_backoff,retry_on_json,assertions_json,assertions_enabled FROM jobs WHERE id=?`, jobID).Scan(&method, &rawURL, &authType, &authCipher, &timeout, &headersJSON, &queryJSON, &bodyJSON, &idempotent, &retryMax, &backoff, &retryJSON, &assertionsJSON, &assertionsEnabled) != nil {
		a.finish(id, "failed", 0, "job not found")
		return
	}
	if timeout == 0 {
		timeout = 30
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	var headers, query map[string]string
	var authCfg AuthConfig
	if authType != "" && authType != "none" {
		if !authCipher.Valid {
			a.finish(id, "failed", nil, "missing encrypted auth configuration")
			return
		}
		var authErr error
		authCfg, authErr = decryptAuth(a.SecretKey, authCipher.String)
		if authErr != nil {
			a.finish(id, "failed", nil, authErr.Error())
			return
		}
		authCfg.Type = authType
	}
	var body []byte
	if headersJSON.Valid {
		_ = json.Unmarshal([]byte(headersJSON.String), &headers)
	}
	if queryJSON.Valid {
		_ = json.Unmarshal([]byte(queryJSON.String), &query)
	}
	if bodyJSON.Valid {
		body, _ = json.Marshal(json.RawMessage(bodyJSON.String))
	}
	var retryRules []string
	if retryJSON.Valid {
		_ = json.Unmarshal([]byte(retryJSON.String), &retryRules)
	}
	_ = a.DB.QueryRow(`SELECT simulated,simulated_time,dry_run FROM runs WHERE id=?`, id).Scan(&simulated, &simulatedTime, &dryRun)
	var dryParam, simHeader sql.NullString
	var clockAware, allowProd int
	_ = a.DB.QueryRow(`SELECT dry_run_param,simulated_time_header,clock_aware,allow_simulated_time_prod FROM jobs WHERE id=?`, jobID).Scan(&dryParam, &simHeader, &clockAware, &allowProd)
	rules := assertionsFor(assertionsJSON, assertionsEnabled, assertionsOverride)
	if simulated != 0 {
		st := simulatedTime.String
		vars := map[string]string{"simulated_time": st, "now": st, "date": st[:10]}
		if parsed, e := time.Parse(time.RFC3339, st); e == nil {
			vars["unix"] = strconv.FormatInt(parsed.Unix(), 10)
		}
		headers = expandMap(headers, vars)
		query = expandMap(query, vars)
		body, _ = ExpandJSON(body, vars)
		if simHeader.Valid && headers == nil {
			headers = map[string]string{}
		}
		if simHeader.Valid {
			headers[simHeader.String] = st
		}
	}
	if dryRun != 0 && dryParam.Valid {
		if query == nil {
			query = map[string]string{}
		}
		query[dryParam.String] = "true"
	}
	excerptRequest := requestExcerpt(method, withQuery(rawURL, query))
	for attempt := completedAttempts + 1; ; attempt++ {
		if attempt > 1 {
			if _, e := a.DB.Exec(`UPDATE runs SET status='running',attempts=?,started_at=? WHERE id=?`, attempt, time.Now().UTC().Format(time.RFC3339), id); e != nil {
				return
			}
		}

		started := time.Now().UTC().Format(time.RFC3339)
		res, err := a.do(ctx, Request{Method: method, URL: rawURL, Headers: headers, Query: query, Body: body, Auth: authCfg, Timeout: time.Duration(timeout) * time.Second, RunID: id, Trigger: "manual", TriggeredBy: actorID, IdempotencyKey: id, MaxResponse: 32 << 10, SimulatedTime: func() string {
			if simulatedTime.Valid {
				return simulatedTime.String
			}
			return ""
		}()})
		status := "success"
		var hs any
		var msg any
		if err != nil {
			status = "failed"
			msg = err.Error()
		} else {
			hs = res.Status
			if res.Status < 200 || res.Status >= 400 {
				status = "failed"
				msg = http.StatusText(res.Status)
			}
		}
		transient := Transient(err, res.Status, retryRules) && (idempotent == 1 || method == "GET" || method == "HEAD") && attempt <= retryMax
		attemptStatus := status
		if transient {
			attemptStatus = "failed"
			delay := RetryDelay(attempt, backoff == "fixed", 0)
			next := time.Now().UTC().Add(delay).Format(time.RFC3339)
			if _, dbErr := a.DB.Exec(`INSERT INTO run_attempts(id,run_id,attempt_no,started_at,finished_at,status,http_status,duration_ms,error,response_excerpt,next_attempt_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, store.ID(), id, attempt, started, time.Now().UTC().Format(time.RFC3339), attemptStatus, hs, res.Duration.Milliseconds(), msg, string(res.Body), next); dbErr != nil {
				_ = a.finish(id, "failed", hs, "run attempt persistence failed")
				return
			}
			if _, dbErr := a.DB.Exec(`UPDATE runs SET status='queued',attempts=?,error=?,http_status=?,response_excerpt=? WHERE id=?`, attempt, msg, hs, string(res.Body), id); dbErr != nil {
				return
			}
			return
		}
		if Transient(err, res.Status, retryRules) && (idempotent == 1 || method == "GET" || method == "HEAD") && attempt > retryMax {
			status = "failed_after_retries"
		}
		if _, dbErr := a.DB.Exec(`INSERT INTO run_attempts(id,run_id,attempt_no,started_at,finished_at,status,http_status,duration_ms,error,response_excerpt) VALUES(?,?,?,?,?,?,?,?,?,?)`, store.ID(), id, attempt, started, time.Now().UTC().Format(time.RFC3339), attemptStatus, hs, res.Duration.Milliseconds(), msg, string(res.Body)); dbErr != nil {
			_ = a.finish(id, "failed", hs, "run attempt persistence failed")
		}
		var excerpt any
		if len(res.Body) != 0 {
			excerpt = string(res.Body)
		}
		if _, dbErr := a.DB.Exec(`UPDATE runs SET status=?,finished_at=?,http_status=?,duration_ms=?,error=?,response_excerpt=?,response_truncated=?,request_excerpt=? WHERE id=?`, status, time.Now().UTC().Format(time.RFC3339), hs, res.Duration.Milliseconds(), msg, excerpt, res.Truncated, excerptRequest, id); dbErr != nil {
			return
		}
		// Assertions are evaluated after the HTTP outcome is durable so a broken
		// evaluator can never rewrite a real success/failure.
		if len(rules) > 0 {
			outcomes := EvaluateAssertions(rules, res)
			if err := a.storeAssertionOutcomes(id, outcomes); err != nil {
				return
			}
			for _, outcome := range outcomes {
				if !outcome.Passed {
					if err := notify.Enqueue(a.DB, "assertion_failed", jobID, id,
						map[string]string{"message": a.alertText("assertion_failed", jobID, id, status, hs, outcome.Kind+" expected "+outcome.Expected+", got "+outcome.Actual, res, attempt)}, time.Now().UTC()); err != nil {
						log.Printf("notify: assertion enqueue for run %s: %v", id, err)
					}
					break
				}
			}
		}
		// Any terminal failure alerts the channels. Gating this on
		// "failed_after_retries" silently swallowed every failure from an
		// endpoint that does not retry (the default) or that answers 4xx.
		if status == "failed" || status == "failed_after_retries" || status == "timeout" {
			if err := notify.Enqueue(a.DB, "failure", jobID, id,
				map[string]string{"message": a.alertText("failure", jobID, id, status, hs, msg, res, attempt)}, time.Now().UTC()); err != nil {
				log.Printf("notify: failure enqueue for run %s: %v", id, err)
			}
		}
		if status == "success" && completedAttempts > 0 {
			if err := notify.Enqueue(a.DB, "recovered", jobID, id,
				map[string]string{"message": a.alertText("recovered", jobID, id, status, hs, nil, res, attempt)}, time.Now().UTC()); err != nil {
				log.Printf("notify: recovery enqueue for run %s: %v", id, err)
			}
		}
		return
	}
}
func (a *API) finish(id, status string, hs any, msg any) error {
	_, err := a.DB.Exec(`UPDATE runs SET status=?,finished_at=?,http_status=?,error=? WHERE id=?`, status, time.Now().UTC().Format(time.RFC3339), hs, msg, id)
	return err
}

// alertText builds the message an operator reads on their phone. It carries the
// endpoint identity, the request, the outcome and the response body, because a
// bare "job failed" forces them back into the console to learn anything useful.
func (a *API) alertText(kind, jobID, runID, status string, httpStatus, errVal any, res Result, attempts int) string {
	var name, key, service, url, method, tz, cron string
	_ = a.DB.QueryRow(`SELECT name,key,service,url,method,COALESCE(timezone,'Asia/Jakarta'),cron FROM jobs WHERE id=?`, jobID).
		Scan(&name, &key, &service, &url, &method, &tz, &cron)
	var trigger, triggeredBy, createdAt, runErr, responseExcerpt sql.NullString
	var runHTTP, runDuration sql.NullInt64
	_ = a.DB.QueryRow(`SELECT trigger,COALESCE(triggered_by,''),created_at,COALESCE(error,''),COALESCE(response_excerpt,''),http_status,duration_ms FROM runs WHERE id=?`, runID).
		Scan(&trigger, &triggeredBy, &createdAt, &runErr, &responseExcerpt, &runHTTP, &runDuration)

	head := map[string]string{
		"failure":          "🔴 CronSoul · run failed",
		"assertion_failed": "🟠 CronSoul · assertion failed",
		"recovered":        "🟢 CronSoul · run recovered",
	}[kind]
	if head == "" {
		head = "CronSoul · " + kind
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteString("\n\n")
	if name != "" {
		fmt.Fprintf(&b, "Endpoint: %s (%s · %s)\n", name, key, service)
	}
	fmt.Fprintf(&b, "Request: %s %s\n", method, url)
	fmt.Fprintf(&b, "Schedule: %s · %s\n", cron, tz)

	code := ""
	if runHTTP.Valid {
		code = fmt.Sprintf("HTTP %d %s", runHTTP.Int64, http.StatusText(int(runHTTP.Int64)))
	}
	if httpStatus != nil && !runHTTP.Valid {
		code = "HTTP " + fmt.Sprint(httpStatus)
	}
	line := "Result: " + strings.ToUpper(strings.ReplaceAll(status, "_", " "))
	if code != "" {
		line += " · " + code
	}
	if runDuration.Valid {
		line += fmt.Sprintf(" · %d ms", runDuration.Int64)
	}
	if attempts > 0 {
		line += fmt.Sprintf(" · attempt %d", attempts)
	}
	b.WriteString(line + "\n")

	trig := trigger.String
	if trig == "" {
		trig = "manual"
	}
	if triggeredBy.String != "" {
		trig += " by " + triggeredBy.String
	}
	fmt.Fprintf(&b, "Trigger: %s\n", trig)
	if createdAt.Valid {
		when := createdAt.String
		if loc, err := time.LoadLocation(tz); err == nil {
			if t, err := time.Parse(time.RFC3339, createdAt.String); err == nil {
				when = t.In(loc).Format("2006-01-02 15:04:05 MST")
			}
		}
		fmt.Fprintf(&b, "Run: %s · %s\n", shortRun(runID), when)
	} else {
		fmt.Fprintf(&b, "Run: %s\n", shortRun(runID))
	}
	if runErr.String != "" {
		fmt.Fprintf(&b, "Error: %s\n", clip(runErr.String, 300))
	}
	if errVal != nil && fmt.Sprint(errVal) != "" && fmt.Sprint(errVal) != runErr.String {
		fmt.Fprintf(&b, "Detail: %s\n", clip(fmt.Sprint(errVal), 300))
	}
	body := responseExcerpt.String
	if body == "" && len(res.Body) > 0 {
		body = string(res.Body)
	}
	if body != "" {
		fmt.Fprintf(&b, "Response: %s\n", clip(strings.TrimSpace(body), 400))
	}
	out := strings.TrimSpace(b.String())
	if len(out) > 3500 {
		out = out[:3500] + "…"
	}
	return out
}

func shortRun(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// clip keeps a value readable in a chat message without cutting mid-word badly.
func clip(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// PreviewHandler exposes a dry validation of the stored job: it performs the
// guarded outbound request but never creates a run row.
func (a *API) PreviewHandler() http.HandlerFunc { return a.preview }

func (a *API) preview(w http.ResponseWriter, r *http.Request) {
	var method, rawURL, authType string
	var authCipher, headersJSON, queryJSON, bodyJSON, dryParam, simHeader sql.NullString
	var timeout, dryRun int
	err := a.DB.QueryRow(`SELECT method,url,auth_type,auth_cipher,timeout_s,headers_json,query_json,body_json,dry_run_param,simulated_time_header FROM jobs WHERE id=? AND enabled=1`, chi.URLParam(r, "id")).
		Scan(&method, &rawURL, &authType, &authCipher, &timeout, &headersJSON, &queryJSON, &bodyJSON, &dryParam, &simHeader)
	if err != nil {
		out(w, 404, map[string]string{"error": "job not found"})
		return
	}
	var in struct {
		SimulatedTime string `json:"simulated_time"`
		DryRun        bool   `json:"dry_run"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
	}
	if timeout == 0 {
		timeout = 30
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeout)*time.Second)
	defer cancel()

	var headers, query map[string]string
	if headersJSON.Valid {
		_ = json.Unmarshal([]byte(headersJSON.String), &headers)
	}
	if queryJSON.Valid {
		_ = json.Unmarshal([]byte(queryJSON.String), &query)
	}
	var body []byte
	if bodyJSON.Valid {
		body, _ = json.Marshal(json.RawMessage(bodyJSON.String))
	}
	var authCfg AuthConfig
	if authType != "" && authType != "none" {
		if !authCipher.Valid {
			out(w, 422, map[string]string{"error": "missing encrypted auth configuration"})
			return
		}
		authCfg, err = decryptAuth(a.SecretKey, authCipher.String)
		if err != nil {
			out(w, 422, map[string]string{"error": "auth configuration unreadable"})
			return
		}
		authCfg.Type = authType
	}
	if in.SimulatedTime != "" {
		if _, err := time.Parse(time.RFC3339, in.SimulatedTime); err != nil {
			out(w, 400, map[string]string{"error": "simulated_time must be RFC3339"})
			return
		}
		st := in.SimulatedTime
		vars := map[string]string{"simulated_time": st, "now": st, "date": st[:10]}
		if parsed, e := time.Parse(time.RFC3339, st); e == nil {
			vars["unix"] = strconv.FormatInt(parsed.Unix(), 10)
		}
		headers = expandMap(headers, vars)
		query = expandMap(query, vars)
		body, _ = ExpandJSON(body, vars)
		if simHeader.Valid {
			if headers == nil {
				headers = map[string]string{}
			}
			headers[simHeader.String] = st
		}
	}
	if in.DryRun && dryParam.Valid {
		if query == nil {
			query = map[string]string{}
		}
		query[dryParam.String] = "true"
		dryRun = 1
	}
	res, err := a.do(ctx, Request{
		Method: method, URL: rawURL, Headers: headers, Query: query, Body: body, Auth: authCfg,
		Timeout: time.Duration(timeout) * time.Second, Trigger: "preview", RunID: "preview",
		MaxResponse: 32 << 10, SimulatedTime: in.SimulatedTime,
	})
	if err != nil {
		out(w, 200, map[string]any{"ok": false, "error": err.Error(), "duration_ms": res.Duration.Milliseconds()})
		return
	}
	excerpt := string(res.Body)
	if len(excerpt) > 2048 {
		excerpt = excerpt[:2048]
	}
	out(w, 200, map[string]any{
		"ok": res.Status >= 200 && res.Status < 400, "status": res.Status,
		"duration_ms": res.Duration.Milliseconds(), "truncated": res.Truncated,
		"body_excerpt": excerpt, "dry_run": dryRun == 1, "simulated_time": in.SimulatedTime,
	})
}
