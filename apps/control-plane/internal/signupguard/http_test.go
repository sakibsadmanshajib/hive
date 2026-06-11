package signupguard

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustBlocklist(t *testing.T) *Blocklist {
	t.Helper()
	bl, err := LoadDisposableBlocklist()
	if err != nil {
		t.Fatalf("LoadDisposableBlocklist: %v", err)
	}
	return bl
}

func postPrecheck(t *testing.T, h http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sign-up/precheck", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:54321"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newTestHandler(t *testing.T, opts ...func(*HandlerDeps)) *Handler {
	t.Helper()
	deps := HandlerDeps{
		Blocklist:   mustBlocklist(t),
		RateLimiter: NewRateLimiter(nil, RateLimitConfig{}),
		Turnstile:   NewTurnstileVerifier("", nil),
	}
	for _, o := range opts {
		o(&deps)
	}
	return NewHandler(deps)
}

func mustParseCIDRs(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, s := range cidrs {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatalf("ParseCIDR(%q): %v", s, err)
		}
		out = append(out, n)
	}
	return out
}

func TestPrecheckMethodNotAllowed(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sign-up/precheck", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET should be 405, got %d", rec.Code)
	}
}

func TestPrecheckCleanEmailPasses(t *testing.T) {
	h := newTestHandler(t)
	rec := postPrecheck(t, h, `{"email":"new.user@gmail.com"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("clean signup should be 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPrecheckDisposableBlocked(t *testing.T) {
	h := newTestHandler(t)
	rec := postPrecheck(t, h, `{"email":"x@mailinator.com"}`, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disposable email should be 403, got %d", rec.Code)
	}
	// Provider-blind: must not name the control that tripped.
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	msg := strings.ToLower(body["error"])
	for _, leak := range []string{"disposable", "mailinator", "blocklist", "captcha", "rate"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("error message leaked control detail %q: %q", leak, msg)
		}
	}
}

func TestPrecheckInvalidEmail(t *testing.T) {
	h := newTestHandler(t)
	rec := postPrecheck(t, h, `{"email":"not-an-email"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid email should be 400, got %d", rec.Code)
	}
}

func TestPrecheckMalformedBody(t *testing.T) {
	h := newTestHandler(t)
	rec := postPrecheck(t, h, `{not json`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body should be 400, got %d", rec.Code)
	}
}

func TestPrecheckRateLimited(t *testing.T) {
	inc := &fakeIncrementer{}
	h := newTestHandler(t, func(d *HandlerDeps) {
		d.RateLimiter = NewRateLimiter(inc, RateLimitConfig{Limit: 1, Window: time.Hour})
	})
	first := postPrecheck(t, h, `{"email":"a@gmail.com"}`, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first signup should pass, got %d", first.Code)
	}
	second := postPrecheck(t, h, `{"email":"b@gmail.com"}`, nil)
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second signup from same IP should be 429, got %d", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("429 response must carry Retry-After")
	}
}

