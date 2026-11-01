package runner

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestValidateURLSecurity(t *testing.T) {
	base := EgressConfig{Allowlist: []string{"api.example.test:8443"}}
	if err := base.ValidateURL("https://api.example.test:8443/x", time.Now()); err != nil {
		t.Fatalf("allowlisted URL: %v", err)
	}
	if err := base.ValidateURL("http://api.example.test:8443/x", time.Now()); err != ErrInsecure {
		t.Fatalf("expected insecure rejection, got %v", err)
	}
	if err := (EgressConfig{Allowlist: []string{"169.254.169.254"}}).ValidateURL("https://169.254.169.254/", time.Now()); err == nil {
		t.Fatal("explicitly allowlisted metadata IP must still be rejected")
	}
	for _, host := range []string{"127.0.0.1", "0.0.0.0", "224.0.0.1", "10.0.0.1"} {
		cfg := EgressConfig{Allowlist: []string{host}}
		err := cfg.ValidateURL("https://"+host+"/", time.Now())
		if host == "10.0.0.1" {
			if err == nil {
				t.Errorf("private address allowed without flag")
			}
			continue
		}
		if err == nil {
			t.Errorf("blocked address %s allowed", host)
		}
	}
	private := EgressConfig{Allowlist: []string{"10.0.0.1"}, AllowPrivate: true}
	if err := private.ValidateURL("https://10.0.0.1/", time.Now()); err != nil {
		t.Fatalf("private allowlist + flag rejected: %v", err)
	}
	if err := (EgressConfig{Allowlist: []string{"example.test"}}).ValidateURL("https://example.test:8443/", time.Now()); err == nil {
		t.Fatal("host-only allowlist must default to port 443")
	}
	if err := (EgressConfig{Allowlist: []string{"example.test"}}).ValidateURL("https://other.test/", time.Now()); err != ErrNotAllowlisted {
		t.Fatalf("expected allowlist rejection, got %v", err)
	}
}
func TestBlockedIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "169.254.1.1", "10.0.0.1", "224.0.0.1", "::1", "fe80::1"} {
		if !blockedIP(net.ParseIP(s)) {
			t.Errorf("%s was not blocked", s)
		}
	}
}
func TestTemplateExpansion(t *testing.T) {
	v, err := ExpandJSON([]byte(`{"url":"{{job_key}}","nested":{"t":"{{simulated_time}}"}}`), TemplateVars("r1", "job-a", time.Unix(0, 0), "2026-01-01T00:00:00Z"))
	if err != nil || string(v) != `{"nested":{"t":"2026-01-01T00:00:00Z"},"url":"job-a"}` {
		t.Fatalf("expansion: %s %v", v, err)
	}
}
func TestAuthModes(t *testing.T) {
	for _, a := range []AuthConfig{{Type: "bearer", Token: "x"}, {Type: "api_key", APIKey: "x"}, {Type: "basic", Username: "u", Password: "p"}, {Type: "custom_header", Header: "X-X", Value: "v"}, {Type: "hmac_sha256", Secret: "s"}} {
		req, _ := NewRequestForTest("GET", "https://example.test/")
		if err := ApplyAuth(req, a); err != nil {
			t.Fatal(err)
		}
	}
}
func NewRequestForTest(method, raw string) (*http.Request, error) {
	return http.NewRequestWithContext(context.Background(), method, raw, nil)
}

func TestDryRunParameterReachesTheTargetAndTheExcerpt(t *testing.T) {
	// The runner injects the configured marker into the outgoing query; the run
	// row must record the URL as actually called or QA cannot verify a dry run.
	got := withQuery("https://api.example.test/v1/sync?tenant=acme", map[string]string{"dry_run": "true"})
	if got != "https://api.example.test/v1/sync?dry_run=true&tenant=acme" {
		t.Fatalf("effective URL = %q", got)
	}
	excerpt := requestExcerpt("POST", got)
	if excerpt != "POST https://api.example.test/v1/sync?dry_run=true&tenant=acme" {
		t.Fatalf("excerpt lost the query string: %q", excerpt)
	}
}
