package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/user4xn/cronsoul/internal/store"
)

func notificationDB(t *testing.T) *sql.DB {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err = s.Migrate(); err != nil {
		t.Fatal(err)
	}
	return s.DB
}

func notificationJob(t *testing.T, db *sql.DB, notifyOn, emails, chats string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO jobs(id,key,name,service,method,url,cron,notify_on_json,notify_email_json,notify_telegram_json) VALUES('j','j','job','svc','GET','https://example.test','* * * * *',?,?,?)`, notifyOn, emails, chats)
	if err != nil {
		t.Fatal(err)
	}
}

func notificationChannel(t *testing.T, db *sql.DB, id, kind string, events []string, cipher string) {
	t.Helper()
	ev, err := encodeEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO channels(id,kind,name,config_cipher,events_json) VALUES(?,?,?,?,?)`, id, kind, id, cipher, ev); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueueRoutesConfiguredChannelsAndDeduplicates(t *testing.T) {
	db := notificationDB(t)
	notificationJob(t, db, `["failure"]`, `["a@example.test"]`, `["42"]`)
	notificationChannel(t, db, "c1", "smtp", []string{"failure"}, "cipher")
	notificationChannel(t, db, "c2", "telegram", []string{"failure"}, "cipher")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for range 2 {
		if err := Enqueue(db, "failure", "j", "", map[string]string{"message": "failed"}, now); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("outbox rows=%d, want 2 (one per channel)", count)
	}
	var channels string
	rows, err := db.Query(`SELECT channel FROM outbox ORDER BY channel`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		channels += c + " "
	}
	if strings.TrimSpace(channels) != "smtp telegram" {
		t.Fatalf("channels=%q", channels)
	}
}