func TestPrecheckRateLimiterFailClosed(t *testing.T) {
	inc := &fakeIncrementer{err: errors.New("redis down")}
	h := newTestHandler(t, func(d *HandlerDeps) {
		d.RateLimiter = NewRateLimiter(inc, RateLimitConfig{Limit: 5, Window: time.Hour, FailOpen: false})
	})
	rec := postPrecheck(t, h, `{"email":"a@gmail.com"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("backend down with fail-closed should be 429, got %d", rec.Code)
	}
}

func TestPrecheckCaptchaRequiredWhenEnabled(t *testing.T) {
	h := newTestHandler(t, func(d *HandlerDeps) {
		d.Turnstile = NewTurnstileVerifier("a-secret", nil)
	})
	// No captcha_token in body, captcha enabled -> reject (provider-blind 403).
	rec := postPrecheck(t, h, `{"email":"a@gmail.com"}`, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing captcha when enabled should be 403, got %d", rec.Code)
	}
}

func TestPrecheckMaxConcurrent(t *testing.T) {
	h := newTestHandler(t, func(d *HandlerDeps) {
		d.MaxConcurrent = 1
	})
	// Fill the semaphore manually.
	h.sem <- struct{}{}
	rec := postPrecheck(t, h, `{"email":"a@gmail.com"}`, nil)
	<-h.sem
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("saturated semaphore should be 429, got %d", rec.Code)
	}
}

// TestClientIPWithTrust verifies trusted-proxy CIDR logic.
func TestClientIPWithTrust(t *testing.T) {
	trusted := mustParseCIDRs(t, "10.0.0.0/8")
	none := []*net.IPNet{}

	cases := []struct {
		name    string
		headers map[string]string
		remote  string
		cidrs   []*net.IPNet
		want    string
	}{
		// Trusted peer: honour CF-Connecting-IP.
		{"trusted peer cf-connecting-ip", map[string]string{"CF-Connecting-IP": "9.9.9.9", "X-Forwarded-For": "8.8.8.8"}, "10.0.0.1:5", trusted, "9.9.9.9"},
		// Trusted peer: XFF rightmost untrusted hop.
		{"trusted peer xff rightmost-untrusted", map[string]string{"X-Forwarded-For": "1.2.3.4, 5.6.7.8, 10.0.0.2"}, "10.0.0.1:5", trusted, "5.6.7.8"},
		// Trusted peer: XFF all-trusted falls back to RemoteAddr host.
		{"trusted peer xff all-trusted fallback", map[string]string{"X-Forwarded-For": "10.0.0.5, 10.0.0.6"}, "10.0.0.1:5", trusted, "10.0.0.1"},
		// Untrusted peer: ignore CF-Connecting-IP (spoofed).
		{"untrusted peer ignores cf-connecting-ip", map[string]string{"CF-Connecting-IP": "9.9.9.9", "X-Forwarded-For": "8.8.8.8"}, "203.0.113.7:5", trusted, "203.0.113.7"},
		// Untrusted peer: ignore XFF (spoofed).
		{"untrusted peer ignores xff", map[string]string{"X-Forwarded-For": "8.8.8.8, 7.7.7.7"}, "203.0.113.7:5", trusted, "203.0.113.7"},
		// No trusted CIDRs: always RemoteAddr.
		{"no trusted cidrs uses remote addr", map[string]string{"CF-Connecting-IP": "9.9.9.9"}, "1.1.1.1:5", none, "1.1.1.1"},
		// Fallback cases.
		{"remote addr fallback", nil, "1.1.1.1:5555", none, "1.1.1.1"},
		{"remote addr no port", nil, "1.1.1.1", none, "1.1.1.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/x", nil)
			req.RemoteAddr = c.remote
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			if got := clientIPWithTrust(req, c.cidrs); got != c.want {
				t.Fatalf("clientIPWithTrust = %q, want %q", got, c.want)
			}
		})
	}
}

// TestPrecheckSpoofedHeaderFromUntrustedPeer verifies that a client supplying
// a CF-Connecting-IP header from an untrusted RemoteAddr is bucketed by
// RemoteAddr, not by the spoofed header value.
func TestPrecheckSpoofedHeaderFromUntrustedPeer(t *testing.T) {
	inc := &fakeIncrementer{}
	h := newTestHandler(t, func(d *HandlerDeps) {
		d.RateLimiter = NewRateLimiter(inc, RateLimitConfig{Limit: 1, Window: time.Hour})
		d.TrustedProxyCIDRs = []*net.IPNet{}
	})

	// First request: attacker sends CF-Connecting-IP from untrusted RemoteAddr.
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sign-up/precheck", strings.NewReader(`{"email":"a@gmail.com"}`))
	req1.RemoteAddr = "203.0.113.7:1234"
	req1.Header.Set("CF-Connecting-IP", "9.9.9.9")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should pass, got %d", rec1.Code)
	}

	// Second request: same RemoteAddr, different spoofed CF header.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sign-up/precheck", strings.NewReader(`{"email":"b@gmail.com"}`))
	req2.RemoteAddr = "203.0.113.7:5678"
	req2.Header.Set("CF-Connecting-IP", "1.2.3.4")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	// Must be 429 because 203.0.113.7 hit its limit, not because 9.9.9.9/1.2.3.4 did.
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from same untrusted RemoteAddr should be 429, got %d (spoofed header bypassed rate limit)", rec2.Code)
	}
}

// TestPrecheckTrustedProxyHonoursForwardedHeader verifies that when the
// RemoteAddr is inside the trusted CIDR list, the CF-Connecting-IP header is
// honoured for bucketing.
func TestPrecheckTrustedProxyHonoursForwardedHeader(t *testing.T) {
	_, trusted, _ := net.ParseCIDR("10.0.0.0/8")
	inc := &fakeIncrementer{}
	h := newTestHandler(t, func(d *HandlerDeps) {
		d.RateLimiter = NewRateLimiter(inc, RateLimitConfig{Limit: 1, Window: time.Hour})
		d.TrustedProxyCIDRs = []*net.IPNet{trusted}
	})

	// First request: trusted proxy 10.0.0.1 forwards real client 9.9.9.9.
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sign-up/precheck", strings.NewReader(`{"email":"a@gmail.com"}`))
	req1.RemoteAddr = "10.0.0.1:1234"
	req1.Header.Set("CF-Connecting-IP", "9.9.9.9")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request should pass, got %d", rec1.Code)
	}

	// Second request: same real client (9.9.9.9) via a different trusted proxy.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sign-up/precheck", strings.NewReader(`{"email":"b@gmail.com"}`))
	req2.RemoteAddr = "10.0.0.2:5678"
	req2.Header.Set("CF-Connecting-IP", "9.9.9.9")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	// Rate-limited by forwarded IP 9.9.9.9.
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request from same forwarded IP should be 429, got %d", rec2.Code)
	}
}

// audit sink stub to assert the handler records classifications without leaking.
type recordedAudit struct {
	actions []string
	details []map[string]string
}

func (r *recordedAudit) record(action string, detail map[string]string) {
	r.actions = append(r.actions, action)
	r.details = append(r.details, detail)
}

func TestPrecheckAuditOnBlock(t *testing.T) {
	rec := &recordedAudit{}
	h := newTestHandler(t, func(d *HandlerDeps) {
		d.AuditFunc = func(_ context.Context, action string, detail map[string]string) {
			rec.record(action, detail)
		}
	})
	_ = postPrecheck(t, h, `{"email":"x@mailinator.com"}`, nil)
	if len(rec.actions) == 0 {
		t.Fatal("expected an audit entry on disposable block")
	}
	// Audit detail must not echo the raw email or a provider name.
	for _, d := range rec.details {
		for _, v := range d {
			if strings.Contains(v, "mailinator.com") {
				t.Fatalf("audit detail leaked domain: %v", d)
			}
		}
	}
}
