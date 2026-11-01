package scheduler

import (
	"github.com/user4xn/cronsoul/internal/store"
	"testing"
	"time"
)

func schedulerDB(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("INSERT INTO users(id,username,password_hash,role,must_change_password) VALUES('u','admin','x','superadmin',0)"); err != nil {
		t.Fatal(err)
	}
	return s
}
func addJob(t *testing.T, s *store.Store, id, cronExpr, zone, next string) {
	t.Helper()
	_, err := s.DB.Exec(`INSERT INTO jobs(id,key,name,service,method,url,cron,timezone,next_run_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, "key-"+id, "name", "svc", "GET", "https://example.test/", cronExpr, zone, next)
	if err != nil {
		t.Fatal(err)
	}
}
func TestSchedulerOffDoesNotTouchCursorOrQueue(t *testing.T) {
	s := schedulerDB(t)
	addJob(t, s, "j", "0 * * * *", "UTC", "2026-10-27T10:00:00Z")
	now := time.Date(2026, 10, 27, 11, 0, 0, 0, time.UTC)
	w := Scheduler{DB: s.DB, Now: func() time.Time { return now }}
	if err := w.Tick(); err != nil {
		t.Fatal(err)
	}
	var c int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM runs").Scan(&c)
	if c != 0 {
		t.Fatalf("runs=%d", c)
	}
	var cursor string
	_ = s.DB.QueryRow("SELECT next_run_at FROM jobs WHERE id='j'").Scan(&cursor)
	if cursor != "2026-10-27T10:00:00Z" {
		t.Fatalf("cursor=%s", cursor)
	}
}
func TestMissedWatcherIgnoresFutureCursorAndDedupesPast(t *testing.T) {
	s := schedulerDB(t)
	addJob(t, s, "future", "0 * * * *", "UTC", "2026-10-27T11:05:00Z")
	addJob(t, s, "past", "0 * * * *", "UTC", "2026-10-27T10:00:00Z")
	now := time.Date(2026, 10, 27, 11, 0, 0, 0, time.UTC)
	w := Scheduler{DB: s.DB, Enabled: true, Grace: 15 * time.Minute, Now: func() time.Time { return now }}
	if _, err := s.DB.Exec("UPDATE settings SET value='true' WHERE key='scheduler.enabled'"); err != nil {
		t.Fatal(err)
	}
	if err := w.WatchMissed(); err != nil {
		t.Fatal(err)
	}
	if err := w.WatchMissed(); err != nil {
		t.Fatal(err)
	}
	var future, past int
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM alerts WHERE job_id='future'").Scan(&future)
	_ = s.DB.QueryRow("SELECT COUNT(*) FROM alerts WHERE job_id='past'").Scan(&past)
	if future != 0 || past != 1 {
		t.Fatalf("future alerts=%d past alerts=%d", future, past)
	}
}
