package runner

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var (
	ErrRedirect         = errors.New("blocked: redirects are not followed")
	ErrNotAllowlisted   = errors.New("blocked: host not on allowlist")
	ErrBlockedIP        = errors.New("blocked: IP address not permitted")
	ErrInsecure         = errors.New("blocked: plain HTTP needs insecure test mode")
	ErrBodyTooLarge     = errors.New("request body too large")
	ErrResponseTooLarge = errors.New("response body too large")
)

const maxResponseBytes int64 = 1 << 20

type EgressConfig struct {
	Allowlist                                               []string
	AllowPrivate                                            bool
	AllowInsecure                                           bool
	InsecureUntil                                           time.Time
	Resolver                                                *net.Resolver
	DialTimeout, TLSHandshakeTimeout, ResponseHeaderTimeout time.Duration
}

func (c EgressConfig) insecureOK(now time.Time) bool {
	return c.AllowInsecure && (c.InsecureUntil.IsZero() || now.Before(c.InsecureUntil))
}
func hostPort(u *url.URL) (string, string) {
	h := strings.ToLower(u.Hostname())
	p := u.Port()
	if p == "" {
		if u.Scheme == "https" {
			p = "443"
		} else {
			p = "80"
		}
	}
	return h, p
}
func matchHost(pattern, host, port string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	ph, pp, err := net.SplitHostPort(pattern)
	if err == nil {
		pattern = strings.ToLower(ph)
		if pp != port {
			return false
		}
	} else {
		// A host-only allowlist entry implies the standard HTTPS port, never
		// arbitrary ports. Non-standard ports must be listed explicitly.
		if port != "443" {
			return false
		}
		pattern = strings.Trim(pattern, "[]")
	}
	if strings.HasPrefix(pattern, "*.") {
		return strings.HasSuffix(host, pattern[1:]) && host != pattern[2:]
	}
	return pattern == host
}
func allowlisted(patterns []string, host, port string) bool {
	for _, p := range patterns {
		if matchHost(p, host, port) {
			return true
		}
	}
	return false
}
func blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	if ip.To4() != nil {
		return ip.IsPrivate() || ip.To4()[0] == 169 && ip.To4()[1] == 254
	}
	return ip.IsPrivate()
}
func (c EgressConfig) validate(host, port string, ips []net.IP) error {
	explicitPort := allowlisted(c.Allowlist, host, port) || allowlisted(c.Allowlist, net.JoinHostPort(host, port), port)
	explicitHost := port == "443" && allowlisted(c.Allowlist, host, "")
	if net.ParseIP(host) == nil && !explicitPort && !explicitHost {
		return ErrNotAllowlisted
	}
	if !explicitPort && !explicitHost {
		return ErrNotAllowlisted
	}
	for _, ip := range ips {
		if ip == nil {
			return ErrBlockedIP
		}
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || (ip.To4() != nil && ip.To4()[0] == 169 && ip.To4()[1] == 254) {
			return fmt.Errorf("%w: %s", ErrBlockedIP, ip)
		}
		if ip.IsPrivate() && !c.AllowPrivate {
			return fmt.Errorf("%w: private %s", ErrBlockedIP, ip)
		}
	}
	return nil
}
func (c EgressConfig) ValidateURL(raw string, now time.Time) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("invalid target URL")
	}
	if u.Scheme == "http" && !c.insecureOK(now) {
		return ErrInsecure
	}
	h, p := hostPort(u)
	if !allowlisted(c.Allowlist, h, p) && !(p == "443" && allowlisted(c.Allowlist, h, "")) {
		return ErrNotAllowlisted
	}
	if net.ParseIP(h) != nil {
		return c.validate(h, p, []net.IP{net.ParseIP(h)})
	}
	return nil
}
func (c EgressConfig) Client(ctx context.Context, now time.Time) (*http.Client, error) {
	if c.Resolver == nil {
		c.Resolver = net.DefaultResolver
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.TLSHandshakeTimeout == 0 {
		c.TLSHandshakeTimeout = 5 * time.Second
	}
	if c.ResponseHeaderTimeout == 0 {
		c.ResponseHeaderTimeout = 10 * time.Second
	}
	tr := &http.Transport{TLSHandshakeTimeout: c.TLSHandshakeTimeout, ResponseHeaderTimeout: c.ResponseHeaderTimeout, ForceAttemptHTTP2: true}
	d := &net.Dialer{Timeout: c.DialTimeout}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		h, p, e := net.SplitHostPort(addr)
		if e != nil {
			return nil, e
		}
		ips, e := c.Resolver.LookupIP(ctx, "ip", h)
		if e != nil {
			return nil, e
		}
		if e = c.validate(strings.ToLower(h), p, ips); e != nil {
			return nil, e
		}
		return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), p))
	}
	return &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return ErrRedirect }}, nil
}

