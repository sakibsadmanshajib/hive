package auth_test

// The inert-token invariant.
//
// public.custom_access_token_hook no longer refuses to mint a token for a user
// with no ACTIVE public.tenant_users row. It issues a valid token that simply
// carries no tenant_id claim, so first sign-in stops returning HTTP 500 and the
// user can reach the one endpoint that provisions them.
//
// The whole safety of that change rests on such a token being inert: it must
// authenticate the user and grant nothing. These tests pin the edge-api half of
// that invariant at the only place that populates the request principal, so a
// future refactor cannot quietly turn "no tenant claim" into "no tenant filter".
//
// Every form the absence can take is covered, because the validator is
// deliberately permissive at the parse layer (a malformed claim yields a
// zero-value UUID rather than a parse error) and the rejection therefore has to
// happen in the middleware.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// TestParseYieldsNilTenantForEveryAbsentForm documents the parse-layer
// behaviour the middleware assertions below depend on. None of these raise a
// parse error, which is why a middleware guard is required rather than optional.
func TestParseYieldsNilTenantForEveryAbsentForm(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)

	uid := uuid.New()
	cases := []struct {
		name  string
		extra map[string]any
	}{
		{
			// What the fixed hook actually emits for a membership-less user.
			name:  "claim absent",
			extra: map[string]any{"sub": uid.String(), "aud": "authenticated"},
		},
		{
			name:  "claim empty string",
			extra: map[string]any{"sub": uid.String(), "aud": "authenticated", "tenant_id": ""},
		},
		{
			name:  "claim explicit null",
			extra: map[string]any{"sub": uid.String(), "aud": "authenticated", "tenant_id": nil},
		},
		{
			name:  "claim unparseable",
			extra: map[string]any{"sub": uid.String(), "aud": "authenticated", "tenant_id": "not-a-uuid"},
		},
		{
			// The all-zero UUID parses successfully and must still land on
			// uuid.Nil, so it cannot be used as a sentinel that slips past a
			// naive "parsed successfully" test.
			name:  "claim all-zero uuid",
			extra: map[string]any{"sub": uid.String(), "aud": "authenticated", "tenant_id": uuid.Nil.String()},
		},
		{
			name:  "claim wrong json type",
			extra: map[string]any{"sub": uid.String(), "aud": "authenticated", "tenant_id": 42},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token := signToken(t, priv, "https://test.supabase.co/auth/v1", tc.extra)
			claims, err := v.Parse(context.Background(), token)
			require.NoError(t, err, "the parse layer is permissive by design")
			require.Equal(t, uuid.Nil, claims.TenantID,
				"every absent form must collapse to uuid.Nil, never to a usable tenant")
			require.Equal(t, uid, claims.Sub, "the principal itself must still parse")
		})
	}
}

// TestJWTMiddlewareRejectsTokenWithoutTenantClaim is the load-bearing
// assertion. A signed, unexpired, correctly-audienced token with a valid
// subject and no tenant claim must be refused before any handler runs, and the
// refusal must be a denial rather than an unfiltered pass.
func TestJWTMiddlewareRejectsTokenWithoutTenantClaim(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		extra map[string]any
	}{
		{"absent", map[string]any{"sub": uuid.NewString(), "aud": "authenticated", "email": "new@example.com"}},
		{"empty", map[string]any{"sub": uuid.NewString(), "aud": "authenticated", "tenant_id": ""}},
		{"all-zero", map[string]any{"sub": uuid.NewString(), "aud": "authenticated", "tenant_id": uuid.Nil.String()}},
		{"unparseable", map[string]any{"sub": uuid.NewString(), "aud": "authenticated", "tenant_id": "../../etc/passwd"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			// tenantFallback nil: the #269 Open WebUI DB recovery path must not
			// be consulted for an ordinary JWT, and with no fallback wired
			// there is nothing that could rescue the missing claim.
			handler := auth.JWTMiddleware(v, nil, nil)(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					reached = true
					w.WriteHeader(http.StatusOK)
				}))

			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			req.Header.Set("Authorization", "Bearer "+
				signToken(t, priv, "https://test.supabase.co/auth/v1", tc.extra))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusUnauthorized, rec.Code,
				"a tenant-less token must be denied, not passed through unfiltered")
			require.False(t, reached,
				"no handler may run for a tenant-less token: reaching one is the "+
					"privilege-escalation shape this test exists to prevent")
			require.Contains(t, rec.Body.String(), "UNAUTHENTICATED")
			// The refusal must not disclose why in terms a prober can use to
			// enumerate tenant state.
			require.NotContains(t, rec.Body.String(), "tenant")
		})
	}
}

// TestJWTMiddlewareAcceptsTokenWithTenantClaim is the counterpart that keeps
// the guard honest. If the deny-side tests above ever pass because the
// middleware rejects everything, this one fails.
func TestJWTMiddlewareAcceptsTokenWithTenantClaim(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)

	tenantID := uuid.New()
	userID := uuid.New()

	var seen *auth.User
	handler := auth.JWTMiddleware(v, nil, nil)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFrom(r.Context())
			require.True(t, ok, "a tenanted token must populate the principal")
			seen = user
			w.WriteHeader(http.StatusOK)
		}))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+
		signToken(t, priv, "https://test.supabase.co/auth/v1", map[string]any{
			"sub":       userID.String(),
			"aud":       "authenticated",
			"tenant_id": tenantID.String(),
			"role":      "MEMBER",
		}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, seen)
	require.Equal(t, tenantID, seen.TenantID)
	require.Equal(t, userID, seen.ID)
}
