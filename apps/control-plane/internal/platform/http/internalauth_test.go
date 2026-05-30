package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("reached"))
	})
}

func TestRequireInternalTokenRejectsMissingToken(t *testing.T) {
	h := RequireInternalToken("s3cret", okHandler())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/internal/apikeys/resolve", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when X-Internal-Token absent, got %d", rr.Code)
	}
	if rr.Body.String() == "reached" {
		t.Fatal("inner handler must not run for an unauthenticated internal request")
	}
}

func TestRequireInternalTokenRejectsWrongToken(t *testing.T) {
	h := RequireInternalToken("s3cret", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/internal/apikeys/resolve", nil)
	req.Header.Set("X-Internal-Token", "wrong")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d", rr.Code)
	}
}

func TestRequireInternalTokenAcceptsCorrectToken(t *testing.T) {
	h := RequireInternalToken("s3cret", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/internal/apikeys/resolve", nil)
	req.Header.Set("X-Internal-Token", "s3cret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "reached" {
		t.Fatalf("expected inner handler to run for correct token, got %d %q", rr.Code, rr.Body.String())
	}
}

// Fail closed: when no token is configured, every internal request is rejected
// (even one with an empty header) rather than leaving the endpoints open.
func TestRequireInternalTokenFailsClosedWhenUnconfigured(t *testing.T) {
	h := RequireInternalToken("", okHandler())

	// No header.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/internal/apikeys/resolve", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (fail closed) when token unconfigured, got %d", rr.Code)
	}

	// Empty header must not match an empty configured token.
	req := httptest.NewRequest(http.MethodPost, "/internal/apikeys/resolve", nil)
	req.Header.Set("X-Internal-Token", "")
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for empty header vs empty token, got %d", rr2.Code)
	}
}
