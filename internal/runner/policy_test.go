package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The governance allowlist is read immediately before every outbound call.
// A stale boot-time Egress value would let a removed host keep running.
func TestPolicyOverridesBootEgressAtCallTime(t *testing.T) {
	api := &API{
		Egress: EgressConfig{Allowlist: []string{"old.example.test"}},
		Policy: func() EgressConfig {
			return EgressConfig{Allowlist: []string{"new.example.test"}}
		},
	}
	_, err := api.do(context.Background(), Request{Method: "GET", URL: "https://old.example.test/x"})
	if !errors.Is(err, ErrNotAllowlisted) {
		t.Fatalf("removed host still allowed: %v", err)
	}
	_, err = api.do(context.Background(), Request{Method: "GET", URL: "https://new.example.test/x"})
	if errors.Is(err, ErrNotAllowlisted) {
		t.Fatalf("live host rejected by stale policy: %v", err)
	}
}

func TestInsecureModeExpiresAtPolicyTime(t *testing.T) {
	// A host-only allowlist entry means port 443, so plain HTTP needs the port
	// spelled out. That is intentional: an allowlisted host must not silently
	// grant port 80 as well.
	cfg := EgressConfig{Allowlist: []string{"plain.example.test:80"}, AllowInsecure: true, InsecureUntil: time.Now().Add(-time.Minute)}
	if err := cfg.ValidateURL("http://plain.example.test/", time.Now()); !errors.Is(err, ErrInsecure) {
		t.Fatalf("expired insecure window still accepted: %v", err)
	}
	cfg.InsecureUntil = time.Now().Add(time.Minute)
	if err := cfg.ValidateURL("http://plain.example.test/", time.Now()); err != nil {
		t.Fatalf("active insecure window rejected: %v", err)
	}
	// AllowInsecure alone must not widen the allowlist.
	insecureOnly := EgressConfig{Allowlist: []string{"plain.example.test:80"}, AllowInsecure: true, InsecureUntil: time.Now().Add(time.Minute)}
	if err := insecureOnly.ValidateURL("http://other.example.test/", time.Now()); !errors.Is(err, ErrNotAllowlisted) {
		t.Fatalf("insecure mode must not bypass the allowlist: %v", err)
	}
}
