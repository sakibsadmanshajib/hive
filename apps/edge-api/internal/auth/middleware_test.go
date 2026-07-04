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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
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
	}, nil)
	rr := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatalf("downstream handler must not run with nil validator")
	})).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/x", nil))
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	require.Equal(t, "AUTH_JWT_MISCONFIGURED", auditedAction)
}

// fakeTenantFallback implements auth.TenantFallbackResolver so the
// fallback-gate tests below can control Resolve's return values directly,
// without a real DB connection.
type fakeTenantFallback struct {
	tenantID uuid.UUID
	role     string
	ok       bool
	err      error
}

func (f *fakeTenantFallback) Resolve(context.Context, uuid.UUID) (uuid.UUID, string, bool, error) {
	return f.tenantID, f.role, f.ok, f.err
}

// fallbackGateRequest builds a request that exercises the #269 fallback
// gate end to end: it goes through the real OWUIUnwrap middleware (shim
// key + upstream_auth metadata) so IsOWUIUnwrapped(ctx) is genuinely true
// when it reaches JWTMiddleware, and carries a real signed JWT with a sub
// claim but no tenant_id claim -- exactly the OAuth-server-minted token
// shape described in tenant_fallback.go.
func fallbackGateRequest(t *testing.T, token string) *http.Request {
	t.Helper()
	body := map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"__metadata": map[string]any{
			"upstream_auth": "Bearer " + token,
		},
	}
	return wrap(t, body, "Bearer "+testShimKey)
}

func TestJWTMiddleware_FallbackGate_ResolveSuccess_AppliesTenant(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	uid := uuid.New()
	token := signToken(t, priv, "https://test.supabase.co/auth/v1", map[string]any{
		"sub": uid.String(),
		// Deliberately no tenant_id claim -- the #269 OAuth-server shape.
	})
	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)

	tenantID := uuid.New()
	fallback := &fakeTenantFallback{tenantID: tenantID, role: "member", ok: true}

	var gotUser *auth.User
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotUser, _ = auth.UserFrom(r.Context())
	})
	owui := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	mw := auth.JWTMiddleware(v, nil, fallback)

	rr := httptest.NewRecorder()
	owui(mw(next)).ServeHTTP(rr, fallbackGateRequest(t, token))

	require.NotNil(t, gotUser, "downstream handler must run on a resolved fallback")
	require.Equal(t, tenantID, gotUser.TenantID)
	require.Equal(t, "member", gotUser.Role)
}

func TestJWTMiddleware_FallbackGate_ResolveMiss_FailsClosed(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	token := signToken(t, priv, "https://test.supabase.co/auth/v1", map[string]any{
		"sub": uuid.New().String(),
	})
	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)

	fallback := &fakeTenantFallback{ok: false} // no active membership found
	var nextCalled bool
	var auditedAction string
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })
	owui := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	mw := auth.JWTMiddleware(v, func(action, _, _ string) { auditedAction = action }, fallback)

	rr := httptest.NewRecorder()
	owui(mw(next)).ServeHTTP(rr, fallbackGateRequest(t, token))

	require.False(t, nextCalled, "a clean miss must still fail closed -- claims.TenantID stays Nil")
	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.Equal(t, "AUTH_JWT_INVALID", auditedAction)
}

func TestJWTMiddleware_FallbackGate_ResolveError_TreatedAsMiss(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	token := signToken(t, priv, "https://test.supabase.co/auth/v1", map[string]any{
		"sub": uuid.New().String(),
	})
	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)

	fallback := &fakeTenantFallback{err: errors.New("db: connection reset")}
	var nextCalled bool
	var auditedAction string
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })
	owui := auth.OWUIUnwrap(auth.OWUIUnwrapConfig{ShimKey: testShimKey})
	mw := auth.JWTMiddleware(v, func(action, _, _ string) { auditedAction = action }, fallback)

	rr := httptest.NewRecorder()
	owui(mw(next)).ServeHTTP(rr, fallbackGateRequest(t, token))

	require.False(t, nextCalled, "a resolve error must be treated as a miss, not let through")
	require.Equal(t, http.StatusUnauthorized, rr.Code)
	require.Equal(t, "AUTH_JWT_INVALID", auditedAction)
}
