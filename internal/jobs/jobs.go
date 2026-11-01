package jobs

import (
	"sort"
	"strconv"

	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"
	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/runner"
	"github.com/user4xn/cronsoul/internal/store"
)

// Assertions is the stored per-job assertion configuration. Absent means the
// job simply does not check anything after the HTTP call.
type Assertions struct {
	Enabled bool                   `json:"enabled"`
	Rules   []runner.AssertionRule `json:"rules"`
}

type Retry struct {
	Max     int      `json:"max"`
	Backoff string   `json:"backoff"`
	On      []string `json:"on,omitempty"`
}

type Notify struct {
	On       []string `json:"on,omitempty"`
	Emails   []string `json:"emails,omitempty"`
	Telegram []string `json:"telegram,omitempty"`
}

// AuthConfig is write-only: it is encrypted at rest and never returned.
type AuthConfig struct {
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config,omitempty"`
}

type Job struct {
	ID                     string          `json:"id"`
	Key                    string          `json:"key"`
	Name                   string          `json:"name"`
	Service                string          `json:"service"`
	Method                 string          `json:"method"`
	URL                    string          `json:"url"`
	Cron                   string          `json:"cron"`
	Timezone               string          `json:"timezone"`
	Timeout                int             `json:"timeout_s"`
	RiskTier               string          `json:"risk_tier"`
	Enabled                bool            `json:"enabled"`
	Headers                json.RawMessage `json:"headers,omitempty"`
	Query                  json.RawMessage `json:"query,omitempty"`
	Body                   json.RawMessage `json:"body,omitempty"`
	Assertions             *Assertions     `json:"assertions,omitempty"`
	AuthType               string          `json:"auth_type"`
	Retry                  Retry           `json:"retry"`
	Notify                 Notify          `json:"notify"`
	Singleton              bool            `json:"singleton"`
	Idempotent             bool            `json:"idempotent"`
	DryRunParam            string          `json:"dry_run_param,omitempty"`
	ClockAware             bool            `json:"clock_aware"`
	SimulatedTimeHeader    string          `json:"simulated_time_header"`
	AllowSimulatedTimeProd bool            `json:"allow_simulated_time_prod"`
	Tags                   []string        `json:"tags,omitempty"`
	NextRunAt              string          `json:"next_run_at,omitempty"`
	LastRunStatus          string          `json:"last_run_status,omitempty"`
	LastRunAt              string          `json:"last_run_at,omitempty"`
	AssertionCount         int             `json:"assertion_count"`
	CreatedAt              string          `json:"created_at"`
	UpdatedAt              string          `json:"updated_at"`
}

type input struct {
	Key                    string          `json:"key"`
	Name                   string          `json:"name"`
	Service                string          `json:"service"`
	Method                 string          `json:"method"`
	URL                    string          `json:"url"`
	Cron                   string          `json:"cron"`
	Timezone               string          `json:"timezone"`
	Timeout                int             `json:"timeout_s"`
	RiskTier               string          `json:"risk_tier"`
	Enabled                *bool           `json:"enabled"`
	Headers                json.RawMessage `json:"headers"`
	Query                  json.RawMessage `json:"query"`
	Body                   json.RawMessage `json:"body"`
	Assertions             *Assertions     `json:"assertions"`
	Auth                   *AuthConfig     `json:"auth"`
	Retry                  *Retry          `json:"retry"`
	Notify                 *Notify         `json:"notify"`
	Singleton              *bool           `json:"singleton"`
	Idempotent             *bool           `json:"idempotent"`
	DryRunParam            *string         `json:"dry_run_param"`
	ClockAware             *bool           `json:"clock_aware"`
	SimulatedTimeHeader    *string         `json:"simulated_time_header"`
	AllowSimulatedTimeProd *bool           `json:"allow_simulated_time_prod"`
	Tags                   []string        `json:"tags"`
}

