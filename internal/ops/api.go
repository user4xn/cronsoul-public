package ops

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/store"
)

type API struct {
	DB  *sql.DB
	Env string
}

func RegisterRoutes(r chi.Router, db *sql.DB, a *auth.Service, env ...string) {
	x := &API{DB: db}
	if len(env) > 0 {
		x.Env = env[0]
	}
	r.With(auth.RequireRole(a, "viewer")).Get("/alerts", x.alerts)
	r.With(auth.RequireRole(a, "viewer")).Get("/outbox", x.outbox)
	r.With(auth.RequireRole(a, "viewer")).Get("/settings", x.settings)
	r.With(auth.RequireRole(a, "superadmin")).Patch("/settings/scheduler", x.updateScheduler)
	r.With(auth.RequireRole(a, "superadmin")).Patch("/settings/display", x.updateDisplay)
	RegisterGovernance(r, db, a, x.Env)
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type alert struct {
	ID          string  `json:"id"`
	JobID       string  `json:"job_id"`
	WindowStart string  `json:"window_start"`
	Kind        string  `json:"kind"`
	Channel     *string `json:"channel"`
	SentAt      *string `json:"sent_at"`
	ResolvedAt  *string `json:"resolved_at"`
}
type outboxItem struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	JobID         *string `json:"job_id"`
	RunID         *string `json:"run_id"`
	Channel       string  `json:"channel"`
	Target        *string `json:"target"`
	Attempts      int     `json:"attempts"`
	NextAttemptAt string  `json:"next_attempt_at"`
	SentAt        *string `json:"sent_at"`
	Status        string  `json:"status"`
	LastError     *string `json:"last_error"`
}

func (a *API) alerts(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,job_id,window_start,kind,channel,sent_at,resolved_at FROM alerts ORDER BY window_start DESC LIMIT 200`)
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	data := []alert{}
	for rows.Next() {
		var x alert
		var channel, sent, resolved sql.NullString
		if err := rows.Scan(&x.ID, &x.JobID, &x.WindowStart, &x.Kind, &channel, &sent, &resolved); err != nil {
			write(w, 500, map[string]string{"error": "database error"})
			return
		}
		x.Channel = nullable(channel)
		x.SentAt = nullable(sent)
		x.ResolvedAt = nullable(resolved)
		data = append(data, x)
	}
	if err := rows.Err(); err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	write(w, 200, map[string]any{"data": data})
}

func (a *API) outbox(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "sent" && status != "failed" {
		write(w, 400, map[string]string{"error": "invalid outbox status"})
		return
	}
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,kind,job_id,run_id,channel,target,attempts,next_attempt_at,sent_at,status,last_error FROM outbox WHERE status=? ORDER BY next_attempt_at DESC LIMIT 200`, status)
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	data := []outboxItem{}
	for rows.Next() {
		var x outboxItem
		var job, run, target, sent, last sql.NullString
		if err := rows.Scan(&x.ID, &x.Kind, &job, &run, &x.Channel, &target, &x.Attempts, &x.NextAttemptAt, &sent, &x.Status, &last); err != nil {
			write(w, 500, map[string]string{"error": "database error"})
			return
		}
		x.JobID = nullable(job)
		x.RunID = nullable(run)
		x.Target = nullable(target)
		x.SentAt = nullable(sent)
		x.LastError = nullable(last)
		data = append(data, x)
	}
	if err := rows.Err(); err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	write(w, 200, map[string]any{"data": data, "status": status})
}

// updateScheduler turns background scheduling on or off. This is an operational
// switch (maintenance windows, post-deploy start), not a security control, so it
// is allowed in every environment; each change is audited and persisted.
func (a *API) updateScheduler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || req.Enabled == nil {
		write(w, http.StatusBadRequest, map[string]string{"error": "enabled boolean is required"})
		return
	}
	name, actorID, ok := auth.UserFromContext(r.Context())
	if !ok || actorID == "" {
		write(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	value := "false"
	if *req.Enabled {
		value = "true"
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer tx.Rollback()
	var before string
	if err = tx.QueryRowContext(r.Context(), "SELECT value FROM settings WHERE key='scheduler.enabled'").Scan(&before); err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	if _, err = tx.ExecContext(r.Context(), "UPDATE settings SET value=?,updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE key='scheduler.enabled'", value); err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO audit_log(id,actor_id,actor_name,action,target_type,target_id,before_json,after_json,ip) VALUES(?,?,?,?,?,?,?,?,?)`, store.ID(), actorID, name, "settings.scheduler.update", "setting", "scheduler.enabled", `{"enabled":`+before+`}`, `{"enabled":`+value+`}`, r.RemoteAddr)
	}
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	if err = tx.Commit(); err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	write(w, 200, map[string]any{"enabled": *req.Enabled})
}

// updateDisplay stores the timezone the console renders times in. Schedules
// keep their own per-endpoint zones; this only affects what humans read.
func (a *API) updateDisplay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Timezone string `json:"timezone"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || strings.TrimSpace(req.Timezone) == "" {
		write(w, http.StatusBadRequest, map[string]string{"error": "timezone is required"})
		return
	}
	loc, err := time.LoadLocation(req.Timezone)
	if err != nil {
		write(w, http.StatusBadRequest, map[string]string{"error": "unknown timezone"})
		return
	}
	name, actorID, ok := auth.UserFromContext(r.Context())
	if !ok || actorID == "" {
		write(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer tx.Rollback()
	var before string
	_ = tx.QueryRowContext(r.Context(), "SELECT value FROM settings WHERE key='display.timezone'").Scan(&before)
	if _, err = tx.ExecContext(r.Context(), `INSERT INTO settings(key,value,updated_at) VALUES('display.timezone',?,strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, req.Timezone); err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO audit_log(id,actor_id,actor_name,action,target_type,target_id,before_json,after_json,ip) VALUES(?,?,?,?,?,?,?,?,?)`,
			store.ID(), actorID, name, "settings.display.update", "setting", "display.timezone",
			`{"timezone":`+strconv.Quote(before)+`}`, `{"timezone":`+strconv.Quote(req.Timezone)+`}`, r.RemoteAddr)
	}
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	if err = tx.Commit(); err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	_, offset := time.Now().In(loc).Zone()
	write(w, 200, map[string]any{"timezone": req.Timezone, "zone": time.Now().In(loc).Format("MST"), "offset_min": offset / 60})
}

func (a *API) settings(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT key,value FROM settings ORDER BY key`)
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	data := map[string]any{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			write(w, 500, map[string]string{"error": "database error"})
			return
		}
		data[k] = settingValue(v)
	}
	if err := rows.Err(); err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	write(w, 200, map[string]any{"data": data, "env": a.Env})
}
func settingValue(v string) any {
	switch strings.ToLower(v) {
	case "true":
		return true
	case "false":
		return false
	}
	return v
}
func nullable(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}
