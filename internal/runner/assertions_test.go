package runner

import (
	"testing"
	"time"
)

func testResult(status int, headers map[string]string, body string, dur time.Duration) Result {
	return Result{Status: status, Headers: headers, Body: []byte(body), Duration: dur}
}

func TestEvaluateAssertionsKinds(t *testing.T) {
	headers := map[string]string{"Content-Type": "application/json", "X-Request-Id": "abc"}
	body := `{"status":"success","data":{"total":3,"summary":{"skipped":0,"processed":2},"items":[{"id":7},{"id":9}]}}`

	cases := []struct {
		name string
		rule AssertionRule
		want bool
	}{
		{"status pass", AssertionRule{Kind: "status_code", Value: 200}, true},
		{"status fail", AssertionRule{Kind: "status_code", Value: 201}, false},
		{"duration pass", AssertionRule{Kind: "duration_ms", Value: 500, Op: "lt"}, true},
		{"duration fail", AssertionRule{Kind: "duration_ms", Value: 300, Op: "lt"}, false},
		{"json path exists", AssertionRule{Kind: "json_path_exists", Path: "data.summary.processed"}, true},
		{"json path missing", AssertionRule{Kind: "json_path_exists", Path: "data.summary.missing"}, false},
		{"json array index", AssertionRule{Kind: "json_path_equals", Path: "data.items[1].id", Value: 9}, true},
		{"json equals via equals field", AssertionRule{Kind: "json_path_equals", Path: "data.total", Equals: 3}, true},
		{"json equals string", AssertionRule{Kind: "json_path_equals", Path: "status", Value: "success"}, true},
		{"json gte pass", AssertionRule{Kind: "json_path_gte", Path: "data.total", Value: 3}, true},
		{"json gte fail", AssertionRule{Kind: "json_path_gte", Path: "data.total", Value: 4}, false},
		{"json lte pass", AssertionRule{Kind: "json_path_lte", Path: "data.summary.skipped", Value: 0}, true},
		{"json lte fail", AssertionRule{Kind: "json_path_lte", Path: "data.total", Value: 2}, false},
		{"body contains", AssertionRule{Kind: "body_contains", Value: `"processed":2`}, true},
		{"body not contains", AssertionRule{Kind: "body_contains", Value: "nope"}, false},
		{"header equals case-insensitive name", AssertionRule{Kind: "header_equals", Path: "content-type", Value: "application/json"}, true},
		{"header mismatch", AssertionRule{Kind: "header_equals", Path: "Content-Type", Value: "text/plain"}, false},
	}
	res := testResult(200, headers, body, 320*time.Millisecond)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := EvaluateAssertions([]AssertionRule{tc.rule}, res)
			if len(out) != 1 {
				t.Fatalf("outcomes=%d", len(out))
			}
			if out[0].Passed != tc.want {
				t.Fatalf("passed=%v want %v (expected=%q actual=%q)", out[0].Passed, tc.want, out[0].Expected, out[0].Actual)
			}
			if out[0].Kind != tc.rule.Kind {
				t.Fatalf("kind=%q", out[0].Kind)
			}
		})
	}
}

func TestEvaluateAssertionsNonJSONBodyAffectsOnlyJSONKinds(t *testing.T) {
	res := testResult(200, nil, "plain text ok", 10*time.Millisecond)
	out := EvaluateAssertions([]AssertionRule{
		{Kind: "json_path_exists", Path: "data"},
		{Kind: "body_contains", Value: "ok"},
		{Kind: "status_code", Value: 200},
	}, res)
	if out[0].Passed {
		t.Fatalf("json path must not resolve on non-JSON body: %+v", out[0])
	}
	if !out[1].Passed || !out[2].Passed {
		t.Fatalf("unexpected outcomes: %+v", out)
	}
}

func TestEvaluateAssertionsTransportFailureIsRecordedNotSkipped(t *testing.T) {
	out := EvaluateAssertions([]AssertionRule{
		{Kind: "status_code", Value: 200},
		{Kind: "body_contains", Value: "ok"},
	}, Result{})
	if len(out) != 2 || out[0].Passed || out[1].Passed {
		t.Fatalf("outcomes=%+v", out)
	}
}

func TestEvaluateAssertionsNumericStringCoercion(t *testing.T) {
	res := testResult(200, nil, `{"count":"12","ok":true}`, time.Millisecond)
	out := EvaluateAssertions([]AssertionRule{
		{Kind: "json_path_gte", Path: "count", Value: 10},
		{Kind: "json_path_equals", Path: "ok", Value: true},
		{Kind: "json_path_equals", Path: "ok", Value: "true"},
	}, res)
	if !out[0].Passed {
		t.Fatalf("numeric string should compare numerically: %+v", out[0])
	}
	if !out[1].Passed {
		t.Fatalf("bool equality: %+v", out[1])
	}
	if out[2].Passed {
		t.Fatalf("bool must not equal string: %+v", out[2])
	}
}

func TestValidateAssertions(t *testing.T) {
	valid := []AssertionRule{
		{Kind: "status_code", Value: 200},
		{Kind: "duration_ms", Value: 1000, Op: "lt"},
		{Kind: "json_path_exists", Path: "data.ok"},
		{Kind: "json_path_equals", Path: "data.ok", Value: true},
		{Kind: "json_path_gte", Path: "data.n", Value: 1},
		{Kind: "json_path_lte", Path: "data.n", Value: 9},
		{Kind: "body_contains", Value: "ok"},
		{Kind: "header_equals", Path: "Content-Type", Value: "application/json"},
	}
	if err := ValidateAssertions(valid); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
	invalid := [][]AssertionRule{
		{{Kind: "nope", Value: 1}},
		{{Kind: "status_code"}},
		{{Kind: "status_code", Value: 200, Op: "approx"}},
		{{Kind: "duration_ms", Value: "soon"}},
		{{Kind: "json_path_exists"}},
		{{Kind: "json_path_equals", Path: "a"}},
		{{Kind: "json_path_gte", Path: "a", Value: "x"}},
		{{Kind: "body_contains"}},
		{{Kind: "header_equals", Path: "X-A"}},
		{{Kind: ""}},
	}
	for i, rules := range invalid {
		if err := ValidateAssertions(rules); err == nil {
			t.Fatalf("case %d accepted invalid rules: %+v", i, rules)
		}
	}
	if err := ValidateAssertions(nil); err != nil {
		t.Fatalf("empty rules must be valid: %v", err)
	}
}

func TestParseAssertionRulesAcceptsObjectOrArray(t *testing.T) {
	fromObject, err := ParseAssertionRules(`{"enabled":true,"rules":[{"kind":"status_code","value":200}]}`)
	if err != nil || len(fromObject) != 1 {
		t.Fatalf("object: %v %+v", err, fromObject)
	}
	fromArray, err := ParseAssertionRules(`[{"kind":"body_contains","value":"ok"}]`)
	if err != nil || len(fromArray) != 1 {
		t.Fatalf("array: %v %+v", err, fromArray)
	}
	if _, err = ParseAssertionRules(`{"rules":[{"kind":"bogus"}]}`); err == nil {
		t.Fatal("invalid rules must be rejected while parsing job config")
	}
	if rules, err := ParseAssertionRules(""); err != nil || rules != nil {
		t.Fatalf("empty: %v %+v", err, rules)
	}
}
