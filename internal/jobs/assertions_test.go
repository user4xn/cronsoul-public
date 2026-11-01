package jobs

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/user4xn/cronsoul/internal/runner"
)

func TestJobAssertionsAreOptionalAndPersisted(t *testing.T) {
	_, h, token := jobSetup(t, "admin")

	// no assertions block at all: the job is valid and simply checks nothing
	plain := `{"key":"plain","name":"Plain","service":"svc","method":"GET","url":"https://example.test/a","cron":"0 * * * *"}`
	rec := request(t, h, token, http.MethodPost, "/", plain)
	if rec.Code != http.StatusCreated {
		t.Fatalf("plain create=%d %s", rec.Code, rec.Body)
	}
	var created Job
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Assertions != nil {
		t.Fatalf("assertions must stay absent: %+v", created.Assertions)
	}

	withRules := `{"key":"checked","name":"Checked","service":"svc","method":"GET","url":"https://example.test/b","cron":"0 * * * *",` +
		`"assertions":{"enabled":true,"rules":[{"kind":"status_code","value":200},{"kind":"json_path_gte","path":"data.total","value":1}]}}`
	rec = request(t, h, token, http.MethodPost, "/", withRules)
	if rec.Code != http.StatusCreated {
		t.Fatalf("checked create=%d %s", rec.Code, rec.Body)
	}
	var checked Job
	if err := json.Unmarshal(rec.Body.Bytes(), &checked); err != nil {
		t.Fatal(err)
	}
	if checked.Assertions == nil || !checked.Assertions.Enabled || len(checked.Assertions.Rules) != 2 {
		t.Fatalf("assertions roundtrip: %+v", checked.Assertions)
	}
	if checked.Assertions.Rules[1].Path != "data.total" {
		t.Fatalf("rule path lost: %+v", checked.Assertions.Rules[1])
	}

	// stored config is visible on GET and through the list endpoint
	rec = request(t, h, token, http.MethodGet, "/"+checked.ID, "")
	if rec.Code != 200 {
		t.Fatalf("get=%d", rec.Code)
	}
	var fetched Job
	if err := json.Unmarshal(rec.Body.Bytes(), &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.Assertions == nil || len(fetched.Assertions.Rules) != 2 {
		t.Fatalf("get assertions: %+v", fetched.Assertions)
	}
	rec = request(t, h, token, http.MethodGet, "/", "")
	var list struct {
		Data []Job `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, j := range list.Data {
		if j.ID == checked.ID {
			found = j.Assertions != nil && len(j.Assertions.Rules) == 2
		}
	}
	if !found {
		t.Fatalf("list lost assertions: %+v", list.Data)
	}
}

func TestJobAssertionsValidation(t *testing.T) {
	_, h, token := jobSetup(t, "admin")
	base := `{"key":"bad-assert","name":"Bad","service":"svc","method":"GET","url":"https://example.test/a","cron":"0 * * * *","assertions":`
	cases := []string{
		base + `{"enabled":true,"rules":[]}}`,
		base + `{"enabled":true,"rules":[{"kind":"bogus"}]}}`,
		base + `{"enabled":true,"rules":[{"kind":"json_path_equals","path":"a.b"}]}}`,
		base + `{"enabled":true,"rules":[{"kind":"duration_ms","value":100,"op":"around"}]}}`,
		base + `{"enabled":true,"rules":[{"kind":"header_equals","path":"X-A"}]}}`,
	}
	for _, body := range cases {
		if rec := request(t, h, token, http.MethodPost, "/", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s payload=%s", rec.Code, rec.Body, body)
		}
	}
}

func TestJobPatchKeepsAssertionsWhenOmittedAndCanDisableThem(t *testing.T) {
	st, h, token := jobSetup(t, "admin")
	payload := `{"key":"keep","name":"Keep","service":"svc","method":"GET","url":"https://example.test/a","cron":"0 * * * *",` +
		`"assertions":{"enabled":true,"rules":[{"kind":"status_code","value":200}]}}`
	rec := request(t, h, token, http.MethodPost, "/", payload)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create=%d %s", rec.Code, rec.Body)
	}
	var created Job
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// PATCH without the assertions field must not silently drop stored rules
	withoutAssertions := `{"key":"keep","name":"Keep renamed","service":"svc","method":"GET","url":"https://example.test/a","cron":"0 * * * *"}`
	rec = request(t, h, token, http.MethodPatch, "/"+created.ID, withoutAssertions)
	if rec.Code != 200 {
		t.Fatalf("patch=%d %s", rec.Code, rec.Body)
	}
	var patched Job
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Assertions == nil || !patched.Assertions.Enabled || len(patched.Assertions.Rules) != 1 {
		t.Fatalf("assertions dropped by patch: %+v", patched.Assertions)
	}

	// disabling keeps the rules available for later re-enabling
	disable := `{"key":"keep","name":"Keep","service":"svc","method":"GET","url":"https://example.test/a","cron":"0 * * * *",` +
		`"assertions":{"enabled":false,"rules":[{"kind":"status_code","value":200}]}}`
	rec = request(t, h, token, http.MethodPatch, "/"+created.ID, disable)
	if rec.Code != 200 {
		t.Fatalf("disable=%d %s", rec.Code, rec.Body)
	}
	var disabled Job
	if err := json.Unmarshal(rec.Body.Bytes(), &disabled); err != nil {
		t.Fatal(err)
	}
	if disabled.Assertions == nil || disabled.Assertions.Enabled || len(disabled.Assertions.Rules) != 1 {
		t.Fatalf("disabled assertions: %+v", disabled.Assertions)
	}
	var enabled int
	var raw string
	if err := st.DB.QueryRow("SELECT assertions_enabled, assertions_json FROM jobs WHERE id=?", created.ID).Scan(&enabled, &raw); err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Enabled bool                   `json:"enabled"`
		Rules   []runner.AssertionRule `json:"rules"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	if enabled != 0 || stored.Enabled || len(stored.Rules) != 1 {
		t.Fatalf("stored enabled=%d json=%s", enabled, raw)
	}
}
