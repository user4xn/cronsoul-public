package ops

import (
	"database/sql"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/store"
)

func (a *API) actor(r *http.Request) (string, string) {
	n, id, _ := auth.UserFromContext(r.Context())
	return n, id
}
func auditTx(tx *sql.Tx, r *http.Request, action, typ, target string, before, after any) error {
	name, id, _ := auth.UserFromContext(r.Context())
	var b, c any
	if before != nil {
		x, _ := json.Marshal(before)
		b = string(x)
	}
	if after != nil {
		x, _ := json.Marshal(after)
		c = string(x)
	}
	_, err := tx.Exec(`INSERT INTO audit_log(id,actor_id,actor_name,action,target_type,target_id,before_json,after_json,ip) VALUES(?,?,?,?,?,?,?,?,?)`, store.ID(), nullStr(id), name, action, typ, target, b, c, r.RemoteAddr)
	return err
}
func (a *API) audit(r *http.Request, action, typ, target string, before, after any) error {
	name, id := a.actor(r)
	var b, c any
	if before != nil {
		x, _ := json.Marshal(before)
		b = string(x)
	}
	if after != nil {
		x, _ := json.Marshal(after)
		c = string(x)
	}
	_, err := a.DB.ExecContext(r.Context(), `INSERT INTO audit_log(id,actor_id,actor_name,action,target_type,target_id,before_json,after_json,ip) VALUES(?,?,?,?,?,?,?,?,?)`, store.ID(), nullStr(id), name, action, typ, target, b, c, r.RemoteAddr)
	return err
}
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (a *API) users(w http.ResponseWriter, r *http.Request) {
	rows, err := a.DB.QueryContext(r.Context(), `SELECT id,username,email,role,disabled,must_change_password,last_login_at FROM users ORDER BY username`)
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	data := []map[string]any{}
	for rows.Next() {
		var id, name, role string
		var email, last sql.NullString
		var disabled, must int
		if rows.Scan(&id, &name, &email, &role, &disabled, &must, &last) != nil {
			write(w, 500, map[string]string{"error": "database error"})
			return
		}
		data = append(data, map[string]any{"id": id, "username": name, "email": nullable(email), "role": role, "disabled": disabled != 0, "must_change_password": must != 0, "last_login_at": nullable(last)})
	}
	if rows.Err() != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	write(w, 200, map[string]any{"data": data})
}
func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
		Email    string `json:"email"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&in) != nil || !regexp.MustCompile(`^[A-Za-z0-9._-]{2,64}$`).MatchString(in.Username) || len(in.Password) < 10 || (in.Role != "viewer" && in.Role != "qa" && in.Role != "admin") {
		write(w, 400, map[string]string{"error": "invalid user"})
		return
	}
	hash, e := store.HashPassword(in.Password)
	if e != nil {
		write(w, 500, map[string]string{"error": "internal error"})
		return
	}
	_, actor, _ := auth.UserFromContext(r.Context())
	id := store.ID()
	tx, e := a.DB.BeginTx(r.Context(), nil)
	if e != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(r.Context(), `INSERT INTO users(id,username,email,password_hash,role,must_change_password,created_by) VALUES(?,?,?,?,?,1,?)`, id, in.Username, nullStr(in.Email), hash, in.Role, nullStr(actor))
	if e != nil {
		write(w, 409, map[string]string{"error": "username unavailable"})
		return
	}
	if e = auditTx(tx, r, "user.create", "user", id, nil, map[string]string{"username": in.Username, "role": in.Role}); e != nil {
		write(w, 500, map[string]string{"error": "audit failure"})
		return
	}
	if e = tx.Commit(); e != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	write(w, 201, map[string]any{"id": id, "username": in.Username, "role": in.Role, "must_change_password": true})
}
func (a *API) userAction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	action := chi.URLParam(r, "action")
	var name, role string
	var disabled int
	if err := a.DB.QueryRowContext(r.Context(), "SELECT username,role,disabled FROM users WHERE id=?", id).Scan(&name, &role, &disabled); err != nil {
		write(w, 404, map[string]string{"error": "user not found"})
		return
	}
	_, actorID, _ := auth.UserFromContext(r.Context())
	if id == actorID {
		write(w, 409, map[string]string{"error": "cannot modify own account"})
		return
	}
	if role == "superadmin" {
		var n int
		if a.DB.QueryRowContext(r.Context(), "SELECT count(*) FROM users WHERE role='superadmin' AND disabled=0").Scan(&n) != nil || n <= 1 {
			write(w, 409, map[string]string{"error": "cannot remove last active superadmin"})
			return
		}
		if action == "delete" || action == "disable" {
			write(w, 403, map[string]string{"error": "superadmin deletion or disable prohibited"})
			return
		}
	}
	before := map[string]any{"username": name, "role": role, "disabled": disabled != 0}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer tx.Rollback()
	switch action {
	case "reset-password":
		var in struct {
			Password string `json:"password"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Password) < 10 {
			write(w, 400, map[string]string{"error": "password must be at least 10 characters"})
			return
		}
		var h string
		h, err = store.HashPassword(in.Password)
		if err == nil {
			_, err = tx.ExecContext(r.Context(), "UPDATE users SET password_hash=?,must_change_password=1,updated_at=? WHERE id=?", h, time.Now().UTC().Format(time.RFC3339), id)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), "UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL", time.Now().UTC().Format(time.RFC3339), id)
			}
		}
	case "disable", "enable":
		d := 0
		if action == "disable" {
			d = 1
		}
		_, err = tx.ExecContext(r.Context(), "UPDATE users SET disabled=?,updated_at=? WHERE id=?", d, time.Now().UTC().Format(time.RFC3339), id)
		if err == nil && d == 1 {
			_, err = tx.ExecContext(r.Context(), "UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL", time.Now().UTC().Format(time.RFC3339), id)
		}
	case "delete":
		_, err = tx.ExecContext(r.Context(), "DELETE FROM users WHERE id=?", id)
	default:
		write(w, 404, map[string]string{"error": "unknown action"})
		return
	}
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	if err = auditTx(tx, r, "user."+action, "user", id, before, nil); err != nil {
		write(w, 500, map[string]string{"error": "audit failure"})
		return
	}
	if err = tx.Commit(); err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	write(w, 200, map[string]bool{"ok": true})
}

