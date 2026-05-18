package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

func TestJWTValidator_ValidToken_PopulatesContext(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	tid := uuid.New()
	uid := uuid.New()
	token := signToken(t, priv, "https://test.supabase.co/auth/v1", map[string]any{
		"sub":       uid.String(),
		"email":     "ada@office.example",
		"aud":       "authenticated",
		"tenant_id": tid.String(),
		"role":      "ADMIN",
	})

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)

	claims, err := v.Parse(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, uid, claims.Sub)
	require.Equal(t, tid, claims.TenantID)
	// Roles are normalized to lowercase at the trust boundary so the
	// downstream authz.Role policy table (keyed lowercase) matches
	// whether the upstream token emits "ADMIN" or "admin".
	require.Equal(t, "admin", claims.Role)
	require.Equal(t, "ada@office.example", claims.Email)
}

func TestJWTValidator_ExpiredToken_ReturnsErrExpired(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	token := signTokenWithExp(t, priv, "https://test.supabase.co/auth/v1", time.Now().Add(-time.Hour))

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)
	_, err = v.Parse(context.Background(), token)
	require.ErrorIs(t, err, auth.ErrJWTExpired)
}

func TestJWTValidator_BadIssuer_Rejected(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	token := signToken(t, priv, "https://attacker.example/auth/v1", nil)

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)
	_, err = v.Parse(context.Background(), token)
	require.Error(t, err)
}

func TestJWTValidator_BadAudience_Rejected(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := jwksServer(t, jwksJSON)
	defer srv.Close()

	// Sign a token whose audience does not match the validator's required aud.
	token := signTokenWithExpAud(t, priv, "https://test.supabase.co/auth/v1", time.Now().Add(time.Hour), "not-authenticated")

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  "https://test.supabase.co/auth/v1",
		JWKSURL: srv.URL + "/jwks",
	})
	require.NoError(t, err)
	_, err = v.Parse(context.Background(), token)
	require.Error(t, err)
}

func TestJWTValidator_MissingConfig_Rejected(t *testing.T) {
	_, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{})
	require.Error(t, err)
}

// Test helpers ------------------------------------------------------------

func newTestKey(t *testing.T) (*rsa.PrivateKey, jwk.Set, []byte) {
	t.Helper()
	raw, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	priv, err := jwk.FromRaw(raw)
	require.NoError(t, err)
	require.NoError(t, priv.Set(jwk.KeyIDKey, "kid-test"))
	require.NoError(t, priv.Set(jwk.AlgorithmKey, jwa.RS256.String()))
	pub, err := priv.PublicKey()
	require.NoError(t, err)
	set := jwk.NewSet()
	require.NoError(t, set.AddKey(pub))
	raw2, err := json.Marshal(set)
	require.NoError(t, err)
	return raw, set, raw2
}

func jwksServer(t *testing.T, jwksJSON []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksJSON)
	})
	return httptest.NewServer(mux)
}

func signToken(t *testing.T, raw *rsa.PrivateKey, iss string, extra map[string]any) string {
	return signTokenWithExp(t, raw, iss, time.Now().Add(time.Hour), extra)
}

func signTokenWithExp(t *testing.T, raw *rsa.PrivateKey, iss string, exp time.Time, extra ...map[string]any) string {
	return signTokenWithExpAud(t, raw, iss, exp, "authenticated", extra...)
}

func signTokenWithExpAud(t *testing.T, raw *rsa.PrivateKey, iss string, exp time.Time, aud string, extra ...map[string]any) string {
	t.Helper()
	b := jwt.NewBuilder().
		Issuer(iss).
		Audience([]string{aud}).
		IssuedAt(time.Now()).
		Expiration(exp)
	for _, m := range extra {
		for k, v := range m {
			b = b.Claim(k, v)
		}
	}
	tok, err := b.Build()
	require.NoError(t, err)

	signKey, err := jwk.FromRaw(raw)
	require.NoError(t, err)
	require.NoError(t, signKey.Set(jwk.KeyIDKey, "kid-test"))

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, signKey))
	require.NoError(t, err)
	return fmt.Sprintf("%s", signed)
}
