package runner

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/robfig/cron/v3"
)

// timelineCell is one hour of one endpoint's day. Runs is the total number of
// executions in that hour, so a lane reads as a calendar rather than a density
// blur: filled = something happened, colour = how it went.
type timelineCell struct {
	Runs   int `json:"runs"`
	OK     int `json:"ok"`
	Fail   int `json:"fail"`
	Missed int `json:"missed"`
}

type timelineLane struct {
	JobID   string         `json:"job_id"`
	Name    string         `json:"name"`
	Key     string         `json:"key"`
	Enabled bool           `json:"enabled"`
	Cron    string         `json:"cron"`
	Cells   []timelineCell `json:"cells"`
	Runs    int            `json:"runs"`
	OK      int            `json:"ok"`
	Fail    int            `json:"fail"`
	Missed  int            `json:"missed"`
}

type hourLoad struct {
	Hour int `json:"hour"`
	Jobs int `json:"jobs"`
}

type failureRow struct {
	JobID  string `json:"job_id"`
	Name   string `json:"name"`
	Failed int    `json:"failed"`
	Missed int    `json:"missed"`
}

// timeline answers GET /timeline: a 24-lane wall for one day or the trailing
// week, aggregated by hour of day in the requested timezone.
func (a *API) timeline(w http.ResponseWriter, r *http.Request) {
	zone := r.URL.Query().Get("tz")
	if zone == "" {
		// follow the console display timezone, which is an IANA name; the UI
		// only knows the short label (WIB) and must not send that here.
		_ = a.DB.QueryRow(`SELECT value FROM settings WHERE key='display.timezone'`).Scan(&zone)
		if zone == "" {
			zone = "Asia/Jakarta"
		}
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		out(w, 400, map[string]string{"error": "unknown timezone"})
		return
	}
	rangeKey := r.URL.Query().Get("range")
	days := 1
	switch rangeKey {
	case "week", "7d":
		rangeKey, days = "week", 7
	case "today", "":
		rangeKey, days = "today", 1
	default:
		out(w, 400, map[string]string{"error": "range must be today or week"})
		return
	}

	now := time.Now().In(loc)
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from := startOfDay.AddDate(0, 0, -(days - 1))
	to := startOfDay.AddDate(0, 0, 1)

	rows, err := a.DB.Query(`SELECT job_id,status,created_at FROM runs WHERE created_at>=? AND created_at<?`,
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	if err != nil {
		out(w, 500, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()

	lanes := map[string]*timelineLane{}
	order := []string{}
	ensure := func(id string) *timelineLane {
		if l, ok := lanes[id]; ok {
			return l
		}
		l := &timelineLane{JobID: id, Cells: make([]timelineCell, 24)}
		lanes[id] = l
		order = append(order, id)
		return l
	}
	for rows.Next() {
		var jobID, status, createdAt string
		if rows.Scan(&jobID, &status, &createdAt) != nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			continue
		}
		l := ensure(jobID)
		h := t.In(loc).Hour()
		l.Cells[h].Runs++
		l.Runs++
		if status == "success" {
			l.Cells[h].OK++
			l.OK++
		} else {
			l.Cells[h].Fail++
			l.Fail++
		}
	}

	// Missed windows are stored as alerts; they explain gaps that are our fault
	// rather than the endpoint's.
	if ar, err := a.DB.Query(`SELECT job_id,window_start FROM alerts WHERE kind='missed_run' AND window_start>=? AND window_start<?`,
		from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)); err == nil {
		for ar.Next() {
			var jobID, at string
			if ar.Scan(&jobID, &at) != nil {
				continue
			}
			t, err := time.Parse(time.RFC3339, at)
			if err != nil {
				continue
			}
			l := ensure(jobID)
			h := t.In(loc).Hour()
			l.Cells[h].Missed++
			l.Missed++
		}
		ar.Close()
	}

	meta := map[string]struct {
		name    string
		key     string
		cron    string
		enabled bool
		hour    int
	}{}
	if jr, err := a.DB.Query(`SELECT id,name,key,cron,enabled,next_run_at FROM jobs`); err == nil {
		for jr.Next() {
			var id, name, key, cronExpr string
			var enabled int
			var next *string
			if jr.Scan(&id, &name, &key, &cronExpr, &enabled, &next) != nil {
				continue
			}
			h := -1
			if next != nil {
				if t, err := time.Parse(time.RFC3339, *next); err == nil {
					h = t.In(loc).Hour()
				}
			}
			meta[id] = struct {
				name    string
				key     string
				cron    string
				enabled bool
				hour    int
			}{name, key, cronExpr, enabled != 0, h}
		}
		jr.Close()
	}

	data := make([]timelineLane, 0, len(order))
	for _, id := range order {
		l := lanes[id]
		if m, ok := meta[id]; ok {
			l.Name, l.Key, l.Cron, l.Enabled = m.name, m.key, m.cron, m.enabled
		}
		if l.Name == "" {
			l.Name = id
		}
		data = append(data, *l)
	}
	sort.SliceStable(data, func(i, j int) bool {
		if data[i].Fail+data[i].Missed != data[j].Fail+data[j].Missed {
			return data[i].Fail+data[i].Missed > data[j].Fail+data[j].Missed
		}
		return data[i].Name < data[j].Name
	})

	// Scheduled load per hour of day: expand each enabled schedule across the
	// range so the banner can warn about pile-ups before they hurt.
	load := make([]hourLoad, 24)
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	horizon := to
	if days == 1 {
		horizon = to
	}
	for id, m := range meta {
		_ = id
		if !m.enabled {
			continue
		}
		sched, err := parser.Parse(m.cron)
		if err != nil {
			continue
		}
		seen := map[int]bool{}
		cursor := from
		for i := 0; i < 20000; i++ {
			next := sched.Next(cursor)
			if next.IsZero() || !next.Before(horizon) {
				break
			}
			seen[next.In(loc).Hour()] = true
			cursor = next
		}
		for h := range seen {
			load[h].Jobs++
		}
	}
	for i := range load {
		load[i].Hour = i
	}

	// Which endpoints are least reliable over the full week, distinguishing a
	// failed call from a window we never fired.
	failures := []failureRow{}
	weekStart := startOfDay.AddDate(0, 0, -6)
	agg := map[string]*failureRow{}
	if fr, err := a.DB.Query(`SELECT job_id,status,COUNT(*) FROM runs WHERE created_at>=? GROUP BY job_id,status`,
		weekStart.UTC().Format(time.RFC3339)); err == nil {
		for fr.Next() {
			var id, status string
			var n int
			if fr.Scan(&id, &status, &n) != nil || status == "success" {
				continue
			}
			if agg[id] == nil {
				agg[id] = &failureRow{JobID: id}
			}
			agg[id].Failed += n
		}
		fr.Close()
	}
	if mr, err := a.DB.Query(`SELECT job_id,COUNT(*) FROM alerts WHERE kind='missed_run' AND window_start>=? GROUP BY job_id`,
		weekStart.UTC().Format(time.RFC3339)); err == nil {
		for mr.Next() {
			var id string
			var n int
			if mr.Scan(&id, &n) != nil {
				continue
			}
			if agg[id] == nil {
				agg[id] = &failureRow{JobID: id}
			}
			agg[id].Missed += n
		}
		mr.Close()
	}
	for id, row := range agg {
		row.Name = meta[id].name
		if row.Name == "" {
			row.Name = id
		}
		failures = append(failures, *row)
	}
	sort.SliceStable(failures, func(i, j int) bool {
		return failures[i].Failed+failures[i].Missed > failures[j].Failed+failures[j].Missed
	})
	if len(failures) > 6 {
		failures = failures[:6]
	}

	out(w, http.StatusOK, map[string]any{
		"range":      rangeKey,
		"days":       days,
		"timezone":   zone,
		"from":       from.Format(time.RFC3339),
		"to":         to.Format(time.RFC3339),
		"date":       now.Format("2006-01-02"),
		"lanes":      data,
		"load":       load,
		"failures7d": failures,
	})
}

// keep context import used when the handler is extended with cancellation.
var _ = context.Background
var _ = strconv.Itoa