type Request struct {
	Method, URL                                                string
	Headers                                                    map[string]string
	Query                                                      map[string]string
	Body                                                       []byte
	Timeout                                                    time.Duration
	MaxBody                                                    int64
	MaxResponse                                                int64
	Auth                                                       AuthConfig
	RunID, Trigger, TriggeredBy, IdempotencyKey, SimulatedTime string
}
type AuthConfig struct{ Type, Token, APIKey, APIKeyHeader, Username, Password, Header, Value, Secret string }
type Result struct {
	Status    int
	Headers   map[string]string
	Body      []byte
	Truncated bool
	Duration  time.Duration
}

func expandString(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, "{{"+k+"}}", v)
	}
	return s
}
func Expand(v any, vars map[string]string) any {
	switch x := v.(type) {
	case string:
		return expandString(x, vars)
	case []any:
		for i := range x {
			x[i] = Expand(x[i], vars)
		}
	case map[string]any:
		for k, vv := range x {
			x[k] = Expand(vv, vars)
		}
	}
	return v
}
func ExpandJSON(raw []byte, vars map[string]string) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(Expand(v, vars))
}
func ApplyAuth(req *http.Request, a AuthConfig) error {
	switch a.Type {
	case "", "none":
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+a.Token)
	case "api_key":
		h := a.APIKeyHeader
		if h == "" {
			h = "X-API-Key"
		}
		req.Header.Set(h, a.APIKey)
	case "basic":
		req.SetBasicAuth(a.Username, a.Password)
	case "custom_header":
		if a.Header == "" {
			return errors.New("custom header name required")
		}
		req.Header.Set(a.Header, a.Value)
	case "hmac_sha256":
		m := hmac.New(sha256.New, []byte(a.Secret))
		m.Write([]byte(req.Method + " " + req.URL.RequestURI()))
		req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(m.Sum(nil)))
	default:
		return fmt.Errorf("unsupported auth type %q", a.Type)
	}
	return nil
}
func (c EgressConfig) Do(ctx context.Context, r Request) (Result, error) {
	var out Result
	now := time.Now()
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	if err := c.ValidateURL(r.URL, now); err != nil {
		return out, err
	}
	if r.MaxBody == 0 {
		r.MaxBody = 1 << 20
	}
	if int64(len(r.Body)) > r.MaxBody {
		return out, ErrBodyTooLarge
	}
	if r.MaxResponse == 0 {
		r.MaxResponse = 32 << 10
	}
	if r.MaxResponse > maxResponseBytes {
		r.MaxResponse = maxResponseBytes
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		return out, err
	}
	q := u.Query()
	for k, v := range r.Query {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, r.Method, u.String(), bytes.NewReader(r.Body))
	if err != nil {
		return out, err
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	if err = ApplyAuth(req, r.Auth); err != nil {
		return out, err
	}
	for k, v := range map[string]string{"X-Run-Id": r.RunID, "X-Trigger": r.Trigger, "X-Triggered-By": r.TriggeredBy, "Idempotency-Key": r.IdempotencyKey, "X-Simulated-Time": r.SimulatedTime} {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	cl, err := c.Client(ctx, now)
	if err != nil {
		return out, err
	}
	start := time.Now()
	resp, err := cl.Do(req)
	out.Duration = time.Since(start)
	if err != nil {
		if errors.Is(err, ErrRedirect) {
			return out, ErrRedirect
		}
		return out, err
	}
	defer resp.Body.Close()
	out.Status = resp.StatusCode
	out.Headers = map[string]string{}
	n := 0
	for k, v := range resp.Header {
		if n >= 32 {
			break
		}
		val := strings.Join(v, ",")
		if len(val) > 1024 {
			val = val[:1024]
		}
		out.Headers[k] = val
		n++
	}
	lr := io.LimitReader(resp.Body, r.MaxResponse+1)
	out.Body, err = io.ReadAll(lr)
	if int64(len(out.Body)) > r.MaxResponse {
		out.Body = out.Body[:r.MaxResponse]
		out.Truncated = true
	}
	return out, err
}
func TemplateVars(runID, jobKey string, now time.Time, simulated string) map[string]string {
	return map[string]string{"date": now.UTC().Format("2006-01-02"), "now": now.UTC().Format(time.RFC3339), "unix": strconv.FormatInt(now.Unix(), 10), "run_id": runID, "job_key": jobKey, "simulated_time": simulated}
}
