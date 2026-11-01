// Contoh penerima panggilan CronSoul.
//
// Ini BUKAN bagian dari panel — ini contoh untuk service yang mau dipanggil
// oleh CronSoul. Salin middleware di bawah ke service Anda, atau pakai file ini
// sebagai acuan perilaku yang benar.
//
// Jalankan:
//
//	CRONSOUL_RECEIVER_TOKEN=rahasia-panjang go run ./examples/receiver
//	curl -X POST "http://localhost:9000/jobs/sync-poc" \
//	     -H "X-CronSoul-Token: rahasia-panjang" \
//	     -H "X-Run-Id: 01J9ZK4M2Q" \
//	     -H "X-Simulated-Time: 2026-11-30T23:59:00+07:00"
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---------------------------------------------------------------------------
// 1. Header yang dikirim CronSoul
// ---------------------------------------------------------------------------
const (
	hdrToken       = "X-CronSoul-Token"
	hdrRunID       = "X-Run-Id"
	hdrIdempotency = "Idempotency-Key"
	hdrTrigger     = "X-Trigger"      // manual | schedule | retry | replay
	hdrTriggeredBy = "X-Triggered-By" // email pengguna, kosong untuk jadwal
	hdrSimulated   = "X-Simulated-Time"
)

// ---------------------------------------------------------------------------
// 2. Middleware keamanan: verifikasi token
// ---------------------------------------------------------------------------
func requireToken(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get(hdrToken)
			if subtle.ConstantTimeCompare([]byte(got), []byte(secret)) != 1 {
				writeError(w, http.StatusUnauthorized, "token tidak valid")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
//  3. Middleware idempotensi
//     Retry CronSoul memakai X-Run-Id dan Idempotency-Key yang SAMA.
//     Artinya: percobaan kedua tidak boleh mengulang efek samping.
//
// ---------------------------------------------------------------------------
type idempotencyStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

func newIdempotencyStore(ttl time.Duration) *idempotencyStore {
	s := &idempotencyStore{seen: map[string]time.Time{}, ttl: ttl}
	go func() {
		for range time.Tick(10 * time.Minute) {
			s.mu.Lock()
			for k, t := range s.seen {
				if time.Since(t) > s.ttl {
					delete(s.seen, k)
				}
			}
			s.mu.Unlock()
		}
	}()
	return s
}

// FirstTime mengembalikan false bila key sudah pernah diproses.
func (s *idempotencyStore) FirstTime(key string) bool {
	if key == "" {
		return true // tanpa key: tidak bisa dedupe, biarkan jalan
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[key]; ok {
		return false
	}
	s.seen[key] = time.Now()
	return true
}

func withIdempotency(store *idempotencyStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(hdrIdempotency)
			if key == "" {
				key = r.Header.Get(hdrRunID)
			}
			if !store.FirstTime(key) {
				writeJSON(w, http.StatusOK, envelope{
					Status:  "success",
					Message: "duplikat diabaikan (idempotent)",
					Data:    map[string]any{"summary": map[string]any{"skipped": 1}},
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ---------------------------------------------------------------------------
// 4. Waktu: pakai simulasi bila dikirim CronSoul
// ---------------------------------------------------------------------------
const ctxKeyNow = "now"

func withSimulatedTime(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		if raw := r.Header.Get(hdrSimulated); raw != "" {
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				now = t
			} else {
				writeError(w, http.StatusBadRequest, "X-Simulated-Time bukan RFC3339")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyNow, now)))
	})
}

func nowFrom(r *http.Request) time.Time {
	if t, ok := r.Context().Value(ctxKeyNow).(time.Time); ok {
		return t
	}
	return time.Now()
}

// ---------------------------------------------------------------------------
// 5. Handler contoh
// ---------------------------------------------------------------------------
type envelope struct {
	Status  string         `json:"status"`
	Message string         `json:"message,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

func syncPOCHandler(w http.ResponseWriter, r *http.Request) {
	now := nowFrom(r)
	dryRun := r.URL.Query().Get("dry_run") == "true" || r.Header.Get("X-Dry-Run") == "true"

	// Inilah yang membuat uji akhir bulan mungkin: tanggal diambil dari context,
	// bukan dari time.Now() langsung.
	day := now.Format("2006-01-02")

	if dryRun {
		writeJSON(w, http.StatusOK, envelope{
			Status:  "success",
			Message: "dry-run, tidak ada perubahan data",
			Data: map[string]any{
				"summary":        map[string]any{"would_process": 42},
				"simulated_time": now.Format(time.RFC3339),
			},
		})
		return
	}

	// ... pekerjaan nyata di sini, memakai `day` dan `now` ...

	writeJSON(w, http.StatusOK, envelope{
		Status:  "success",
		Message: "sync selesai",
		Data: map[string]any{
			"summary": map[string]any{
				"processed": 42,
				"skipped":   0,
				"date":      day,
			},
			// Balasan ini dipakai panel untuk memastikan simulasi benar-benar dipakai.
			"simulated_time": now.Format(time.RFC3339),
		},
	})
}

// ---------------------------------------------------------------------------
// 6. Utilitas
// ---------------------------------------------------------------------------
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, envelope{Status: "failed", Message: msg})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic: %v", rec)
				writeError(w, http.StatusInternalServerError, "panic ditangkap")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
//  7. Wiring
//     Dengan chi: r.Use(requireToken(secret), withIdempotency(store), withSimulatedTime)
//
// ---------------------------------------------------------------------------
func main() {
	secret := os.Getenv("CRONSOUL_RECEIVER_TOKEN")
	if len(secret) < 16 {
		log.Fatal("CRONSOUL_RECEIVER_TOKEN wajib diisi (minimal 16 karakter)")
	}

	store := newIdempotencyStore(24 * time.Hour)

	mux := http.NewServeMux()
	mux.Handle("POST /jobs/sync-poc", recoverer(requireToken(secret)(withIdempotency(store)(withSimulatedTime(http.HandlerFunc(syncPOCHandler))))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, envelope{Status: "ok"})
	})

	srv := &http.Server{
		Addr:              ":9000",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
	}

	go func() {
		log.Printf("receiver mendengarkan di %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("berhenti dengan rapi")
}

var _ = strings.TrimSpace // dipakai bila menambah parsing header lain