var authTypes = map[string]bool{"": true, "none": true, "bearer": true, "api_key": true, "basic": true, "hmac_sha256": true, "custom_header": true}

func encryptAuth(key, plain []byte) (string, error) {
	if len(key) != 32 {
		return "", errors.New("secret key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(nonce, gcm.Seal(nil, nonce, plain, nil)...)), nil
}

func decryptAuth(key []byte, raw string) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("secret key must be 32 bytes")
	}
	payload, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(payload) < gcm.NonceSize() {
		return nil, errors.New("invalid encrypted auth")
	}
	return gcm.Open(nil, payload[:gcm.NonceSize()], payload[gcm.NonceSize():], nil)
}

func encodeList(v []string) any {
	if len(v) == 0 {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

func decodeList(v sql.NullString) []string {
	if !v.Valid || v.String == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(v.String), &out)
	return out
}

var paramPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
var headerPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

var keyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

// New mounts the job registry. When a Play handler is supplied it is exposed as
// POST /{id}/run here, because /api/v1/jobs is mounted as its own subtree and
// would otherwise shadow the runner's route.
func New(db *sql.DB, a *auth.Service, key []byte, play http.HandlerFunc, preview ...http.HandlerFunc) http.Handler {
	r := chi.NewRouter()
	read := func(next http.Handler) http.Handler { return auth.RequireRole(a, "viewer")(next) }
	write := func(next http.Handler) http.Handler { return auth.RequireRole(a, "admin")(next) }
	r.With(read).Get("/", list(db))
	r.With(write).Post("/", create(db, key))
	r.With(read).Get("/upcoming", upcoming(db))
	r.With(auth.RequireRole(a, "qa")).Post("/schedule-preview", schedulePreview)
	r.With(read).Get("/{id}", get(db))
	r.With(write).Patch("/{id}", update(db, key))
	r.With(write).Put("/{id}", update(db, key))
	r.With(write).Delete("/{id}", remove(db))
	if play != nil {
		r.With(auth.RequireRole(a, "qa")).Post("/{id}/run", play)
	}
	if len(preview) > 0 && preview[0] != nil {
		r.With(auth.RequireRole(a, "qa")).Post("/{id}/preview", preview[0])
	}
	return r
}

// schedulePreview answers "when would this fire?" for the endpoint form, so the
// operator sees real dates and a validation error before saving.
func schedulePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Cron     string `json:"cron"`
		Timezone string `json:"timezone"`
		Count    int    `json:"count"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<14)).Decode(&in); err != nil {
		writeError(w, 400, "invalid JSON body")
		return
	}
	if in.Count <= 0 || in.Count > 12 {
		in.Count = 3
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(strings.TrimSpace(in.Cron))
	if err != nil {
		jsonOut(w, 400, map[string]any{"error": "invalid cron expression", "detail": err.Error()})
		return
	}
	zone := strings.TrimSpace(in.Timezone)
	if zone == "" {
		zone = "Asia/Jakarta"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		jsonOut(w, 400, map[string]any{"error": "unknown timezone", "detail": err.Error()})
		return
	}
	now := time.Now().In(loc)
	next := now
	runs := make([]string, 0, in.Count)
	for i := 0; i < in.Count; i++ {
		next = sched.Next(next)
		if next.IsZero() {
			break
		}
		runs = append(runs, next.Format(time.RFC3339))
	}
	var first string
	if len(runs) > 0 {
		first = runs[0]
	}
	jsonOut(w, 200, map[string]any{"runs": runs, "timezone": zone, "next": first, "now": now.Format(time.RFC3339)})
}

func valid(in *input) error {
	in.Key = strings.TrimSpace(in.Key)
	in.Name = strings.TrimSpace(in.Name)
	in.Service = strings.TrimSpace(in.Service)
	in.Method = strings.ToUpper(strings.TrimSpace(in.Method))
	if !keyPattern.MatchString(in.Key) || in.Name == "" || in.Service == "" {
		return errors.New("key, name, and service are required; key must be lowercase alphanumeric, dash, or underscore")
	}
	switch in.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
	default:
		return errors.New("unsupported method")
	}
	u, e := url.ParseRequestURI(in.URL)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return errors.New("url must be an absolute http(s) URL")
	}
	if _, e = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse(strings.TrimSpace(in.Cron)); e != nil {
		return fmt.Errorf("invalid cron expression: %w", e)
	}
	if in.Timezone == "" {
		in.Timezone = "Asia/Jakarta"
	}
	if _, e = time.LoadLocation(in.Timezone); e != nil {
		return errors.New("invalid timezone")
	}
	if in.Timeout == 0 {
		in.Timeout = 30
	}
	if in.Timeout < 1 || in.Timeout > 600 {
		return errors.New("timeout_s must be between 1 and 600")
	}
	if in.RiskTier == "" {
		if in.Method == "GET" || in.Method == "HEAD" {
			in.RiskTier = "A"
		} else {
			in.RiskTier = "B"
		}
	}
	if in.RiskTier != "A" && in.RiskTier != "B" && in.RiskTier != "C" {
		return errors.New("risk_tier must be A, B, or C")
	}
	for n, p := range map[string]json.RawMessage{"headers": in.Headers, "query": in.Query, "body": in.Body} {
		if len(p) > 0 && !json.Valid(p) {
			return fmt.Errorf("%s must be valid JSON", n)
		}
		if len(p) > 0 && p[0] != '{' && n != "body" {
			return fmt.Errorf("%s must be a JSON object", n)
		}
	}
	if in.Assertions != nil {
		if len(in.Assertions.Rules) > 0 {
			if e := runner.ValidateAssertions(in.Assertions.Rules); e != nil {
				return e
			}
		}
		// Assertions are an optional extra layer: enabled without rules would
		// silently report nothing, so it is rejected instead.
		if in.Assertions.Enabled && len(in.Assertions.Rules) == 0 {
			return errors.New("assertions.enabled requires at least one rule")
		}
	}
	if in.Auth != nil {
		if !authTypes[strings.ToLower(strings.TrimSpace(in.Auth.Type))] {
			return errors.New("unsupported auth.type")
		}
		if len(in.Auth.Config) > 0 && !json.Valid(in.Auth.Config) {
			return errors.New("auth.config must be valid JSON")
		}
	}
	if in.Retry != nil {
		if in.Retry.Max < 0 || in.Retry.Max > 5 {
			return errors.New("retry.max must be between 0 and 5")
		}
		if b := strings.ToLower(strings.TrimSpace(in.Retry.Backoff)); b != "" && b != "exponential" && b != "fixed" {
			return errors.New("retry.backoff must be exponential or fixed")
		}
		for _, on := range in.Retry.On {
			switch on {
			case "http_5xx", "http_429", "timeout", "network_error":
			default:
				return errors.New("retry.on supports http_5xx, http_429, timeout, network_error")
			}
		}
	}
	if in.Notify != nil {
		for _, on := range in.Notify.On {
			switch on {
			case "failure", "missed_run", "recovered", "assertion_failed", "success", "slow_run":
			default:
				return errors.New("notify.on has an unsupported event")
			}
		}
		if len(in.Notify.Emails) > 20 || len(in.Notify.Telegram) > 20 {
			return errors.New("notify supports at most 20 destinations per channel")
		}
		for _, addr := range in.Notify.Emails {
			if !strings.Contains(addr, "@") || strings.ContainsAny(addr, " \t\n") {
				return errors.New("notify.emails contains an invalid address")
			}
		}
	}
	if in.DryRunParam != nil {
		v := strings.TrimSpace(*in.DryRunParam)
		if v != "" && !paramPattern.MatchString(v) {
			return errors.New("dry_run_param must be a simple parameter name")
		}
	}
	if in.SimulatedTimeHeader != nil {
		v := strings.TrimSpace(*in.SimulatedTimeHeader)
		if v != "" && !headerPattern.MatchString(v) {
			return errors.New("simulated_time_header must be a valid HTTP header name")
		}
	}
	if len(in.Tags) > 20 {
		return errors.New("at most 20 tags are allowed")
	}
	for _, tag := range in.Tags {
		if strings.TrimSpace(tag) == "" || len(tag) > 32 {
			return errors.New("each tag must be 1-32 characters")
		}
	}
	return nil
}

// assertionColumns renders the stored assertion config back to the API shape.
func assertionColumns(raw sql.NullString, enabled int) (*Assertions, error) {
	if !raw.Valid {
		return nil, nil
	}
	rules, err := runner.ParseAssertionRules(raw.String)
	if err != nil {
		return nil, err
	}
	return &Assertions{Enabled: enabled != 0, Rules: rules}, nil
}

func assertionValues(a *Assertions) (any, int) {
	if a == nil {
		return nil, 0
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil, 0
	}
	enabled := 0
	if a.Enabled {
		enabled = 1
	}
	return string(b), enabled
}
func decode(w http.ResponseWriter, r *http.Request) (input, bool) {
	var in input
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil {
		writeError(w, 400, "invalid JSON request")
		return in, false
	}
	if e := valid(&in); e != nil {
		writeError(w, 400, e.Error())
		return in, false
	}
	return in, true
}
func writeError(w http.ResponseWriter, status int, msg string) {
	jsonOut(w, status, map[string]string{"error": msg})
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func scan(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var h, q, b, a, retryOn, notifyOn, notifyEmail, notifyTG, tags, dryRun sql.NullString
	var enabled, assertionsEnabled, singleton, idempotent, clockAware, allowProd int
	e := row.Scan(
		&j.ID, &j.Key, &j.Name, &j.Service, &j.Method, &j.URL, &j.Cron, &j.Timezone,
		&j.Timeout, &j.RiskTier, &enabled, &h, &q, &b, &a, &assertionsEnabled,
		&j.AuthType, &j.Retry.Max, &j.Retry.Backoff, &retryOn, &notifyOn, &notifyEmail, &notifyTG,
		&singleton, &idempotent, &dryRun, &tags, &clockAware, &j.SimulatedTimeHeader, &allowProd,
		&j.CreatedAt, &j.UpdatedAt,
	)
	j.Enabled = enabled != 0
	j.Singleton = singleton != 0
	j.Idempotent = idempotent != 0
	j.ClockAware = clockAware != 0
	j.AllowSimulatedTimeProd = allowProd != 0
	if dryRun.Valid {
		j.DryRunParam = dryRun.String
	}
	j.Retry.On = decodeList(retryOn)
	j.Notify = Notify{
		On:       decodeList(notifyOn),
		Emails:   decodeList(notifyEmail),
		Telegram: decodeList(notifyTG),
	}
	j.Tags = decodeList(tags)
	if h.Valid {
		j.Headers = json.RawMessage(h.String)
	}
	if q.Valid {
		j.Query = json.RawMessage(q.String)
	}
	if b.Valid {
		j.Body = json.RawMessage(b.String)
	}
	if e == nil {
		j.Assertions, e = assertionColumns(a, assertionsEnabled)
	}
	return j, e
}

const columns = `id,key,name,service,method,url,cron,timezone,timeout_s,risk_tier,enabled,headers_json,query_json,body_json,assertions_json,assertions_enabled,` +
	`auth_type,retry_max,retry_backoff,retry_on_json,notify_on_json,notify_email_json,notify_telegram_json,` +
	`singleton,idempotent,dry_run_param,tags_json,clock_aware,simulated_time_header,allow_simulated_time_prod,created_at,updated_at`

const insertSQL = `INSERT INTO jobs(` + columns + `,auth_cipher) VALUES(` +
	`?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,` +
	`?,?,?,?,?,?,?,` +
	`?,?,?,?,?,?,?,?,?,?)`

const updateSQL = `UPDATE jobs SET key=?,name=?,service=?,method=?,url=?,cron=?,timezone=?,timeout_s=?,risk_tier=?,enabled=?,` +
	`headers_json=?,query_json=?,body_json=?,assertions_json=?,assertions_enabled=?,` +
	`auth_type=?,retry_max=?,retry_backoff=?,retry_on_json=?,notify_on_json=?,notify_email_json=?,notify_telegram_json=?,` +
	`singleton=?,idempotent=?,dry_run_param=?,tags_json=?,clock_aware=?,simulated_time_header=?,allow_simulated_time_prod=?,auth_cipher=?,updated_at=? WHERE id=?`

func values(j Job, authCipher any) []any {
	var h, q, b any
	if len(j.Headers) > 0 {
		h = string(j.Headers)
	}
	if len(j.Query) > 0 {
		q = string(j.Query)
	}
	if len(j.Body) > 0 {
		b = string(j.Body)
	}
	assertionsJSON, assertionsEnabled := assertionValues(j.Assertions)
	enabled, singleton, idempotent := 0, 0, 0
	if j.Enabled {
		enabled = 1
	}
	if j.Singleton {
		singleton = 1
	}
	if j.Idempotent {
		idempotent = 1
	}
	clockAware, allowProd := 0, 0
	if j.ClockAware {
		clockAware = 1
	}
	if j.AllowSimulatedTimeProd {
		allowProd = 1
	}
	var dryRun any
	if strings.TrimSpace(j.DryRunParam) != "" {
		dryRun = strings.TrimSpace(j.DryRunParam)
	}
	return []any{
		j.ID, j.Key, j.Name, j.Service, j.Method, j.URL, j.Cron, j.Timezone, j.Timeout, j.RiskTier, enabled,
		h, q, b, assertionsJSON, assertionsEnabled,
		j.AuthType, j.Retry.Max, j.Retry.Backoff, encodeList(j.Retry.On), encodeList(j.Notify.On), encodeList(j.Notify.Emails), encodeList(j.Notify.Telegram),
		singleton, idempotent, dryRun, encodeList(j.Tags), clockAware, j.SimulatedTimeHeader, allowProd,
		j.CreatedAt, j.UpdatedAt,
		authCipher,
	}
}

func audit(tx *sql.Tx, actorID, actor, action, id string, before, after any, ip string) error {
	var b, a any
	if before != nil {
		x, _ := json.Marshal(before)
		b = string(x)
	}
	if after != nil {
		x, _ := json.Marshal(after)
		a = string(x)
	}
	_, e := tx.Exec(`INSERT INTO audit_log(id,actor_id,actor_name,action,target_type,target_id,before_json,after_json,ip) VALUES(?,?,?,?,?,?,?,?,?)`, store.ID(), actorID, actor, action, "job", id, b, a, ip)
	return e
}
func actor(r *http.Request) (string, string, bool) { return auth.UserFromContext(r.Context()) }

func buildJob(in input, id, createdAt string) (Job, error) {
	j := Job{
		ID: id, Key: in.Key, Name: in.Name, Service: in.Service, Method: in.Method,
		URL: in.URL, Cron: in.Cron, Timezone: in.Timezone, Timeout: in.Timeout,
		RiskTier: in.RiskTier, Enabled: true, Headers: in.Headers, Query: in.Query,
		Body: in.Body, Assertions: in.Assertions,
		Retry:     Retry{Max: 0, Backoff: "exponential"},
		Singleton: true,
		Tags:      in.Tags, CreatedAt: createdAt, UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if in.Auth != nil {
		j.AuthType = strings.TrimSpace(in.Auth.Type)
	}
	if in.Enabled != nil {
		j.Enabled = *in.Enabled
	}
	if j.AuthType == "" {
		j.AuthType = "none"
	}
	if in.Retry != nil {
		j.Retry = *in.Retry
		if j.Retry.Backoff == "" {
			j.Retry.Backoff = "exponential"
		}
	}
	if in.Notify != nil {
		j.Notify = *in.Notify
	}
	if in.Singleton != nil {
		j.Singleton = *in.Singleton
	}
	if in.Idempotent != nil {
		j.Idempotent = *in.Idempotent
	}
	if in.DryRunParam != nil {
		j.DryRunParam = *in.DryRunParam
	}
	if in.ClockAware != nil {
		j.ClockAware = *in.ClockAware
	}
	if in.SimulatedTimeHeader != nil && strings.TrimSpace(*in.SimulatedTimeHeader) != "" {
		j.SimulatedTimeHeader = strings.TrimSpace(*in.SimulatedTimeHeader)
	}
	if in.AllowSimulatedTimeProd != nil {
		j.AllowSimulatedTimeProd = *in.AllowSimulatedTimeProd
	}
	if j.SimulatedTimeHeader == "" {
		j.SimulatedTimeHeader = "X-Simulated-Time"
	}
	if in.Auth != nil && len(in.Auth.Config) > 0 {
		if j.AuthType == "" || j.AuthType == "none" {
			return j, errors.New("auth.config requires an auth.type")
		}
	}
	return j, nil
}

func authCipherFor(key []byte, in input) (any, error) {
	if in.Auth == nil || len(in.Auth.Config) == 0 {
		return nil, nil
	}
	cfg, err := json.Marshal(in.Auth.Config)
	if err != nil {
		return nil, err
	}
	return encryptAuth(key, cfg)
}

func create(db *sql.DB, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decode(w, r)
		if !ok {
			return
		}
		j, err := buildJob(in, store.ID(), time.Now().UTC().Format(time.RFC3339))
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		cipherText, err := authCipherFor(key, in)
		if err != nil {
			writeError(w, 500, "auth encryption unavailable")
			return
		}
		tx, e := db.Begin()
		if e != nil {
			writeError(w, 500, "database error")
			return
		}
		args := values(j, cipherText)
		_, e = tx.Exec(insertSQL, args...)
		if e == nil {
			name, actorID, ok := actor(r)
			if !ok || actorID == "" {
				tx.Rollback()
				writeError(w, 401, "unauthorized")
				return
			}
			e = audit(tx, actorID, name, "job.create", j.ID, nil, j, r.RemoteAddr)
		}
		if e != nil {
			tx.Rollback()
			if strings.Contains(e.Error(), "UNIQUE") {
				writeError(w, 409, "key already exists")
			} else {
				writeError(w, 500, "database error: "+e.Error())
			}
			return
		}
		if tx.Commit() != nil {
			writeError(w, 500, "database error")
			return
		}
		jsonOut(w, 201, j)
	}
}
func get(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		j, e := scan(db.QueryRow("SELECT "+columns+" FROM jobs WHERE id=? AND enabled=1", chi.URLParam(r, "id")))
		if e != nil {
			writeError(w, 404, "job not found")
			return
		}
		jsonOut(w, 200, j)
	}
}

// upcoming lists the scheduled runs that will fire inside the requested window,
// so the console can show what is about to happen instead of only the past.
func upcoming(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		minutes := 60
		if v := r.URL.Query().Get("minutes"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 10080 {
				writeError(w, 400, "minutes must be between 1 and 10080")
				return
			}
			minutes = n
		}
		now := time.Now().UTC()
		until := now.Add(time.Duration(minutes) * time.Minute)

		enabled := "true"
		_ = db.QueryRow("SELECT value FROM settings WHERE key='scheduler.enabled'").Scan(&enabled)

		rows, err := db.Query("SELECT id,key,name,method,url,cron,timezone FROM jobs WHERE enabled=1 ORDER BY name")
		if err != nil {
			writeError(w, 500, "database error")
			return
		}
		defer rows.Close()
		type slot struct {
			JobID    string `json:"job_id"`
			JobKey   string `json:"job_key"`
			JobName  string `json:"job_name"`
			Method   string `json:"job_method"`
			URL      string `json:"job_url"`
			At       string `json:"at"`
			InSecond int    `json:"in_seconds"`
		}
		out := []slot{}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		for rows.Next() {
			var id, key, name, method, url, expr, tz string
			if rows.Scan(&id, &key, &name, &method, &url, &expr, &tz) != nil {
				writeError(w, 500, "database error")
				return
			}
			loc, err := time.LoadLocation(tz)
			if err != nil {
				loc = time.UTC
			}
			sched, err := parser.Parse(expr)
			if err != nil {
				continue
			}
			// Walk forward from now in the job's own timezone.
			next := sched.Next(now.In(loc))
			for i := 0; i < 20 && !next.IsZero() && next.Before(until.In(loc)); i++ {
				at := next.UTC()
				out = append(out, slot{JobID: id, JobKey: key, JobName: name, Method: method, URL: url,
					At: at.Format(time.RFC3339), InSecond: int(at.Sub(now).Seconds())})
				next = sched.Next(next)
			}
		}
		if err := rows.Err(); err != nil {
			writeError(w, 500, "database error")
			return
		}
		sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
		if len(out) > 100 {
			out = out[:100]
		}
		jsonOut(w, 200, map[string]any{"data": out, "minutes": minutes, "scheduler_enabled": enabled == "true", "now": now.Format(time.RFC3339)})
	}
}

// nextRunAt returns the next cron occurrence in UTC, so the inventory can show
// when each endpoint will actually fire without waiting for a run to happen.
func nextRunAt(j Job, now time.Time) string {
	if !j.Enabled {
		return ""
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(j.Cron)
	if err != nil {
		return ""
	}
	loc, err := time.LoadLocation(j.Timezone)
	if err != nil {
		loc = time.UTC
	}
	next := sched.Next(now.In(loc))
	if next.IsZero() {
		return ""
	}
	return next.UTC().Format(time.RFC3339)
}

type lastRun struct{ status, at string }

// lastRuns returns the newest run per endpoint in one query, so the inventory can
// show "failed 12m ago" without an N+1.
func lastRuns(db *sql.DB) map[string]lastRun {
	out := map[string]lastRun{}
	rows, err := db.Query(`SELECT job_id,status,created_at FROM (
		SELECT job_id,status,created_at,ROW_NUMBER() OVER (PARTITION BY job_id ORDER BY created_at DESC) rn FROM runs
	) WHERE rn=1`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var st lastRun
		if rows.Scan(&id, &st.status, &st.at) == nil {
			out[id] = st
		}
	}
	return out
}

func list(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit <= 0 {
			limit = 50
		}
		if limit > 200 {
			writeError(w, 400, "limit must be 1..200")
			return
		}
		where := []string{"1=1"}
		args := []any{}
		if q.Get("include_disabled") != "true" {
			where = append(where, "enabled=1")
		}
		if v := strings.TrimSpace(q.Get("service")); v != "" {
			where = append(where, "service=?")
			args = append(args, v)
		}
		if v := strings.TrimSpace(q.Get("q")); v != "" {
			where = append(where, "(name LIKE ? OR key LIKE ? OR url LIKE ?)")
			like := "%" + v + "%"
			args = append(args, like, like, like)
		}
		if v := strings.TrimSpace(q.Get("cursor")); v != "" {
			where = append(where, "created_at < ?")
			args = append(args, v)
		}
		args = append(args, limit+1)
		rows, e := db.Query("SELECT "+columns+" FROM jobs WHERE "+strings.Join(where, " AND ")+" ORDER BY created_at DESC, id DESC LIMIT ?", args...)
		if e != nil {
			writeError(w, 500, "database error")
			return
		}
		defer rows.Close()
		data := []Job{}
		for rows.Next() {
			j, e := scan(rows)
			if e != nil {
				writeError(w, 500, "database error")
				return
			}
			data = append(data, j)
		}
		now := time.Now().UTC()
		last := lastRuns(db)
		for i := range data {
			data[i].NextRunAt = nextRunAt(data[i], now)
			if st, ok := last[data[i].ID]; ok {
				data[i].LastRunStatus, data[i].LastRunAt = st.status, st.at
			}
			if data[i].Assertions != nil {
				data[i].AssertionCount = len(data[i].Assertions.Rules)
			}
		}
		var next any
		if len(data) > limit {
			data = data[:limit]
			next = data[len(data)-1].CreatedAt
		}
		jsonOut(w, 200, map[string]any{"data": data, "next_cursor": next})
	}
}

func update(db *sql.DB, key []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		in, ok := decode(w, r)
		if !ok {
			return
		}
		id := chi.URLParam(r, "id")
		before, e := scan(db.QueryRow("SELECT "+columns+" FROM jobs WHERE id=? AND enabled=1", id))
		if e != nil {
			writeError(w, 404, "job not found")
			return
		}
		j, err := buildJob(in, id, before.CreatedAt)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		if j.Assertions == nil && before.Assertions != nil {
			// Omitting assertions in a PATCH keeps the stored configuration.
			j.Assertions = before.Assertions
		}
		var cipherText any
		if in.Auth != nil && len(in.Auth.Config) > 0 {
			cipherText, err = authCipherFor(key, in)
			if err != nil {
				writeError(w, 500, "auth encryption unavailable")
				return
			}
		} else {
			// Preserve the stored credential when the request omits it.
			var existing sql.NullString
			if e := db.QueryRow("SELECT auth_cipher FROM jobs WHERE id=?", id).Scan(&existing); e == nil && existing.Valid {
				cipherText = existing.String
			}
		}
		v := values(j, cipherText)
		args := append(v[1:30], v[32], v[31], id)
		tx, e := db.Begin()
		if e != nil {
			writeError(w, 500, "database error")
			return
		}
		_, e = tx.Exec(updateSQL, args...)
		if e == nil {
			name, actorID, ok := actor(r)
			if !ok || actorID == "" {
				tx.Rollback()
				writeError(w, 401, "unauthorized")
				return
			}
			e = audit(tx, actorID, name, "job.update", id, before, j, r.RemoteAddr)
		}
		if e != nil {
			tx.Rollback()
			if strings.Contains(e.Error(), "UNIQUE") {
				writeError(w, 409, "key already exists")
			} else {
				writeError(w, 500, "database error: "+e.Error())
			}
			return
		}
		if tx.Commit() != nil {
			writeError(w, 500, "database error")
			return
		}
		jsonOut(w, 200, j)
	}
}
func remove(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		before, e := scan(db.QueryRow("SELECT "+columns+" FROM jobs WHERE id=? AND enabled=1", id))
		if e != nil {
			writeError(w, 404, "job not found")
			return
		}
		tx, e := db.Begin()
		if e != nil {
			writeError(w, 500, "database error")
			return
		}
		_, e = tx.Exec("UPDATE jobs SET enabled=0,updated_at=? WHERE id=?", time.Now().UTC().Format(time.RFC3339), id)
		if e == nil {
			name, actorID, ok := actor(r)
			if !ok || actorID == "" {
				tx.Rollback()
				writeError(w, 401, "unauthorized")
				return
			}
			e = audit(tx, actorID, name, "job.delete", id, before, nil, r.RemoteAddr)
		}
		if e != nil {
			tx.Rollback()
			writeError(w, 500, "database error")
			return
		}
		if tx.Commit() != nil {
			writeError(w, 500, "database error")
			return
		}
		w.WriteHeader(204)
	}
}
