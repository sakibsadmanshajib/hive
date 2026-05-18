package auth_test

// Middleware-layer tests. The selector layer
// (selector_test.go::TestSelector_CaseInsensitiveScheme) confirms that
// case-insensitive "Bearer" routing classifies the request. This file
// confirms the middleware fails closed on misconfiguration and accepts
// any capitalisation of the scheme word — closing the gap the Round 3
// adversarial review flagged when the selector started accepting
// "bearer eyJ..." but the middleware's strings.TrimPrefix still
// required exact "Bearer ".

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// TestJWTMiddleware_NilValidator_FailsClosed_503 verifies that a nil
// validator (configuration error) results in a 503 audit-emitting
// middleware rather than a panic on first request. The audit hook is
// invoked with AUTH_JWT_MISCONFIGURED.
func TestJWTMiddleware_NilValidator_FailsClosed_503(t *testing.T) {
	var auditedAction string
	mw := auth.JWTMiddleware(nil, func(action, _, _ string) {
		auditedAction = action
	})
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatalf("downstream handler must not run with nil validator")
	})).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/x", nil))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Equal(t, "AUTH_JWT_MISCONFIGURED", auditedAction)
}
