package runner

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// F2: a successful Play reaches channels that opted into manual_run, while
// channels on the default set stay quiet.
func TestManualRunSuccessNotifiesOptedInChannelsOnly(t *testing.T) {
	f := runSetup(t)
	if _, err := f.db.DB.Exec(`INSERT INTO channels(id,kind,name,config_cipher,events_json) VALUES('ch-manual','telegram','qa','cipher','["manual_run"]'),('ch-default','telegram','ops','cipher','')`); err != nil {
		t.Fatal(err)
	}
	stubTransport(f.api, Result{Status: 200, Body: []byte(`{"ok":true}`)}, nil)
	runID := f.play(t, `{}`)
	f.settle(t)
	rows, err := f.db.DB.Query(`SELECT target,payload_json FROM outbox WHERE run_id=? AND kind='manual_run'`, runID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var targets []string
	for rows.Next() {
		var target, payload string
		if err := rows.Scan(&target, &payload); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
		if !strings.Contains(payload, "manual run succeeded") || !strings.Contains(payload, "Trigger: manual") {
			t.Fatalf("payload does not say it was a manual run: %s", payload)
		}
	}
	if len(targets) != 1 || targets[0] != "ch-manual" {
		t.Fatalf("manual_run targets=%v, want only ch-manual", targets)
	}
}

// F4: the egress errors are English and keep their sentinel identity.
func TestEgressErrorsAreEnglish(t *testing.T) {
	err := (EgressConfig{Allowlist: []string{"example.test"}}).ValidateURL("https://other.test/", time.Now())
	if !errors.Is(err, ErrNotAllowlisted) || err.Error() != "blocked: host not on allowlist" {
		t.Fatalf("got %v", err)
	}
}

// F3: the form can ask, before saving, whether a URL would be refused.
func TestEgressCheckReportsAllowlistGap(t *testing.T) {
	f := runSetup(t)
	for url, want := range map[string]bool{
		"https://example.test/a":      true,
		"https://unknown.test/x":      false,
		"https://example.test:8443/a": false,
	} {
		rec := f.do(t, http.MethodGet, "/egress/check?url="+url, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s -> %d %s", url, rec.Code, rec.Body)
		}
		var got struct {
			Allowed        bool   `json:"allowed"`
			NotAllowlisted bool   `json:"not_allowlisted"`
			Suggested      string `json:"suggested_pattern"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Allowed != want {
			t.Fatalf("%s allowed=%v want %v", url, got.Allowed, want)
		}
		if !want && !got.NotAllowlisted {
			t.Fatalf("%s should be flagged as not allowlisted", url)
		}
		if url == "https://example.test:8443/a" && got.Suggested != "example.test:8443" {
			t.Fatalf("suggested pattern=%q", got.Suggested)
		}
	}
	if rec := f.do(t, http.MethodGet, "/egress/check?url=not-a-url", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad url -> %d", rec.Code)
	}
}
