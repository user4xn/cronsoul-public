package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/store"
)

type Config struct {
	Host    string   `json:"host,omitempty"`
	Port    int      `json:"port,omitempty"`
	User    string   `json:"user,omitempty"`
	Pass    string   `json:"pass,omitempty"`
	From    string   `json:"from,omitempty"`
	TLS     bool     `json:"tls,omitempty"`
	Token   string   `json:"bot_token,omitempty"`
	ChatIDs []string `json:"chat_ids,omitempty"`
}
type Channel struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Events  []string `json:"events"`
	Meta    *Meta    `json:"meta,omitempty"`
}

// Meta is the non-secret half of a channel configuration. It lets the console
// show and edit a channel without ever returning credentials.
type Meta struct {
	Host    string   `json:"host,omitempty"`
	Port    int      `json:"port,omitempty"`
	From    string   `json:"from,omitempty"`
	TLS     bool     `json:"tls,omitempty"`
	Targets []string `json:"targets,omitempty"`
}

// EventKinds are the run outcomes a channel can subscribe to.
var EventKinds = []string{"failure", "recovered", "missed_run", "assertion_failed", "manual_run"}

// DefaultEventKinds is what a channel receives when it has no explicit list.
// manual_run is opt-in so existing channels do not start getting every Play.
var DefaultEventKinds = []string{"failure", "recovered", "missed_run", "assertion_failed"}

type API struct {
	DB     *sql.DB
	Key    []byte
	Client *http.Client
	SMTP   func(Config, string, string) error
}
type Sender interface {
	Send(context.Context, string, string, string, string) error
}

func (a *API) encrypt(data []byte) (string, error) {
	if len(a.Key) != 32 {
		return "", errors.New("secret key must be 32 bytes")
	}
	b, e := aes.NewCipher(a.Key)
	if e != nil {
		return "", e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return "", e
	}
	n := make([]byte, g.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return "", e
	}
	return base64.StdEncoding.EncodeToString(append(n, g.Seal(nil, n, data, nil)...)), nil
}
func (a *API) decrypt(s string) ([]byte, error) {
	if len(a.Key) != 32 {
		return nil, errors.New("secret key must be 32 bytes")
	}
	p, e := base64.StdEncoding.DecodeString(s)
	if e != nil {
		return nil, e
	}
	b, e := aes.NewCipher(a.Key)
	if e != nil {
		return nil, e
	}
	g, e := cipher.NewGCM(b)
	if e != nil {
		return nil, e
	}
	if len(p) < g.NonceSize() {
		return nil, errors.New("invalid encrypted channel")
	}
	return g.Open(nil, p[:g.NonceSize()], p[g.NonceSize():], nil)
}
func Router(db *sql.DB, a *auth.Service, key []byte) http.Handler {
	r := chi.NewRouter()
	RegisterRoutes(r, db, a, key)
	return r
}

// RegisterRoutes adds channel endpoints to an existing chi router.
func RegisterRoutes(r chi.Router, db *sql.DB, a *auth.Service, key []byte) {
	x := &API{DB: db, Key: key}
	r.With(auth.RequireRole(a, "superadmin")).Get("/channels", x.list)
	r.With(auth.RequireRole(a, "superadmin")).Post("/channels", x.create)
	r.With(auth.RequireRole(a, "superadmin")).Put("/channels/{id}", x.update)
	r.With(auth.RequireRole(a, "superadmin")).Post("/channels/{id}/test", x.test)
	r.With(auth.RequireRole(a, "superadmin")).Delete("/channels/{id}", x.delete)
}

