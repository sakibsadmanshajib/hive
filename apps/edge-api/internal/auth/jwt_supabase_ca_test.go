package auth_test

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// The self-hosted (enterprise) profile serves its JWKS through an in-stack
// Caddy holding a private CA's certificate, because edge-api refuses a plain
// http JWKS URL and no public authority can vouch for a compose service name.
// SUPABASE_JWKS_CA_FILE names that authority. These tests pin the two halves
// of that behaviour: the fetch stays verified (an unknown authority is still
// rejected), and a named authority is actually honoured.

const caTestIssuer = "https://selfhosted.test/auth/v1"

func privateCAJWKSServer(t *testing.T, jwksJSON []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwksJSON)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeServerCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	encoded := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: srv.Certificate().Raw,
	})
	require.NotNil(t, encoded)
	require.NoError(t, os.WriteFile(path, encoded, 0o600))
	return path
}

func TestJWTValidator_UnknownAuthority_RejectedWithoutCAFile(t *testing.T) {
	_, _, jwksJSON := newTestKey(t)
	srv := privateCAJWKSServer(t, jwksJSON)

	// No CA file: the JWKS host presents a certificate no system root
	// signed, so the initial refresh must fail and the process must not
	// come up believing it can validate tokens.
	_, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  caTestIssuer,
		JWKSURL: srv.URL + "/jwks",
	})
	require.Error(t, err)
}

func TestJWTValidator_PrivateCAFile_ValidatesToken(t *testing.T) {
	priv, _, jwksJSON := newTestKey(t)
	srv := privateCAJWKSServer(t, jwksJSON)
	caPath := writeServerCA(t, srv)

	uid := uuid.New()
	token := signToken(t, priv, caTestIssuer, map[string]any{
		"sub":   uid.String(),
		"email": "ada@office.example",
		"aud":   "authenticated",
		"role":  "authenticated",
	})

	v, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  caTestIssuer,
		JWKSURL: srv.URL + "/jwks",
		CAFile:  caPath,
	})
	require.NoError(t, err)

	claims, err := v.Parse(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, uid, claims.Sub)
}

func TestJWTValidator_CAFileMissing_FailsClosed(t *testing.T) {
	_, _, jwksJSON := newTestKey(t)
	srv := privateCAJWKSServer(t, jwksJSON)

	// A named-but-absent CA file must be fatal. Falling back to the system
	// pool would turn a deployment mistake into an outage at the first
	// token instead of at boot.
	_, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  caTestIssuer,
		JWKSURL: srv.URL + "/jwks",
		CAFile:  filepath.Join(t.TempDir(), "absent.pem"),
	})
	require.ErrorContains(t, err, "read jwks ca file")
}

func TestJWTValidator_CAFileWithoutCertificate_FailsClosed(t *testing.T) {
	_, _, jwksJSON := newTestKey(t)
	srv := privateCAJWKSServer(t, jwksJSON)

	path := filepath.Join(t.TempDir(), "not-a-cert.pem")
	require.NoError(t, os.WriteFile(path, []byte("this is not a certificate\n"), 0o600))

	_, err := auth.NewSupabaseJWTValidator(context.Background(), auth.SupabaseJWTConfig{
		Issuer:  caTestIssuer,
		JWKSURL: srv.URL + "/jwks",
		CAFile:  path,
	})
	require.ErrorContains(t, err, "holds no certificate")
}
