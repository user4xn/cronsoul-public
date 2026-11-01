package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// AssertionRule is one optional post-run check. Assertions never influence the
// HTTP outcome of a run: they only produce assertion_passed plus per-rule rows.
type AssertionRule struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	Op     string `json:"op,omitempty"`
	Value  any    `json:"value,omitempty"`
	Equals any    `json:"equals,omitempty"`
}

// Outcome is a single evaluated assertion, ready to persist in assertion_results.
type Outcome struct {
	Kind     string
	Target   string
	Expected string
	Actual   string
	Passed   bool
}

// AssertionKinds lists every rule kind supported in phase 1.
var AssertionKinds = []string{
	"status_code",
	"duration_ms",
	"json_path_exists",
	"json_path_equals",
	"json_path_gte",
	"json_path_lte",
	"body_contains",
	"header_equals",
}

func knownKind(kind string) bool {
	for _, k := range AssertionKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// ValidateAssertions rejects structurally unusable rules before they are stored.
func ValidateAssertions(rules []AssertionRule) error {
	for i, r := range rules {
		if !knownKind(r.Kind) {
			return fmt.Errorf("assertion %d: unsupported kind %q", i+1, r.Kind)
		}
		switch r.Kind {
		case "status_code":
			n, ok := toFloat(r.Value)
			if !ok || n < 100 || n > 599 {
				return fmt.Errorf("assertion %d: status_code requires a numeric value between 100 and 599", i+1)
			}
			if r.Op != "" {
				return fmt.Errorf("assertion %d: status_code does not accept op", i+1)
			}
		case "duration_ms":
			if n, ok := toFloat(r.Value); !ok || n < 0 {
				return fmt.Errorf("assertion %d: duration_ms requires a non-negative numeric value", i+1)
			}
			switch r.Op {
			case "", "lt", "lte", "gt", "gte":
			default:
				return fmt.Errorf("assertion %d: unsupported duration_ms op %q", i+1, r.Op)
			}
		case "json_path_exists":
			if strings.TrimSpace(r.Path) == "" {
				return fmt.Errorf("assertion %d: json_path_exists requires path", i+1)
			}
		case "json_path_equals":
			if strings.TrimSpace(r.Path) == "" {
				return fmt.Errorf("assertion %d: json_path_equals requires path", i+1)
			}
			if r.Equals == nil && r.Value == nil {
				return fmt.Errorf("assertion %d: json_path_equals requires value or equals", i+1)
			}
		case "json_path_gte", "json_path_lte":
			if strings.TrimSpace(r.Path) == "" {
				return fmt.Errorf("assertion %d: %s requires path", i+1, r.Kind)
			}
			if _, ok := toFloat(r.Value); !ok {
				return fmt.Errorf("assertion %d: %s requires a numeric value", i+1, r.Kind)
			}
		case "body_contains":
			if s, ok := r.Value.(string); !ok || s == "" {
				return fmt.Errorf("assertion %d: body_contains requires a non-empty string value", i+1)
			}
		case "header_equals":
			if strings.TrimSpace(r.Path) == "" {
				return fmt.Errorf("assertion %d: header_equals requires the header name in path", i+1)
			}
			if _, ok := r.Value.(string); !ok {
				return fmt.Errorf("assertion %d: header_equals requires a string value", i+1)
			}
		}
	}
	return nil
}

// ParseAssertionRules accepts either the stored job object form
// ({"enabled":true,"rules":[...]}) or a bare rules array. Empty input means
// "no assertions configured".
func ParseAssertionRules(raw string) ([]AssertionRule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if !json.Valid([]byte(raw)) {
		return nil, errors.New("assertions must be valid JSON")
	}
	var rules []AssertionRule
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &rules); err != nil {
			return nil, err
		}
	} else {
		var wrapper struct {
			Rules []AssertionRule `json:"rules"`
		}
		if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
			return nil, err
		}
		rules = wrapper.Rules
	}
	if err := ValidateAssertions(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

// EvaluateAssertions runs every rule against a completed HTTP result. Rules are
// always reported (pass or fail) so a failed run still shows which checks ran.
func EvaluateAssertions(rules []AssertionRule, res Result) []Outcome {
	out := make([]Outcome, 0, len(rules))
	var doc any
	var parsed bool
	for _, r := range rules {
		o := Outcome{Kind: r.Kind, Target: r.Path}
		switch r.Kind {
		case "status_code":
			want, _ := toFloat(r.Value)
			o.Expected = trimNumber(want)
			o.Actual = strconv.Itoa(res.Status)
			o.Passed = res.Status != 0 && float64(res.Status) == want
		case "duration_ms":
			want, _ := toFloat(r.Value)
			got := float64(res.Duration.Milliseconds())
			op := r.Op
			if op == "" {
				op = "lt"
			}
			o.Expected = op + " " + trimNumber(want)
			o.Actual = trimNumber(got)
			o.Passed = compareFloat(got, want, op)
		case "body_contains":
			want, _ := r.Value.(string)
			o.Expected = want
			o.Actual = excerpt(string(res.Body), 200)
			o.Passed = strings.Contains(string(res.Body), want)
		case "header_equals":
			want, _ := r.Value.(string)
			o.Expected = want
			actual, found := lookupHeader(res.Headers, r.Path)
			o.Actual = actual
			o.Passed = found && actual == want
		case "json_path_exists":
			if !parsed {
				doc, _ = decodeJSONDoc(res.Body)
				parsed = true
			}
			v, found := lookupJSONPath(doc, r.Path)
			o.Expected = "exists"
			o.Actual = valueString(v)
			o.Passed = found
		case "json_path_equals", "json_path_gte", "json_path_lte":
			if !parsed {
				doc, _ = decodeJSONDoc(res.Body)
				parsed = true
			}
			v, found := lookupJSONPath(doc, r.Path)
			o.Actual = valueString(v)
			want := r.Value
			if r.Equals != nil {
				want = r.Equals
			}
			o.Expected = valueString(want)
			if !found {
				o.Passed = false
				break
			}
			if r.Kind == "json_path_equals" {
				o.Passed = equalValues(v, want)
			} else {
				nv, ok1 := toFloat(v)
				nw, ok2 := toFloat(want)
				if !ok1 || !ok2 {
					o.Passed = false
					break
				}
				if r.Kind == "json_path_gte" {
					o.Expected = ">= " + trimNumber(nw)
					o.Passed = nv >= nw
				} else {
					o.Expected = "<= " + trimNumber(nw)
					o.Passed = nv <= nw
				}
			}
		}
		out = append(out, o)
	}
	return out
}

func toFloat(v any) (float64, bool) { return toFloatAny(v) }

func trimNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func compareFloat(got, want float64, op string) bool {
	switch op {
	case "lt":
		return got < want
	case "lte":
		return got <= want
	case "gt":
		return got > want
	case "gte":
		return got >= want
	}
	return false
}

func lookupHeader(headers map[string]string, name string) (string, bool) {
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func decodeJSONDoc(body []byte) (any, bool) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, false
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if dec.Decode(&v) != nil {
		return nil, false
	}
	return v, true
}

func splitPath(path string) []string {
	p := strings.TrimSpace(path)
	p = strings.TrimPrefix(p, "$")
	p = strings.TrimPrefix(p, ".")
	var segs []string
	var cur strings.Builder
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '.':
			if cur.Len() > 0 {
				segs = append(segs, cur.String())
				cur.Reset()
			}
		case '[':
			if cur.Len() > 0 {
				segs = append(segs, cur.String())
				cur.Reset()
			}
			j := strings.IndexByte(p[i:], ']')
			if j < 0 {
				segs = append(segs, p[i:])
				return segs
			}
			segs = append(segs, "["+p[i+1:i+j]+"]")
			i += j
		default:
			cur.WriteByte(p[i])
		}
	}
	if cur.Len() > 0 {
		segs = append(segs, cur.String())
	}
	return segs
}

// lookupJSONPath walks dotted segments with optional [index] accessors.
func lookupJSONPath(doc any, path string) (any, bool) {
	segs := splitPath(path)
	if len(segs) == 0 {
		return nil, false
	}
	cur := doc
	for _, seg := range segs {
		if strings.HasPrefix(seg, "[") && strings.HasSuffix(seg, "]") {
			idx, err := strconv.Atoi(seg[1 : len(seg)-1])
			if err != nil {
				return nil, false
			}
			arr, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(arr) {
				return nil, false
			}
			cur = arr[idx]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

func equalValues(a, b any) bool {
	if af, ok := toFloat(a); ok {
		if bf, ok2 := toFloat(b); ok2 {
			return af == bf
		}
		return false
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as == bs
	}
	ab, aok2 := a.(bool)
	bb, bok2 := b.(bool)
	if aok2 && bok2 {
		return ab == bb
	}
	return reflect.DeepEqual(normalizeJSON(a), normalizeJSON(b))
}

func normalizeJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalizeJSON(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normalizeJSON(vv)
		}
		return out
	}
	return v
}

func excerpt(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func toFloatAny(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}