var hostnameRE = regexp.MustCompile(`^(\*\.)?[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)

// normalizePattern accepts what an operator actually has on the clipboard: a
// host, host:port, IP, wildcard, or a full URL. It stores only the host form.
func normalizePattern(raw string) (string, bool) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", false
	}
	if strings.Contains(p, "://") {
		u, err := url.Parse(p)
		if err != nil || u.Host == "" || u.User != nil {
			return "", false
		}
		p = u.Host
	} else if i := strings.IndexAny(p, "/?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(p), "."))
	if p == "" || strings.Contains(p, "@") || strings.ContainsAny(p, " \t") {
		return "", false
	}
	// A bare IPv6 address has several colons and no brackets; SplitHostPort
	// would reject it, so bracket it before parsing.
	if strings.Count(p, ":") > 1 && !strings.HasPrefix(p, "[") {
		if ip := net.ParseIP(p); ip != nil && ip.To4() == nil {
			p = "[" + p + "]"
		}
	}
	// A bracketed IPv6 literal without a port is a complete pattern.
	if strings.HasPrefix(p, "[") && strings.HasSuffix(p, "]") {
		if ip := net.ParseIP(p[1 : len(p)-1]); ip != nil {
			return p, true
		}
		return "", false
	}
	h := p
	if strings.Contains(p, ":") {
		var err error
		h, _, err = net.SplitHostPort(p)
		if err != nil {
			return "", false
		}
		port, err := strconv.Atoi(p[strings.LastIndex(p, ":")+1:])
		if err != nil || port < 1 || port > 65535 {
			return "", false
		}
	}
	if net.ParseIP(strings.Trim(h, "[]")) != nil {
		return p, true
	}
	if !hostnameRE.MatchString(h) || strings.Contains(h, "..") {
		return "", false
	}
	if strings.HasPrefix(h, "*.") {
		h = h[2:]
	}
	if h == "" || strings.HasSuffix(h, ".") || !strings.Contains(h, ".") {
		return "", false
	}
	return p, true
}

func validPattern(p string) bool {
	_, ok := normalizePattern(p)
	return ok
}

func (a *API) allowlist(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, e := a.DB.QueryContext(r.Context(), "SELECT id,pattern,note,created_at FROM allowlist ORDER BY pattern")
		if e != nil {
			write(w, 500, map[string]string{"error": "database error"})
			return
		}
		defer rows.Close()
		data := []map[string]any{}
		for rows.Next() {
			var id, p, at string
			var note sql.NullString
			if rows.Scan(&id, &p, &note, &at) != nil {
				write(w, 500, map[string]string{"error": "database error"})
				return
			}
			data = append(data, map[string]any{"id": id, "pattern": p, "note": nullable(note), "created_at": at})
		}
		write(w, 200, map[string]any{"data": data})
		return
	}
	var in struct {
		Pattern string `json:"pattern"`
		Note    string `json:"note"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		write(w, 400, map[string]string{"error": "invalid request body"})
		return
	}
	pattern, ok := normalizePattern(in.Pattern)
	if !ok {
		write(w, 400, map[string]string{"error": "enter a host, host:port, IP, wildcard or a full URL"})
		return
	}
	in.Pattern = pattern
	_, actorID, _ := auth.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	tx, e := a.DB.BeginTx(r.Context(), nil)
	if e != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer tx.Rollback()
	var before any
	if r.Method == http.MethodPost {
		id = store.ID()
		_, e = tx.ExecContext(r.Context(), "INSERT INTO allowlist(id,pattern,note,created_by) VALUES(?,?,?,?)", id, in.Pattern, nullStr(in.Note), nullStr(actorID))
	} else {
		var oldPattern string
		var oldNote sql.NullString
		e = tx.QueryRowContext(r.Context(), "SELECT pattern,note FROM allowlist WHERE id=?", id).Scan(&oldPattern, &oldNote)
		before = map[string]any{"pattern": oldPattern, "note": nullable(oldNote)}
		if e == nil {
			_, e = tx.ExecContext(r.Context(), "UPDATE allowlist SET pattern=?,note=? WHERE id=?", in.Pattern, nullStr(in.Note), id)
		}
	}
	if e != nil {
		if strings.Contains(strings.ToUpper(e.Error()), "UNIQUE") {
			write(w, 409, map[string]string{"error": "pattern already exists", "pattern": pattern})
			return
		}
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	if e = auditTx(tx, r, "allowlist."+strings.ToLower(r.Method), "allowlist", id, before, map[string]string{"pattern": in.Pattern, "note": in.Note}); e != nil {
		write(w, 500, map[string]string{"error": "audit failure"})
		return
	}
	if e = tx.Commit(); e != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	write(w, 200, map[string]any{"id": id, "pattern": in.Pattern, "note": in.Note})
}
func (a *API) deleteAllow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer tx.Rollback()
	var p string
	if err = tx.QueryRowContext(r.Context(), "SELECT pattern FROM allowlist WHERE id=?", id).Scan(&p); err != nil {
		write(w, 404, map[string]string{"error": "entry not found"})
		return
	}
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM allowlist WHERE id=?", id); err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	if err = auditTx(tx, r, "allowlist.delete", "allowlist", id, map[string]string{"pattern": p}, nil); err != nil {
		write(w, 500, map[string]string{"error": "audit failure"})
		return
	}
	if err = tx.Commit(); err != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	w.WriteHeader(204)
}
func (a *API) security(w http.ResponseWriter, r *http.Request) {
	var enabled, until string
	_ = a.DB.QueryRowContext(r.Context(), "SELECT value FROM settings WHERE key='allow_insecure_targets'").Scan(&enabled)
	_ = a.DB.QueryRowContext(r.Context(), "SELECT value FROM settings WHERE key='allow_insecure_targets_until'").Scan(&until)
	exp, e := time.Parse(time.RFC3339, until)
	active := strings.EqualFold(enabled, "true") && e == nil && time.Now().Before(exp)
	if r.Method == http.MethodGet {
		if strings.EqualFold(enabled, "true") && !active {
			_, _ = a.DB.ExecContext(r.Context(), "UPDATE settings SET value='false',updated_at=? WHERE key='allow_insecure_targets'", time.Now().UTC().Format(time.RFC3339))
			enabled = "false"
			until = ""
		}
		write(w, 200, map[string]any{"allow_insecure_targets": active, "expires_at": func() any {
			if active {
				return until
			}
			return nil
		}(), "warning": active})
		return
	}
	var in struct {
		Duration string `json:"duration"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || (in.Duration != "1h" && in.Duration != "24h" && in.Duration != "7d") {
		write(w, 400, map[string]string{"error": "duration must be 1h, 24h, or 7d"})
		return
	}
	if strings.EqualFold(strings.TrimSpace(a.Env), "production") || strings.EqualFold(strings.TrimSpace(a.Env), "prod") {
		write(w, 403, map[string]string{"error": "insecure mode prohibited in production"})
		return
	}
	d := time.Hour
	if in.Duration == "24h" {
		d = 24 * time.Hour
	}
	if in.Duration == "7d" {
		d = 7 * 24 * time.Hour
	}
	until = time.Now().UTC().Add(d).Format(time.RFC3339)
	tx, e := a.DB.BeginTx(r.Context(), nil)
	if e != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, e = tx.ExecContext(r.Context(), "UPDATE settings SET value='true',updated_at=? WHERE key='allow_insecure_targets'", now); e == nil {
		_, e = tx.ExecContext(r.Context(), "UPDATE settings SET value=?,updated_at=? WHERE key='allow_insecure_targets_until'", until, now)
	}
	if e == nil {
		e = auditTx(tx, r, "security.insecure_http.enable", "setting", "allow_insecure_targets", map[string]any{"enabled": active}, map[string]any{"enabled": true, "expires_at": until})
	}
	if e != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	if e = tx.Commit(); e != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	write(w, 200, map[string]any{"allow_insecure_targets": true, "expires_at": until, "warning": true})
}
func (a *API) auditList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		write(w, 400, map[string]string{"error": "limit must be 1..200"})
		return
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		write(w, 400, map[string]string{"error": "offset must be non-negative"})
		return
	}
	like := "%" + q.Get("q") + "%"
	where := `WHERE (?='' OR actor_name=?) AND (?='' OR action=?) AND (?='' OR target_type=?) AND (?='' OR actor_name LIKE ? OR action LIKE ? OR COALESCE(target_id,'') LIKE ? OR COALESCE(target_type,'') LIKE ?)`
	whereArgs := []any{q.Get("actor"), q.Get("actor"), q.Get("action"), q.Get("action"), q.Get("target_type"), q.Get("target_type"), q.Get("q"), like, like, like, like}
	var total int
	if e := a.DB.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM audit_log `+where, whereArgs...).Scan(&total); e != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	rows, e := a.DB.QueryContext(r.Context(), `SELECT id,actor_name,action,target_type,target_id,ip,at FROM audit_log `+where+` ORDER BY at DESC,id DESC LIMIT ? OFFSET ?`, append(whereArgs, limit, offset)...)
	if e != nil {
		write(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	data := []map[string]any{}
	for rows.Next() {
		var id, name, action, at string
		var typ, target, ip sql.NullString
		if rows.Scan(&id, &name, &action, &typ, &target, &ip, &at) != nil {
			write(w, 500, map[string]string{"error": "database error"})
			return
		}
		data = append(data, map[string]any{"id": id, "actor_name": name, "action": action, "target_type": nullable(typ), "target_id": nullable(target), "ip": nullable(ip), "at": at})
	}
	write(w, 200, map[string]any{"data": data, "limit": limit, "offset": offset, "total": total})
}
func RegisterGovernance(r chi.Router, db *sql.DB, a *auth.Service, env ...string) {
	x := &API{DB: db}
	if len(env) > 0 {
		x.Env = env[0]
	}
	r.With(auth.RequireRole(a, "admin")).Get("/users", x.users)
	r.With(auth.RequireRole(a, "admin")).Post("/users", x.createUser)
	r.With(auth.RequireRole(a, "admin")).Post("/users/{id}/{action}", x.userAction)
	r.With(auth.RequireRole(a, "superadmin")).Get("/allowlist", x.allowlist)
	r.With(auth.RequireRole(a, "superadmin")).Post("/allowlist", x.allowlist)
	r.With(auth.RequireRole(a, "superadmin")).Put("/allowlist/{id}", x.allowlist)
	r.With(auth.RequireRole(a, "superadmin")).Delete("/allowlist/{id}", x.deleteAllow)
	r.With(auth.RequireRole(a, "superadmin")).Get("/security", x.security)
	r.With(auth.RequireRole(a, "superadmin")).Put("/security/insecure-http", x.security)
	r.With(auth.RequireRole(a, "admin")).Get("/audit", x.auditList)
}
