package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/auth"
	"github.com/user4xn/cronsoul/internal/config"
	"github.com/user4xn/cronsoul/internal/jobs"
	"github.com/user4xn/cronsoul/internal/notify"
	"github.com/user4xn/cronsoul/internal/ops"
	"github.com/user4xn/cronsoul/internal/runner"
	"github.com/user4xn/cronsoul/internal/scheduler"
	"github.com/user4xn/cronsoul/internal/store"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

var workersRunning atomic.Int32

// buildVersion is stamped at build time (see Makefile) so operators can tell
// which bundle a browser is actually running.
var buildVersion = "dev"

// displayTZDefault comes from CRONSOUL_DISPLAY_TZ; Settings can override it.
var displayTZDefault = "Asia/Jakarta"

//go:embed ui
var frontend embed.FS

func combinedAPI(api *runner.API, db *sql.DB, a *auth.Service) http.Handler {
	mux := chi.NewRouter()
	// Runner and channels share the /api/v1 mux; avoid mounting multiple handlers at "/".
	runner.RegisterRoutes(mux, api, a)
	notify.RegisterRoutes(mux, db, a, api.SecretKey)
	ops.RegisterRoutes(mux, db, a, api.Env)
	return mux
}

func newRouter(a *auth.Service, db *sql.DB, args ...any) chi.Router {
	var api *runner.API
	if len(args) > 0 {
		api, _ = args[0].(*runner.API)
		if api == nil {
			if secret, ok := args[0].(string); ok {
				api = &runner.API{DB: db, SecretKey: []byte(secret)}
			}
		}
	}
	if api == nil {
		api = &runner.API{DB: db}
	}
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		dbStatus, code, status := "ok", http.StatusOK, "ok"
		if err := db.Ping(); err != nil {
			dbStatus, code, status = "error", http.StatusServiceUnavailable, "degraded"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": status, "version": "0.1.0", "db": dbStatus, "env": api.Env,
			"workers": workersRunning.Load(),
		})
	})
	r.Mount("/api/v1/auth", a.Router())
	var patterns []string
	if rows, err := db.Query("SELECT pattern FROM allowlist"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var p string
			if rows.Scan(&p) == nil {
				patterns = append(patterns, p)
			}
		}
	}
	api.Egress.Allowlist = patterns
	api.Policy = func() runner.EgressConfig {
		cfg := api.Egress
		var insecure, until, private string
		_ = db.QueryRow("SELECT value FROM settings WHERE key='allow_insecure_targets'").Scan(&insecure)
		_ = db.QueryRow("SELECT value FROM settings WHERE key='allow_insecure_targets_until'").Scan(&until)
		_ = db.QueryRow("SELECT value FROM settings WHERE key='allow_private_egress'").Scan(&private)
		rows, err := db.Query("SELECT pattern FROM allowlist ORDER BY pattern")
		if err == nil {
			defer rows.Close()
			cfg.Allowlist = nil
			for rows.Next() {
				var p string
				if rows.Scan(&p) == nil {
					cfg.Allowlist = append(cfg.Allowlist, p)
				}
			}
		}
		cfg.AllowPrivate = strings.EqualFold(private, "true")
		cfg.AllowInsecure = strings.EqualFold(insecure, "true")
		cfg.InsecureUntil, _ = time.Parse(time.RFC3339, until)
		return cfg
	}
	r.Mount("/api/v1", combinedAPI(api, db, a))
	// Play lives under the jobs subtree, which is mounted separately; passing the
	// handler here keeps POST /api/v1/jobs/{id}/run reachable.
	r.Mount("/api/v1/jobs", jobs.New(db, a, api.SecretKey, api.RunHandler(), api.PreviewHandler()))
	assets, _ := fs.Sub(frontend, "ui")
	// The SPA references branded assets under /ui/* while the embedded FS is rooted at ui/.
	uiFiles := http.FileServer(http.FS(assets))
	// A new binary ships a new bundle, but browsers happily keep an old app.js.
	// Force revalidation so a normal reload always picks up the deployed UI.
	noStale := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			next.ServeHTTP(w, r)
		})
	}
	r.Get("/time.json", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		// The console renders every timestamp in one display timezone, chosen in
		// Settings; the database wins over the startup default.
		tzName := displayTZDefault
		var stored string
		if err := db.QueryRow("SELECT value FROM settings WHERE key='display.timezone'").Scan(&stored); err == nil && stored != "" {
			tzName = stored
		}
		loc, err := time.LoadLocation(tzName)
		if err != nil {
			loc, tzName = time.UTC, "UTC"
		}
		local := now.In(loc)
		_, offset := local.Zone()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"utc":"` + now.UTC().Format(time.RFC3339) + `","unix_ms":` +
			strconv.FormatInt(now.UnixMilli(), 10) + `,"zone":"` + local.Format("MST") +
			`","tz":"` + tzName + `","offset_min":` + strconv.Itoa(offset/60) + `}`))
	})
	r.Get("/build.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(`{"version":"` + buildVersion + `"}`))
	})
	r.Handle("/ui/*", noStale(http.StripPrefix("/ui/", uiFiles)))
	r.Handle("/*", noStale(uiFiles))
	return r
}

