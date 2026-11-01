package ops

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user4xn/cronsoul/internal/store"
)

func opsDB(t *testing.T) *sql.DB {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.DB
}

func TestReadOnlyOperationalShapes(t *testing.T) {
	db := opsDB(t)
	if _, err := db.Exec(`INSERT INTO jobs(id,key,name,service,method,url,cron) VALUES('j','job-j','Job J','svc','GET','https://example.test','0 * * * *')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO alerts(id,job_id,window_start,kind) VALUES('a','j','2026-01-01T00:00:00Z','missed_run')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO outbox(id,kind,channel,payload_json,next_attempt_at) VALUES('o','missed_run','smtp','{}','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	api := &API{DB: db}
	for name, handler := range map[string]http.HandlerFunc{"alerts": api.alerts, "outbox": api.outbox, "settings": api.settings} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest("GET", "/?status=pending", nil))
			if w.Code != 200 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if _, ok := body["data"]; !ok {
				t.Fatalf("missing data envelope: %s", w.Body)
			}
		})
	}
}

func TestOutboxRejectsInvalidStatus(t *testing.T) {
	api := &API{DB: opsDB(t)}
	w := httptest.NewRecorder()
	api.outbox(w, httptest.NewRequest("GET", "/?status=bogus", nil))
	if w.Code != 400 {
		t.Fatalf("status=%d", w.Code)
	}
}