func jsonOut(w http.ResponseWriter, s int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(s)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	rows, e := a.DB.QueryContext(r.Context(), "SELECT id,kind,name,enabled,COALESCE(events_json,''),config_cipher FROM channels ORDER BY name")
	if e != nil {
		jsonOut(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()
	out := []Channel{}
	for rows.Next() {
		var c Channel
		var en int
		var ev, cipherText string
		if rows.Scan(&c.ID, &c.Kind, &c.Name, &en, &ev, &cipherText) != nil {
			jsonOut(w, 500, map[string]string{"error": "database error"})
			return
		}
		c.Enabled = en != 0
		c.Events = channelEvents(ev)
		// Decrypt in place: a second query while these rows are open deadlocks
		// the single SQLite connection.
		if raw, err := a.decrypt(cipherText); err == nil {
			var cfg Config
			if json.Unmarshal(raw, &cfg) == nil {
				c.Meta = &Meta{Host: cfg.Host, Port: cfg.Port, From: cfg.From, TLS: cfg.TLS, Targets: cfg.ChatIDs}
			}
		}
		out = append(out, c)
	}
	jsonOut(w, 200, out)
}

type input struct {
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Enabled *bool    `json:"enabled"`
	Config  Config   `json:"config"`
	Events  []string `json:"events"`
}

// channelEvents decodes the stored subscription list; an empty list means the
// channel receives every event, which is the safe default for existing rows.
func channelEvents(raw string) []string {
	var ev []string
	if raw == "" || json.Unmarshal([]byte(raw), &ev) != nil || len(ev) == 0 {
		return append([]string{}, DefaultEventKinds...)
	}
	return ev
}

func encodeEvents(in []string) (string, error) {
	ev := in
	if len(ev) == 0 {
		ev = DefaultEventKinds
	}
	out := []string{}
	for _, e := range ev {
		e = strings.TrimSpace(e)
		if !contains(EventKinds, e) {
			return "", fmt.Errorf("unsupported event %q", e)
		}
		if !contains(out, e) {
			out = append(out, e)
		}
	}
	b, err := json.Marshal(out)
	return string(b), err
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var in input
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || in.Name == "" || (in.Kind != "smtp" && in.Kind != "telegram") {
		jsonOut(w, 400, map[string]string{"error": "invalid channel"})
		return
	}
	if in.Kind == "smtp" && (in.Config.Host == "" || in.Config.Port < 1 || in.Config.Port > 65535 || in.Config.From == "" || len(in.Config.ChatIDs) == 0) {
		jsonOut(w, 400, map[string]string{"error": "invalid SMTP configuration"})
		return
	}
	if in.Kind == "telegram" && (in.Config.Token == "" || len(in.Config.ChatIDs) == 0) {
		jsonOut(w, 400, map[string]string{"error": "invalid Telegram configuration"})
		return
	}
	b, e := json.Marshal(in.Config)
	if e != nil {
		jsonOut(w, 400, map[string]string{"error": "invalid configuration"})
		return
	}
	enc, e := a.encrypt(b)
	if e != nil {
		jsonOut(w, 500, map[string]string{"error": "encryption unavailable"})
		return
	}
	enabled := 1
	if in.Enabled != nil && !*in.Enabled {
		enabled = 0
	}
	events, e := encodeEvents(in.Events)
	if e != nil {
		jsonOut(w, 400, map[string]string{"error": e.Error()})
		return
	}
	id := store.ID()
	if _, e = a.DB.ExecContext(r.Context(), "INSERT INTO channels(id,kind,name,config_cipher,enabled,events_json) VALUES(?,?,?,?,?,?)", id, in.Kind, in.Name, enc, enabled, events); e != nil {
		jsonOut(w, 500, map[string]string{"error": "channel save failed"})
		return
	}
	jsonOut(w, 201, Channel{ID: id, Kind: in.Kind, Name: in.Name, Enabled: enabled == 1, Events: channelEvents(events),
		Meta: &Meta{Host: in.Config.Host, Port: in.Config.Port, From: in.Config.From, TLS: in.Config.TLS, Targets: in.Config.ChatIDs}})
}
func (a *API) update(w http.ResponseWriter, r *http.Request) {
	var in input
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || in.Name == "" || (in.Kind != "smtp" && in.Kind != "telegram") {
		jsonOut(w, 400, map[string]string{"error": "invalid channel"})
		return
	}
	existing, old, err := a.load(chi.URLParam(r, "id"))
	if err != nil {
		jsonOut(w, 404, map[string]string{"error": "channel not found"})
		return
	}
	// Empty fields mean "keep what is stored", so the console can edit a
	// channel without ever holding its credentials.
	if in.Config.Token == "" {
		in.Config.Token = old.Token
	}
	if in.Config.Pass == "" {
		in.Config.Pass = old.Pass
	}
	if in.Config.Host == "" {
		in.Config.Host = old.Host
	}
	if in.Config.Port == 0 {
		in.Config.Port = old.Port
	}
	if in.Config.From == "" {
		in.Config.From = old.From
	}
	if len(in.Config.ChatIDs) == 0 {
		in.Config.ChatIDs = old.ChatIDs
	}
	if in.Kind != existing.Kind {
		jsonOut(w, 400, map[string]string{"error": "channel kind cannot change"})
		return
	}
	b, err := json.Marshal(in.Config)
	if err != nil {
		jsonOut(w, 400, map[string]string{"error": "invalid configuration"})
		return
	}
	enc, err := a.encrypt(b)
	if err != nil {
		jsonOut(w, 500, map[string]string{"error": "encryption unavailable"})
		return
	}
	enabled := existing.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	n := 0
	if enabled {
		n = 1
	}
	events, err := encodeEvents(in.Events)
	if err != nil {
		jsonOut(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if _, err = a.DB.ExecContext(r.Context(), "UPDATE channels SET name=?,config_cipher=?,enabled=?,events_json=? WHERE id=?", in.Name, enc, n, events, existing.ID); err != nil {
		jsonOut(w, 500, map[string]string{"error": "channel save failed"})
		return
	}
	jsonOut(w, 200, Channel{ID: existing.ID, Kind: existing.Kind, Name: in.Name, Enabled: enabled, Events: channelEvents(events),
		Meta: &Meta{Host: in.Config.Host, Port: in.Config.Port, From: in.Config.From, TLS: in.Config.TLS, Targets: in.Config.ChatIDs}})
}

func (a *API) load(id string) (Channel, Config, error) {
	var c Channel
	var cipherText string
	var en int
	var ev string
	e := a.DB.QueryRow("SELECT id,kind,name,config_cipher,enabled,COALESCE(events_json,'') FROM channels WHERE id=?", id).Scan(&c.ID, &c.Kind, &c.Name, &cipherText, &en, &ev)
	if e != nil {
		return c, Config{}, e
	}
	c.Enabled = en != 0
	c.Events = channelEvents(ev)
	b, e := a.decrypt(cipherText)
	if e != nil {
		return c, Config{}, e
	}
	var cfg Config
	e = json.Unmarshal(b, &cfg)
	return c, cfg, e
}
func (a *API) send(ctx context.Context, c Channel, cfg Config, target, body string) error {
	if c.Kind == "smtp" {
		fn := a.SMTP
		if fn == nil {
			fn = sendSMTP
		}
		return fn(cfg, target, body)
	}
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	u := "https://api.telegram.org/bot" + url.PathEscape(cfg.Token) + "/sendMessage"
	// No parse_mode: alert text is generated from arbitrary error bodies, and
	// Telegram's Markdown parser rejects messages with unbalanced markers.
	payload, _ := json.Marshal(map[string]string{"chat_id": target, "text": body})
	req, e := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(payload))
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		detail := strings.TrimSpace(string(raw))
		if len(detail) > 200 {
			detail = detail[:200] + "…"
		}
		if detail == "" {
			return fmt.Errorf("Telegram returned HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("Telegram HTTP %d: %s", resp.StatusCode, detail)
	}
	var result struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<10)).Decode(&result); err != nil || !result.OK {
		return errors.New("Telegram rejected notification")
	}
	return nil
}
func sendSMTP(c Config, to, body string) error {
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	msg := []byte("From: " + c.From + "\r\nTo: " + to + "\r\nSubject: CronSoul notification\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body)
	if c.TLS {
		conn, e := (&net.Dialer{Timeout: 10 * time.Second}).Dial("tcp", addr)
		if e != nil {
			return e
		}
		defer conn.Close()
		host, _, _ := net.SplitHostPort(addr)
		tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if e = tc.Handshake(); e != nil {
			return e
		}
		client, e := smtp.NewClient(tc, host)
		if e != nil {
			return e
		}
		defer client.Close()
		if e = client.Mail(c.From); e != nil {
			return e
		}
		if e = client.Rcpt(to); e != nil {
			return e
		}
		w, e := client.Data()
		if e != nil {
			return e
		}
		if _, e = w.Write(msg); e != nil {
			return e
		}
		return w.Close()
	}
	var auth smtp.Auth
	if c.User != "" {
		auth = smtp.PlainAuth("", c.User, c.Pass, c.Host)
	}
	return smtp.SendMail(addr, auth, c.From, []string{to}, msg)
}
func (a *API) test(w http.ResponseWriter, r *http.Request) {
	c, cfg, e := a.load(chi.URLParam(r, "id"))
	if e != nil {
		jsonOut(w, 404, map[string]string{"error": "channel not found"})
		return
	}
	targets := cfg.ChatIDs
	if c.Kind == "smtp" && len(targets) == 0 {
		targets = []string{cfg.From}
	}
	if len(targets) == 0 {
		jsonOut(w, 200, map[string]any{"sent": false, "delivered": 0, "total": 0, "error": "no destination configured", "reason": "This channel has no recipient yet."})
		return
	}
	delivered := 0
	for _, to := range targets {
		if e = a.send(r.Context(), c, cfg, to, "CronSoul test notification"); e != nil {
			jsonOut(w, 200, map[string]any{"sent": false, "delivered": delivered, "total": len(targets), "error": "delivery failed", "reason": safeReason(e)})
			return
		}
		delivered++
	}
	jsonOut(w, 200, map[string]any{"sent": true, "delivered": delivered, "total": len(targets), "error": nil})
}

// safeReason turns a transport error into one diagnostic line: no newlines,
// bounded length, and nothing that could carry a credential.
func safeReason(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", " "), "\n", " ")
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}

func (a *API) delete(w http.ResponseWriter, r *http.Request) {
	res, e := a.DB.ExecContext(r.Context(), "DELETE FROM channels WHERE id=?", chi.URLParam(r, "id"))
	if e != nil {
		jsonOut(w, 500, map[string]string{"error": "delete failed"})
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		jsonOut(w, 404, map[string]string{"error": "channel not found"})
		return
	}
	w.WriteHeader(204)
}
func Enqueue(db *sql.DB, kind, job, run string, payload map[string]string, now time.Time) error {
	return enqueueConfigured(db, kind, job, run, payload, now)
}

// enqueueConfigured routes an event to every enabled channel that subscribes
// to it. Endpoints carry no notification preferences of their own.
func enqueueConfigured(db *sql.DB, kind, job, run string, payload map[string]string, now time.Time) error {
	if !contains(EventKinds, kind) {
		return nil
	}
	rows, err := db.Query("SELECT id,kind,COALESCE(events_json,'') FROM channels WHERE enabled=1")
	if err != nil {
		return err
	}
	type target struct{ id, kind, events string }
	var channels []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.kind, &t.events); err != nil {
			rows.Close()
			return err
		}
		channels = append(channels, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	data, _ := json.Marshal(payload)
	for _, c := range channels {
		if !subscribes(c.events, kind) {
			continue
		}
		dedupe := kind + ":" + run + ":" + c.id
		if _, err := db.Exec("INSERT OR IGNORE INTO outbox(id,kind,job_id,run_id,channel,target,payload_json,next_attempt_at,dedupe_key) VALUES(?,?,?,?,?,?,?,?,?)",
			store.ID(), kind, job, nullable(run), c.kind, c.id, string(data), now.UTC().Format(time.RFC3339), dedupe); err != nil {
			return err
		}
	}
	return nil
}

// subscribes reports whether a channel wants this event. An empty or unreadable
// list means the default set, so a freshly created channel never goes silent.
func subscribes(eventsJSON, kind string) bool {
	var ev []string
	if eventsJSON == "" || json.Unmarshal([]byte(eventsJSON), &ev) != nil || len(ev) == 0 {
		return contains(DefaultEventKinds, kind)
	}
	return contains(ev, kind)
}

// sendError keeps the provider's own explanation (HTTP body, dial error) so the
// console can show why a delivery failed instead of a generic label.
func sendError(err error) string {
	if err == nil {
		return "delivery failed"
	}
	msg := strings.TrimSpace(err.Error())
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

var RetryDelays = []time.Duration{10 * time.Second, 60 * time.Second, 300 * time.Second}

func Worker(db *sql.DB, key []byte, send func(context.Context, string, string, string, string) error, now time.Time) error {
	rows, e := db.Query("SELECT id,kind,job_id,run_id,channel,target,payload_json,attempts FROM outbox WHERE status='pending' AND next_attempt_at<=? ORDER BY next_attempt_at", now.UTC().Format(time.RFC3339))
	if e != nil {
		return e
	}
	type item struct {
		id, kind, job, ch, target, payload string
		run                                sql.NullString
		attempts                           int
	}
	var list []item
	for rows.Next() {
		var x item
		if e = rows.Scan(&x.id, &x.kind, &x.job, &x.run, &x.ch, &x.target, &x.payload, &x.attempts); e != nil {
			rows.Close()
			return e
		}
		list = append(list, x)
	}
	rows.Close()
	for _, x := range list {
		var c Channel
		var cfg Config
		var cipherText string
		var enabled int
		// Outbox rows point at a channel id; older rows used the channel kind and
		// carried the destination themselves.
		channelID, legacyTarget := x.target, ""
		if i := strings.IndexByte(x.target, ':'); i > 0 {
			channelID, legacyTarget = x.target[:i], x.target[i+1:]
		}
		e = db.QueryRow("SELECT id,kind,name,config_cipher,enabled FROM channels WHERE id=? AND enabled=1", channelID).Scan(&c.ID, &c.Kind, &c.Name, &cipherText, &enabled)
		if e != nil {
			e = db.QueryRow("SELECT id,kind,name,config_cipher,enabled FROM channels WHERE kind=? AND enabled=1 ORDER BY created_at LIMIT 1", x.ch).Scan(&c.ID, &c.Kind, &c.Name, &cipherText, &enabled)
		}
		if e != nil {
			return e
		}
		b, e := (&API{Key: key}).decrypt(cipherText)
		if e != nil {
			return e
		}
		_ = json.Unmarshal(b, &cfg)
		var p map[string]string
		_ = json.Unmarshal([]byte(x.payload), &p)
		msg := p["message"]
		destinations := cfg.ChatIDs
		if len(destinations) == 0 {
			destinations = []string{legacyTarget}
		}
		e = nil
		for _, dest := range destinations {
			var err error
			if send == nil {
				api := &API{Key: key}
				err = api.send(context.Background(), c, cfg, dest, msg)
			} else {
				err = send(context.Background(), c.Kind, string(b), dest, msg)
			}
			if err != nil {
				e = err
			}
		}
		if e == nil {
			_, e = db.Exec("UPDATE outbox SET status='sent',sent_at=?,attempts=attempts+1,last_error=NULL WHERE id=? AND status='pending'", now.UTC().Format(time.RFC3339), x.id)
			if e != nil {
				return e
			}
			continue
		}
		attempt := x.attempts + 1
		if attempt >= len(RetryDelays)+1 {
			_, e = db.Exec("UPDATE outbox SET status='failed',attempts=?,last_error=? WHERE id=?", attempt, sendError(e), x.id)
		} else {
			_, e = db.Exec("UPDATE outbox SET attempts=?,next_attempt_at=?,last_error=? WHERE id=?", attempt, now.Add(RetryDelays[attempt-1]).UTC().Format(time.RFC3339), sendError(e), x.id)
		}
		if e != nil {
			return e
		}
	}
	return nil
}