func TestEnqueueDoesNotRouteUnconfiguredEvent(t *testing.T) {
	db := notificationDB(t)
	notificationJob(t, db, `["failure"]`, `["a@example.test"]`, `[]`)
	notificationChannel(t, db, "c1", "smtp", []string{"failure"}, "cipher")
	for _, kind := range []string{"recovered", "missed_run", "assertion_failed"} {
		if err := Enqueue(db, kind, "j", "r", nil, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("outbox rows=%d, want 0 for unsubscribed events", count)
	}
}

func TestWorkerRetriesAndStopsAfterMaximumAttempts(t *testing.T) {
	db := notificationDB(t)
	notificationJob(t, db, `["failure"]`, `["a@example.test"]`, `[]`)
	enc, err := (&API{Key: []byte("01234567890123456789012345678901")}).encrypt([]byte(`{"from":"a@example.test"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO channels(id,kind,name,config_cipher) VALUES('c','smtp','smtp',?)`, enc); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := Enqueue(db, "failure", "j", "", map[string]string{"message": "x"}, now); err != nil {
		t.Fatal(err)
	}
	send := func(context.Context, string, string, string, string) error { return context.DeadlineExceeded }
	for i, delay := range RetryDelays {
		if err := Worker(db, []byte("01234567890123456789012345678901"), send, now); err != nil {
			t.Fatal(err)
		}
		var attempts int
		var next, status string
		if err := db.QueryRow(`SELECT attempts,next_attempt_at,status FROM outbox`).Scan(&attempts, &next, &status); err != nil {
			t.Fatal(err)
		}
		if attempts != i+1 {
			t.Fatalf("attempt=%d want %d", attempts, i+1)
		}
		if status != "pending" {
			t.Fatalf("status=%s after retry %d", status, i+1)
		}
		expected := now.Add(delay).Format(time.RFC3339)
		if next != expected {
			t.Fatalf("next=%s want %s", next, expected)
		}
		now = now.Add(delay)
	}
	if err := Worker(db, []byte("01234567890123456789012345678901"), send, now); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var status string
	if err := db.QueryRow(`SELECT attempts,status FROM outbox`).Scan(&attempts, &status); err != nil {
		t.Fatal(err)
	}
	if attempts != 4 || status != "failed" {
		t.Fatalf("attempts=%d status=%s", attempts, status)
	}
}

func TestChannelSecretsAreEncryptedAndNotReturned(t *testing.T) {
	db := notificationDB(t)
	key := []byte("01234567890123456789012345678901")
	a := &API{DB: db, Key: key}
	body := `{"kind":"telegram","name":"ops","config":{"bot_token":"top-secret-token","chat_ids":["7"]}}`
	r := httptest.NewRequest(http.MethodPost, "/channels", strings.NewReader(body))
	w := httptest.NewRecorder()
	a.create(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "top-secret-token") {
		t.Fatal("secret leaked in create response")
	}
	var cipherText string
	if err := db.QueryRow(`SELECT config_cipher FROM channels`).Scan(&cipherText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cipherText, "top-secret-token") {
		t.Fatal("secret stored in plaintext")
	}
	plain, err := a.decrypt(cipherText)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err = json.Unmarshal(plain, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "top-secret-token" {
		t.Fatal("encrypted secret did not round-trip")
	}
}

func TestSMTPAndTelegramSendUseInjectedTransports(t *testing.T) {
	gotSMTP := false
	a := &API{SMTP: func(c Config, to, body string) error {
		gotSMTP = c.User == "u" && to == "a@example.test" && body == "CronSoul test notification"
		return nil
	}}
	if err := a.send(context.Background(), Channel{Kind: "smtp"}, Config{User: "u"}, "a@example.test", "CronSoul test notification"); err != nil {
		t.Fatal(err)
	}
	if !gotSMTP {
		t.Fatal("SMTP transport was not called with expected values")
	}
	gotTelegram := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTelegram = strings.Contains(r.URL.Path, "botfake-token") && r.URL.Path == "/botfake-token/sendMessage"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	client := srv.Client()
	// Rewrite only the Telegram endpoint in the injected test transport.
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	a = &API{Client: client}
	if err := a.send(context.Background(), Channel{Kind: "telegram"}, Config{Token: "fake-token"}, "42", "hello"); err != nil {
		t.Fatal(err)
	}
	if !gotTelegram {
		t.Fatal("Telegram transport did not receive sendMessage")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEnqueueIgnoresJobNotificationPreferences(t *testing.T) {
	db := notificationDB(t)
	// The job asks only for "recovered"; channel subscriptions decide instead.
	notificationJob(t, db, `["recovered"]`, `["ops@example.test"]`, `["-10042"]`)
	notificationChannel(t, db, "c1", "smtp", []string{"failure"}, "cipher")
	notificationChannel(t, db, "c2", "telegram", []string{"failure", "missed_run"}, "cipher")
	if _, err := db.Exec(`INSERT INTO runs(id,job_id,trigger,status) VALUES('r1','j','manual','failed')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if err := Enqueue(db, "failure", "j", "r1", map[string]string{"message": "failure"}, now); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM outbox WHERE run_id='r1'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rows=%d want 2 subscribed channels", n)
	}
	if err := Enqueue(db, "missed_run", "j", "", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM outbox WHERE kind='missed_run'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("missed_run rows=%d want 1 (only the telegram channel)", n)
	}
}

func TestWorkerDeliverSuccessAndRetrySchedule(t *testing.T) {
	db := notificationDB(t)
	notificationJob(t, db, `["failure"]`, `["ops@example.test"]`, `[]`)
	if _, err := db.Exec(`INSERT INTO runs(id,job_id,trigger,status) VALUES('r1','j','manual','failed')`); err != nil {
		t.Fatal(err)
	}
	key := []byte("01234567890123456789012345678901")
	enc, err := (&API{Key: key}).encrypt([]byte(`{"host":"smtp.test","port":25,"from":"from@example.test","chat_ids":["ops@example.test"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO channels(id,kind,name,config_cipher) VALUES('c','smtp','smtp',?)`, enc); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err = Enqueue(db, "failure", "j", "r1", map[string]string{"message": "oops"}, now); err != nil {
		t.Fatal(err)
	}
	calls := 0
	send := func(_ context.Context, ch, cfg, target, msg string) error {
		calls++
		if ch != "smtp" || target != "ops@example.test" || msg != "oops" || !strings.Contains(cfg, "smtp.test") {
			t.Fatalf("bad delivery: %s %s %s", ch, target, msg)
		}
		if calls < 2 {
			return context.DeadlineExceeded
		}
		return nil
	}
	if err = Worker(db, key, send, now); err != nil {
		t.Fatal(err)
	}
	var attempts int
	var next string
	var status string
	if err = db.QueryRow(`SELECT attempts,next_attempt_at,status FROM outbox`).Scan(&attempts, &next, &status); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || status != "pending" || next != now.Add(10*time.Second).Format(time.RFC3339) {
		t.Fatalf("first failure: %d %s %s", attempts, status, next)
	}
	if err = Worker(db, key, send, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT attempts,status FROM outbox`).Scan(&attempts, &status); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || status != "sent" || calls != 2 {
		t.Fatalf("second send: %d %s calls=%d", attempts, status, calls)
	}
}

func TestChannelTestSendReportsReason(t *testing.T) {
	db := notificationDB(t)
	key := []byte("01234567890123456789012345678901")
	a := &API{DB: db, Key: key, SMTP: func(Config, string, string) error {
		return errors.New("dial tcp 10.0.0.1:25: connection refused")
	}}
	enc, err := a.encrypt([]byte(`{"host":"smtp.test","port":25,"from":"a@example.test","chat_ids":["a@example.test"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO channels(id,kind,name,config_cipher) VALUES('c','smtp','smtp',?)`, enc); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/channels/c/test", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "c")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	a.test(w, r)
	var got struct {
		Sent      bool   `json:"sent"`
		Delivered int    `json:"delivered"`
		Total     int    `json:"total"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body=%s err=%v", w.Body, err)
	}
	if got.Sent || got.Delivered != 0 || got.Total != 1 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if !strings.Contains(got.Reason, "connection refused") {
		t.Fatalf("reason not surfaced: %q", got.Reason)
	}
	if strings.Contains(got.Reason, "\n") {
		t.Fatal("reason must be a single line")
	}
}

func TestTelegramSendsPlainTextSoCronAsterisksDoNotBreakParsing(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	client := srv.Client()
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	a := &API{Client: client}
	body := "Schedule: * */6 * * * · Asia/Jakarta\nResult: FAILED · HTTP 404"
	if err := a.send(context.Background(), Channel{Kind: "telegram"}, Config{Token: "t"}, "42", body); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["parse_mode"]; ok {
		t.Fatalf("parse_mode must not be set: %v", payload)
	}
	if payload["text"] != body {
		t.Fatalf("text altered: %v", payload["text"])
	}
}

func TestTelegramErrorCarriesProviderReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: can't parse entities"}`))
	}))
	defer srv.Close()
	client := srv.Client()
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	a := &API{Client: client}
	err := a.send(context.Background(), Channel{Kind: "telegram"}, Config{Token: "t"}, "42", "* * *")
	if err == nil || !strings.Contains(err.Error(), "can't parse entities") {
		t.Fatalf("error should carry the provider reason, got %v", err)
	}
}
