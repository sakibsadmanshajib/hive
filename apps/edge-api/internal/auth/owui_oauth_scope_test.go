package auth_test

// Regression guard for #782.
//
// OWUIUnwrap fails closed when a shim-key /v1/chat/completions request carries
// no `__metadata.upstream_auth`, which is the correct response to a missing
// per-user token. What made that fail-closed catastrophic in production was
// upstream Open WebUI destroying the token supply on a schedule:
//
//   - Supabase issues OAuth access tokens with a 3600 second lifetime.
//   - OAuthManager.get_oauth_token refreshes once `now + 5 minutes` reaches
//     `expires_at`, so the refresh path is first entered roughly 55 minutes
//     into a signed-in session.
//   - _perform_token_refresh returns None on its first guard when the stored
//     session has no `refresh_token`.
//   - get_oauth_token then calls delete_session_by_id and the OAuth session is
//     gone. `__oauth_token__` is empty from then on, hive_jwt_forward injects
//     nothing, and every completion 401s until the user signs in again.
//
// Two inputs to that chain this repository controls, and both are required.
//
// First, the scope on the authorize request. Supabase advertises
// `offline_access` in scopes_supported and `refresh_token` in
// grant_types_supported, so asking for it is what makes a refresh possible at
// all. Drop `offline_access` and the deployment is guaranteed to lock every
// user out roughly 55 minutes after they sign in.
//
// Second, the token endpoint authentication method. _perform_token_refresh
// does not go through authlib: it hand builds the refresh POST and always
// sends client_id and client_secret in the form body, which is
// client_secret_post. authlib, which performs the authorization code exchange,
// defaults to client_secret_basic. Supabase enforces the registered method
// exactly, so leaving this unset means the refresh is rejected with 400
// invalid_credentials even when a refresh token exists, and the session is
// destroyed anyway. Verified live: the identical refresh token was refused
// with client_secret_post and accepted with client_secret_basic against a
// basic-registered client.
//
// Fixing only one of the two leaves the defect in place, which is why both are
// asserted here.
//
// This lives here, next to the middleware that emits the customer-facing
// error, because that middleware is where the defect surfaces and where the
// next person will start reading.
//
// Why the e2e suite could not catch this: every phase-19 chat spec, including
// the multi-turn one at apps/web-console/e2e/phase-19/owui/
// 02-chat-multi-turn.spec.ts, runs all of its turns inside one short session.
// Nothing holds a session open past the access token lifetime, so no test can
// reach the refresh path. A test that sends one message, or several messages
// in one minute, passes against the broken build. That is why this ships as a
// configuration invariant instead of another chat spec.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// requiredOAuthScope is the scope whose absence caused #782.
const requiredOAuthScope = "offline_access"

// requiredTokenEndpointAuthMethod is the only dialect Open WebUI's refresh
// path can produce, so the authorization code exchange has to match it.
const requiredTokenEndpointAuthMethod = "client_secret_post"

// TestOWUIOAuthScopesRequestOfflineAccess asserts that every Open WebUI OIDC
// scope declaration under deploy/ asks for a refresh token.
//
// It scans all deploy YAML rather than one known line so a second compose
// file, override, or profile cannot reintroduce the defect by copying the old
// value into a new place.
func TestOWUIOAuthScopesRequestOfflineAccess(t *testing.T) {
	declarations := envDeclarations(t, "OAUTH_SCOPES")
	require.NotEmpty(t, declarations,
		"expected at least one OAUTH_SCOPES declaration under deploy/; if Open "+
			"WebUI's OIDC wiring moved, move this guard with it rather than "+
			"deleting it")

	for _, d := range declarations {
		scopes := strings.Fields(d.value)
		require.Contains(t, scopes, requiredOAuthScope,
			"%s:%d declares OAUTH_SCOPES %q without %q. Without it Supabase "+
				"returns no refresh token, Open WebUI deletes the OAuth session at "+
				"its first refresh attempt, and every chat completion 401s about "+
				"55 minutes after sign-in until the user signs in again (#782)",
			d.path, d.line, d.value, requiredOAuthScope)

		// A refresh token with no identity scope would authenticate nobody.
		require.Contains(t, scopes, "openid",
			"%s:%d declares OAUTH_SCOPES %q without openid; the unwrap middleware "+
				"needs an OIDC identity token exchange, not a bare OAuth grant",
			d.path, d.line, d.value)
	}
}

// TestOWUIOAuthUsesRefreshCompatibleClientAuth asserts the deployment pins the
// token endpoint authentication method to the one Open WebUI's refresh path
// can actually speak.
//
// Unset is a failure, not a default: authlib falls back to
// client_secret_basic, which the refresh POST can never match, so a session
// with a perfectly good refresh token is still destroyed at the first refresh.
func TestOWUIOAuthUsesRefreshCompatibleClientAuth(t *testing.T) {
	declarations := envDeclarations(t, "OAUTH_TOKEN_ENDPOINT_AUTH_METHOD")
	require.NotEmpty(t, declarations,
		"expected OAUTH_TOKEN_ENDPOINT_AUTH_METHOD to be declared under deploy/. "+
			"Leaving it unset lets authlib default to client_secret_basic while "+
			"Open WebUI's refresh always sends client_secret_post, which Supabase "+
			"rejects with 400 invalid_credentials, destroying the session (#782)")

	for _, d := range declarations {
		require.Equal(t, requiredTokenEndpointAuthMethod, d.value,
			"%s:%d sets OAUTH_TOKEN_ENDPOINT_AUTH_METHOD to %q. Open WebUI's "+
				"_perform_token_refresh only ever sends the client credentials in "+
				"the form body, so this and the Supabase client registration must "+
				"both be %q or every token refresh fails (#782)",
			d.path, d.line, d.value, requiredTokenEndpointAuthMethod)
	}
}

type scopeDeclaration struct {
	path  string
	line  int
	value string
}

// envDeclarations returns every assignment of key found in YAML under deploy/,
// with its file and line for a legible failure message. Scanning all deploy
// YAML rather than one known line means a second compose file, override or
// profile cannot reintroduce the defect by copying an old value elsewhere.
func envDeclarations(t *testing.T, key string) []scopeDeclaration {
	t.Helper()

	root := repoRootForAuth(t)
	var found []scopeDeclaration

	err := filepath.WalkDir(filepath.Join(root, "deploy"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".yml" && ext != ".yaml" {
			return nil
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			prefix, rawValue, ok := strings.Cut(trimmed, key+":")
			if !ok || prefix != "" {
				continue
			}
			found = append(found, scopeDeclaration{
				path:  rel,
				line:  i + 1,
				value: strings.Trim(strings.TrimSpace(rawValue), `"'`),
			})
		}
		return nil
	})
	require.NoError(t, err)

	return found
}

func repoRootForAuth(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)

	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir
		}
		if parent := filepath.Dir(dir); parent == dir {
			t.Fatalf("could not find repository root from %s", wd)
		}
	}
}