// offsetMinutes reports the server's UTC offset so the console can render the
// clock in the server's own wall time rather than the browser's.
func offsetMinutes(t time.Time) int {
	_, secs := t.Zone()
	return secs / 60
}

// lockDatabase takes an exclusive lock beside the SQLite file so a second
// instance cannot silently share the queue. Memory databases are exempt.
func lockDatabase(path string) (*os.File, error) {
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file::memory:") {
		return nil, nil
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another CronSoul instance is already using %s", path)
	}
	return f, nil
}

func main() {
	cfg, e := config.Load()
	if e != nil {
		log.Fatalf("startup validation failed: %v", e)
	}
	displayTZDefault = cfg.DisplayTZ
	// Two processes on one database would both drain the run queue and the
	// outbox, silently stealing each other's work. Refuse to start.
	if lock, e := lockDatabase(cfg.DBPath); e != nil {
		log.Fatalf("database lock: %v", e)
	} else if lock != nil {
		defer lock.Close()
	}
	if cfg.GeneratedSecretKey {
		log.Printf("generated a new secret key at %s — back it up with the database", cfg.SecretKeyFile)
	}
	if cfg.DevSecretKey {
		log.Print("dev mode: using a derived secret key — set CRONSOUL_SECRET_KEY before storing anything real")
	}
	st, e := store.Open(cfg.DBPath)
	if e != nil {
		log.Fatal(e)
	}
	defer st.Close()
	if e = st.Migrate(); e != nil {
		log.Fatal(e)
	}
	var count int
	st.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count == 0 {
		if e = st.Seed(); e != nil {
			log.Fatal(e)
		}
		log.Print("superadmin dibuat, wajib ganti password saat login pertama")
	}
	a := &auth.Service{DB: st.DB, TTL: cfg.SessionTTL, Secure: cfg.SecureCookie}
	api := &runner.API{DB: st.DB, SecretKey: []byte(cfg.SecretKey), Env: cfg.Env}
	r := newRouter(a, st.DB, api)
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer func() { workersRunning.Store(0); stopWorkers() }()
	sch := scheduler.Scheduler{DB: st.DB}
	workersRunning.Store(1)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if err := sch.Tick(); err != nil {
					log.Printf("scheduler: %v", err)
				}
				if err := sch.WatchMissed(); err != nil {
					log.Printf("missed-run watcher: %v", err)
				}
				if err := notify.Worker(st.DB, api.SecretKey, nil, time.Now().UTC()); err != nil {
					log.Printf("notification worker: %v", err)
				}
				if err := api.ProcessQueued(workerCtx); err != nil {
					log.Printf("runner: %v", err)
				}
			}
		}
	}()
	srv := &http.Server{Addr: cfg.Listen, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if cfg.TLSMode == "cert" {
			e = srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			e = srv.ListenAndServe()
		}
		if e != nil && e != http.ErrServerClosed {
			log.Print(e)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	stopWorkers()
}
