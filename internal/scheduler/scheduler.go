package scheduler

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/user4xn/cronsoul/internal/notify"
	"github.com/user4xn/cronsoul/internal/store"
)

type Scheduler struct {
	DB      *sql.DB
	Enabled bool // optional override; persisted setting remains authoritative by default
	Grace   time.Duration
	Now     func() time.Time
}
type job struct {
	ID, Expression, Zone string
	Next                 sql.NullString
}

func (s Scheduler) isEnabled() (bool, error) {
	var value string
	err := s.DB.QueryRow(`SELECT value FROM settings WHERE key='scheduler.enabled'`).Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return value == "true" || value == "1", nil
}
func (s Scheduler) Tick() error {
	enabled, err := s.isEnabled()
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	rows, err := s.DB.Query(`SELECT id,cron,timezone,next_run_at FROM jobs WHERE enabled=1`)
	if err != nil {
		return err
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err = rows.Scan(&j.ID, &j.Expression, &j.Zone, &j.Next); err != nil {
			rows.Close()
			return err
		}
		jobs = append(jobs, j)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, j := range jobs {
		loc, e := time.LoadLocation(j.Zone)
		if e != nil {
			return fmt.Errorf("job %s timezone: %w", j.ID, e)
		}
		sched, e := cron.ParseStandard(j.Expression)
		if e != nil {
			return fmt.Errorf("job %s cron: %w", j.ID, e)
		}
		var due time.Time
		if j.Next.Valid {
			t, pe := time.Parse(time.RFC3339, j.Next.String)
			if pe != nil {
				return fmt.Errorf("job %s next_run_at: %w", j.ID, pe)
			}
			due = t.UTC()
		} else {
			due = sched.Next(now.In(loc)).UTC()
		}
		if !j.Next.Valid || due.After(now) {
			if !j.Next.Valid {
				_, e = s.DB.Exec(`UPDATE jobs SET next_run_at=? WHERE id=?`, due.Format(time.RFC3339), j.ID)
				if e != nil {
					return e
				}
			}
			continue
		}
		for !due.After(now) {
			stamp := due.Format(time.RFC3339)
			tx, e := s.DB.Begin()
			if e != nil {
				return e
			}
			_, e = tx.Exec(`INSERT INTO runs(id,job_id,trigger,scheduled_for,status,created_at) SELECT ?,?,'schedule',?,'queued',? WHERE NOT EXISTS(SELECT 1 FROM runs WHERE job_id=? AND scheduled_for=? AND trigger='schedule')`, store.ID(), j.ID, stamp, now.Format(time.RFC3339), j.ID, stamp)
			if e != nil {
				tx.Rollback()
				return e
			}
			next := sched.Next(due.In(loc)).UTC()
			if _, e = tx.Exec(`UPDATE jobs SET next_run_at=? WHERE id=?`, next.Format(time.RFC3339), j.ID); e != nil {
				tx.Rollback()
				return e
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			due = next
		}
	}
	return nil
}

// missedText explains which endpoint skipped which window, so the alert is
// actionable without opening the console.
func (s Scheduler) missedText(jobID, window string) string {
	var name, key, service, cron, tz string
	_ = s.DB.QueryRow(`SELECT name,key,service,cron,COALESCE(timezone,'Asia/Jakarta') FROM jobs WHERE id=?`, jobID).
		Scan(&name, &key, &service, &cron, &tz)
	when := window
	if t, err := time.Parse(time.RFC3339, window); err == nil {
		if loc, err := time.LoadLocation(tz); err == nil {
			when = t.In(loc).Format("2006-01-02 15:04:05 MST")
		}
	}
	var b strings.Builder
	b.WriteString("⏰ CronSoul · scheduled run missed\n\n")
	if name != "" {
		fmt.Fprintf(&b, "Endpoint: %s (%s · %s)\n", name, key, service)
	}
	fmt.Fprintf(&b, "Schedule: %s · %s\n", cron, tz)
	fmt.Fprintf(&b, "Window: %s\n", when)
	b.WriteString("What to check: the instance was down, asleep, or the scheduler was disabled at that moment.")
	return b.String()
}

func (s Scheduler) WatchMissed() error {
	enabled, err := s.isEnabled()
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	grace := s.Grace
	if grace == 0 {
		grace = 15 * time.Minute
	}
	rows, err := s.DB.Query(`SELECT id,cron,timezone,next_run_at FROM jobs WHERE enabled=1 AND next_run_at IS NOT NULL`)
	if err != nil {
		return err
	}
	type missed struct{ id, cron, zone, at string }
	var all []missed
	for rows.Next() {
		var m missed
		if err = rows.Scan(&m.id, &m.cron, &m.zone, &m.at); err != nil {
			rows.Close()
			return err
		}
		all = append(all, m)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, m := range all {
		t, e := time.Parse(time.RFC3339, m.at)
		if e != nil {
			return e
		}
		// next_run_at is the next occurrence. It is only missed when that
		// occurrence itself is already in the past and its grace window elapsed.
		if !t.Before(now) || now.Before(t.Add(grace)) {
			continue
		}
		_, err = s.DB.Exec(`INSERT INTO alerts(id,job_id,window_start,kind) VALUES(?,?,?,'missed_run') ON CONFLICT(job_id,window_start,kind) DO NOTHING`, store.ID(), m.id, m.at)
		if err != nil {
			return err
		}
		_ = notify.Enqueue(s.DB, "missed_run", m.id, "", map[string]string{"message": s.missedText(m.id, m.at)}, now)
	}
	return nil
}
