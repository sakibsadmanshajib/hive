package signupguard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newTurnstileStub(t *testing.T, handler func(form url.Values) turnstileResponse) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))
		resp := handler(form)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTurnstileDisabledWhenNoSecret(t *testing.T) {
	v := NewTurnstileVerifier("", nil)
	if v.Enabled() {
		t.Fatal("verifier with empty secret must report disabled")
	}
	// Disabled verifier accepts any (or empty) token without a network call.
	if err := v.Verify(context.Background(), "", "1.2.3.4"); err != nil {
		t.Fatalf("disabled verifier should accept, got %v", err)
	}
}

func TestTurnstileSuccess(t *testing.T) {
	var gotSecret, gotResponse, gotIP string
	srv := newTurnstileStub(t, func(form url.Values) turnstileResponse {
		gotSecret = form.Get("secret")
		gotResponse = form.Get("response")
		gotIP = form.Get("remoteip")
		return turnstileResponse{Success: true}
	})

	v := NewTurnstileVerifier("test-secret", srv.Client())
	v.verifyURL = srv.URL

	if !v.Enabled() {
		t.Fatal("verifier with secret must report enabled")
	}
	if err := v.Verify(context.Background(), "good-token", "9.9.9.9"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if gotSecret != "test-secret" || gotResponse != "good-token" || gotIP != "9.9.9.9" {
		t.Fatalf("siteverify form mismatch: secret=%q response=%q ip=%q", gotSecret, gotResponse, gotIP)
	}
}

func TestTurnstileMissingTokenWhenEnabled(t *testing.T) {
	srv := newTurnstileStub(t, func(url.Values) turnstileResponse {
		t.Fatal("siteverify must not be called when token is empty")
		return turnstileResponse{}
	})
	v := NewTurnstileVerifier("test-secret", srv.Client())
	v.verifyURL = srv.URL
	if err := v.Verify(context.Background(), "", "9.9.9.9"); err == nil {
		t.Fatal("expected error for missing token when enabled")
	}
}

func TestTurnstileFailure(t *testing.T) {
	srv := newTurnstileStub(t, func(url.Values) turnstileResponse {
		return turnstileResponse{Success: false, ErrorCodes: []string{"invalid-input-response"}}
	})
	v := NewTurnstileVerifier("test-secret", srv.Client())
	v.verifyURL = srv.URL
	err := v.Verify(context.Background(), "bad-token", "9.9.9.9")
	if err == nil {
		t.Fatal("expected failure for unsuccessful verification")
	}
	// Provider-blind: customer-visible error must not leak Cloudflare error codes.
	if strings.Contains(err.Error(), "invalid-input-response") {
		t.Fatalf("error leaked upstream code: %v", err)
	}
}

func TestTurnstileUpstreamError(t *testing.T) {
	// Server returns 500 -> verifier must fail closed (return error), not allow.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	v := NewTurnstileVerifier("test-secret", srv.Client())
	v.verifyURL = srv.URL
	if err := v.Verify(context.Background(), "tok", "9.9.9.9"); err == nil {
		t.Fatal("expected fail-closed error on upstream 5xx")
	}
}

func TestTurnstileContextDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(turnstileResponse{Success: true})
	}))
	t.Cleanup(srv.Close)
	v := NewTurnstileVerifier("test-secret", srv.Client())
	v.verifyURL = srv.URL
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	if err := v.Verify(ctx, "tok", "9.9.9.9"); err == nil {
		t.Fatal("expected error when context deadline exceeded")
	}
}
