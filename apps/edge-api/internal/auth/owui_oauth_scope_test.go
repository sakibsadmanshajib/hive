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
//   - _perform_token_refresh returns None when the refresh cannot be completed.
//   - get_oauth_token then calls delete_session_by_id and the OAuth session is
//     gone. `__oauth_token__` is empty from then on, hive_jwt_forward injects
//     nothing, and every completion 401s until the user signs in again.
//
// Two independent inputs to that chain, and both were broken.
//
// First, the scope on the authorize request, which this file guards. Supabase
// advertises `offline_access` in scopes_supported and `refresh_token` in
// grant_types_supported, so asking for it is what makes a refresh possible at
// all. Without it Supabase issues no refresh token, _perform_token_refresh
// returns None on its very first guard, and the deployment locks every user
// out roughly 55 minutes after they sign in.
//
// Second, the client authentication on the refresh request. That one cannot be
// guarded from here because it is not configuration: Open WebUI hand builds
// the refresh POST with the client credentials in the form body, which is
// client_secret_post, while authlib performs the authorization code exchange
// with client_secret_basic. Supabase enforces the registered method exactly,
// so a refresh token that exists is still refused and the session destroyed
// anyway. That half is fixed inside the image, by the patch this file asserts
// is still wired into Dockerfile.open-webui, and its behaviour is covered by
// scripts/test_owui_oauth_client_auth.py.
//
// Fixing only one of the two leaves the defect in place, which is why both are
// asserted here.
//
// This lives here, next to the middleware that emits the customer-facing
// error, because that middleware is where the defect surfaces and where the
// next person will start reading.
//
// Why no e2e test catches this, and why these are configuration invariants
// instead of another chat spec: every phase-19 chat spec runs all of its turns
// inside one short session. The multi-turn one at
// apps/web-console/e2e/phase-19/owui/02-chat-multi-turn.spec.ts budgets 360
// seconds for the whole file and sends its two turns back to back. Nothing in
// the suite holds a session open past the access token lifetime or edits
// expires_at, so nothing reaches the refresh path at all. A test that sends
// one message, or several messages inside a minute, passes against a build
// that locks every user out an hour later. That is the shape of defect that
// ships.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// requiredOAuthScope is the scope whose absence caused #782.
const requiredOAuthScope = "offline_access"

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

// TestOWUIImageAppliesOAuthRefreshClientAuthPatch asserts the Open WebUI image
// still splices the refresh client-authentication fix in.
//
// The scope above is necessary and not sufficient: with a refresh token in
// hand, Supabase still refuses the refresh because Open WebUI sends the client
// credentials in the form body while the client is registered for the header.
// Nothing in the compose environment expresses that fix, so the only thing
// standing between this deployment and a silent return to hour-long sessions
// is that this build step keeps running. Deleting it would leave every test in
// this repository green.
func TestOWUIImageAppliesOAuthRefreshClientAuthPatch(t *testing.T) {
	root := repoRootForAuth(t)

	dockerfile := filepath.Join(root, "deploy", "docker", "Dockerfile.open-webui")
	body, err := os.ReadFile(dockerfile)
	require.NoError(t, err, "the Open WebUI image definition must exist")

	for _, needed := range []string{
		"owui-patches/hive_oauth_client_auth.py",
		"owui-patches/apply_oauth_client_auth_patch.py",
		"python3 /tmp/apply_oauth_client_auth_patch.py",
	} {
		// require.True rather than require.Contains: a failed Contains prints
		// the entire Dockerfile into the test log, which buries the message
		// that explains what broke.
		require.True(t, strings.Contains(string(body), needed),
			"deploy/docker/Dockerfile.open-webui no longer references %q, so the "+
				"image ships upstream's refresh POST, which sends the client "+
				"credentials in the form body. Supabase rejects that with 400 "+
				"invalid_credentials, Open WebUI deletes the OAuth session, and "+
				"chat dies roughly 55 minutes after sign-in (#782)", needed)
	}

	// The patch and the module it splices in must both still be present, or
	// the build step above fails only at image build time, which no unit test
	// lane reaches.
	for _, patchFile := range []string{
		filepath.Join(root, "deploy", "docker", "owui-patches", "hive_oauth_client_auth.py"),
		filepath.Join(root, "deploy", "docker", "owui-patches", "apply_oauth_client_auth_patch.py"),
	} {
		_, statErr := os.Stat(patchFile)
		require.NoError(t, statErr,
			"%s is referenced by Dockerfile.open-webui and must exist (#782)", patchFile)
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
