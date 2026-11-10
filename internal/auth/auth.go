package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/store"
)

type Service struct {
	DB      *sql.DB
	TTL     time.Duration
	Secure  bool
	limiter loginLimiter
}
type user struct {
	ID, Username, Role string
	Must               bool
}
type loginAttempt struct {
	window   time.Time
	failures int
}
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func (l *loginLimiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.attempts == nil {
		l.attempts = make(map[string]loginAttempt)
	}
	a, ok := l.attempts[key]
	if !ok || now.Sub(a.window) >= time.Minute {
		delete(l.attempts, key)
		return false
	}
	return a.failures >= 5
}
func (l *loginLimiter) failure(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.attempts == nil {
		l.attempts = make(map[string]loginAttempt)
	}
	a := l.attempts[key]
	if a.window.IsZero() || now.Sub(a.window) >= time.Minute {
		a = loginAttempt{window: now}
	}
	a.failures++
	l.attempts[key] = a
}
func (l *loginLimiter) success(key string) { l.mu.Lock(); delete(l.attempts, key); l.mu.Unlock() }

func (s *Service) Router() chi.Router {
	r := chi.NewRouter()
	r.Post("/login", s.login)
	r.With(s.require).Get("/me", s.me)
	r.With(s.require).Post("/logout", s.logout)
	r.With(s.require).Post("/change-password", s.change)
	return r
}
func jsonw(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	key, now := clientIP(r)+"\x00"+in.Username, time.Now()
	if s.limiter.blocked(key, now) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		return
	}
	var u user
	var h string
	var disabled int
	e := s.DB.QueryRow("SELECT id,username,role,must_change_password,password_hash,disabled FROM users WHERE username=?", in.Username).Scan(&u.ID, &u.Username, &u.Role, &u.Must, &h, &disabled)
	if e != nil || disabled != 0 || !store.VerifyPassword(h, in.Password) {
		s.limiter.failure(key, now)
		http.Error(w, "invalid credentials", 401)
		return
	}
	s.limiter.success(key)
	raw := make([]byte, 32)
	if _, e = rand.Read(raw); e != nil {
		http.Error(w, "internal error", 500)
		return
	}
	sum := sha256.Sum256(raw)
	exp := time.Now().Add(s.TTL)
	_, e = s.DB.Exec("INSERT INTO sessions(id,user_id,token_hash,ip,user_agent,expires_at) VALUES(?,?,?,?,?,?)", store.ID(), u.ID, hex.EncodeToString(sum[:]), r.RemoteAddr, r.UserAgent(), exp.UTC().Format(time.RFC3339))
	if e != nil {
		http.Error(w, "internal error", 500)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "cs_session", Value: hex.EncodeToString(raw), Path: "/", HttpOnly: true, Secure: s.Secure, SameSite: http.SameSiteLaxMode, Expires: exp, MaxAge: int(s.TTL.Seconds())})
	jsonw(w, map[string]any{"username": u.Username, "role": u.Role, "must_change_password": u.Must})
}
func (s *Service) current(r *http.Request) (user, bool) {
	c, e := r.Cookie("cs_session")
	if e != nil {
		return user{}, false
	}
	raw, e := hex.DecodeString(c.Value)
	if e != nil {
		return user{}, false
	}
	sum := sha256.Sum256(raw)
	var u user
	var revoked sql.NullString
	e = s.DB.QueryRow("SELECT u.id,u.username,u.role,u.must_change_password,s.revoked_at FROM sessions s JOIN users u ON u.id=s.user_id WHERE s.token_hash=? AND s.expires_at>? AND u.disabled=0", hex.EncodeToString(sum[:]), time.Now().UTC().Format(time.RFC3339)).Scan(&u.ID, &u.Username, &u.Role, &u.Must, &revoked)
	return u, e == nil && !revoked.Valid
}
func (s *Service) require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.current(r)
		if !ok {
			http.Error(w, "unauthorized", 401)
			return
		}
		if u.Must && !strings.HasSuffix(r.URL.Path, "/change-password") && !strings.HasSuffix(r.URL.Path, "/logout") {
			http.Error(w, "password change required", 428)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u)))
	})
}

type ctxKey struct{}

func withUser(c context.Context, u user) context.Context { return context.WithValue(c, ctxKey{}, u) }

// UserFromContext returns the authenticated session identity installed by require.
func UserFromContext(c context.Context) (username, id string, ok bool) {
	u, ok := c.Value(ctxKey{}).(user)
	if !ok {
		return "", "", false
	}
	return u.Username, u.ID, true
}
func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	u, _ := s.current(r)
	jsonw(w, map[string]any{"username": u.Username, "role": u.Role, "must_change_password": u.Must})
}
func (s *Service) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("cs_session"); e == nil {
		if raw, e := hex.DecodeString(c.Value); e == nil {
			sum := sha256.Sum256(raw)
			_, _ = s.DB.Exec("UPDATE sessions SET revoked_at=? WHERE token_hash=?", time.Now().UTC().Format(time.RFC3339), hex.EncodeToString(sum[:]))
		}
	}
	w.WriteHeader(204)
}
func (s *Service) change(w http.ResponseWriter, r *http.Request) {
	u, ok := s.current(r)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		LegacyCurrent   string `json:"CurrentPassword"`
		LegacyNew       string `json:"NewPassword"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "weak password", 400)
		return
	}
	if in.CurrentPassword == "" {
		in.CurrentPassword = in.LegacyCurrent
	}
	if in.NewPassword == "" {
		in.NewPassword = in.LegacyNew
	}
	if len(in.NewPassword) < 10 || in.NewPassword == "12345678" {
		http.Error(w, "weak password", 400)
		return
	}
	var h string
	if s.DB.QueryRow("SELECT password_hash FROM users WHERE id=?", u.ID).Scan(&h) != nil {
		http.Error(w, "invalid credentials", 401)
		return
	}
	// A forced change happens right after logging in with the temporary
	// password, so asking for it again adds nothing. Voluntary changes still do.
	if !u.Must && !store.VerifyPassword(h, in.CurrentPassword) {
		http.Error(w, "invalid credentials", 401)
		return
	}
	if store.VerifyPassword(h, in.NewPassword) {
		http.Error(w, "new password must differ from the current one", 400)
		return
	}
	nh, e := store.HashPassword(in.NewPassword)
	if e != nil {
		http.Error(w, "internal error", 500)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, e = s.DB.Exec("UPDATE users SET password_hash=?,must_change_password=0,updated_at=? WHERE id=?", nh, now, u.ID); e != nil {
		http.Error(w, "internal error", 500)
		return
	}
	c, _ := r.Cookie("cs_session")
	raw, _ := hex.DecodeString(c.Value)
	sum := sha256.Sum256(raw)
	if _, e = s.DB.Exec("UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL AND token_hash<>?", now, u.ID, hex.EncodeToString(sum[:])); e != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.WriteHeader(204)
}
func RoleAtLeast(role, need string) bool {
	rank := map[string]int{"viewer": 1, "qa": 2, "admin": 3, "superadmin": 4}
	n, ok := rank[need]
	return ok && rank[role] >= n
}
func RequireRole(s *Service, need string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return s.require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, _ := s.current(r)
			if !RoleAtLeast(u.Role, need) {
				http.Error(w, "forbidden", 403)
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}
