package signupguard

import (
	"context"
	"encoding/json"
	"errors"
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

func TestClientIPExtraction(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		remote  string
		want    string
	}{
		{"cf-connecting-ip wins", map[string]string{"CF-Connecting-IP": "9.9.9.9", "X-Forwarded-For": "8.8.8.8"}, "1.1.1.1:5", "9.9.9.9"},
		{"x-forwarded-for first hop", map[string]string{"X-Forwarded-For": "8.8.8.8, 7.7.7.7"}, "1.1.1.1:5", "8.8.8.8"},
		{"remote addr fallback", nil, "1.1.1.1:5555", "1.1.1.1"},
		{"remote addr no port", nil, "1.1.1.1", "1.1.1.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/x", nil)
			req.RemoteAddr = c.remote
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			if got := clientIP(req); got != c.want {
				t.Fatalf("clientIP = %q, want %q", got, c.want)
			}
		})
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
