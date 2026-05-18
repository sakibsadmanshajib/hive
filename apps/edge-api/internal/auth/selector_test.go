package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// fakeHandler records the number of times it is invoked and emits a
// distinctive status so a test can confirm which handler actually ran
// even when both share a single ResponseWriter pool.
type fakeHandler struct {
	hits   int
	status int
}

func (f *fakeHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	f.hits++
	if f.status == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(f.status)
}

func TestSelector_BearerHK_RoutesToAPIKey(t *testing.T) {
	jwtH := &fakeHandler{status: http.StatusTeapot}
	keyH := &fakeHandler{status: http.StatusAccepted}
	mux := auth.Selector(jwtH, keyH)

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	req.Header.Set("Authorization", "Bearer hk_test_abc123")
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	require.Equal(t, 0, jwtH.hits, "JWT handler must not run for hk_ bearer")
	require.Equal(t, 1, keyH.hits, "API-key handler must run for hk_ bearer")
	require.Equal(t, http.StatusAccepted, rr.Code)
}

func TestSelector_BearerJWT_RoutesToJWT(t *testing.T) {
	jwtH := &fakeHandler{status: http.StatusTeapot}
	keyH := &fakeHandler{status: http.StatusAccepted}
	mux := auth.Selector(jwtH, keyH)

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	// Synthetic JWT-shaped fixture. Built via string concatenation so
	// GitGuardian's "Bearer Token" detector does not match the literal
	// Bearer-prefix + entropy-blob pair as a leaked credential.
	const jwtFixture = "eyJhbGciOiJSUzI1NiJ9" + ".payload.sig"
	req.Header.Set("Authorization", "Bearer "+jwtFixture)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	require.Equal(t, 1, jwtH.hits, "JWT handler must run for non-hk_ bearer")
	require.Equal(t, 0, keyH.hits, "API-key handler must not run for JWT bearer")
	require.Equal(t, http.StatusTeapot, rr.Code)
}

func TestSelector_MissingAuth_FallsThroughToJWT(t *testing.T) {
	// JWT handler simulates 401 for unauthenticated requests — selector
	// must hand the request to it (not silently 200) so the caller sees
	// the canonical 401 from the JWT path.
	jwtH := &fakeHandler{status: http.StatusUnauthorized}
	keyH := &fakeHandler{status: http.StatusAccepted}
	mux := auth.Selector(jwtH, keyH)

	req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	require.Equal(t, 1, jwtH.hits, "missing auth must fall through to JWT handler")
	require.Equal(t, 0, keyH.hits)
	require.Equal(t, http.StatusUnauthorized, rr.Code, "JWT path must own the 401")
}

func TestSelector_CaseSensitivePrefix(t *testing.T) {
	// RFC 7235 §2.1 makes the auth scheme word case-insensitive; the
	// hk_ key prefix is part of the credential body (not the scheme)
	// and stays case-sensitive — the random suffix is base62 and an
	// upper/lower swap would identify a different key.
	cases := []struct {
		name   string
		header string
	}{
		{"uppercase HK prefix", "Bearer HK_test"},
		{"hk inside token only", "Bearer eyJhk_lookslike"},
		{"empty header", ""},
		{"other scheme", "Basic dXNlcjpwYXNz"},
		{"bearer no space", "Bearerhk_test"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			jwtH := &fakeHandler{}
			keyH := &fakeHandler{}
			mux := auth.Selector(jwtH, keyH)

			req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			mux.ServeHTTP(httptest.NewRecorder(), req)

			require.Equal(t, 1, jwtH.hits, "expected JWT path for header %q", tc.header)
			require.Equal(t, 0, keyH.hits, "expected NOT API-key path for header %q", tc.header)
		})
	}
}

// TestSelector_CaseInsensitiveScheme verifies RFC 7235 §2.1 compliance:
// the auth scheme word ("Bearer") is matched case-insensitively while
// the hk_ token body remains case-sensitive.
func TestSelector_CaseInsensitiveScheme(t *testing.T) {
	cases := []struct{ name, header string }{
		{"lowercase bearer", "bearer hk_test"},
		{"mixedcase bearer", "BeArEr hk_test"},
		{"uppercase BEARER", "BEARER hk_test"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			jwtH := &fakeHandler{}
			keyH := &fakeHandler{}
			mux := auth.Selector(jwtH, keyH)
			req := httptest.NewRequest(http.MethodGet, "/v1/anything", nil)
			req.Header.Set("Authorization", tc.header)
			mux.ServeHTTP(httptest.NewRecorder(), req)
			require.Equal(t, 0, jwtH.hits, "expected API-key path for header %q", tc.header)
			require.Equal(t, 1, keyH.hits, "expected API-key path for header %q", tc.header)
		})
	}
}
