package auth_test

// Regression guards for #782 and for the outage its own fix caused, #787.
//
// OWUIUnwrap fails closed when a shim-key /v1/chat/completions request carries
// no `__metadata.upstream_auth`, which is the correct response to a missing
// per-user token. What made that fail-closed catastrophic in production was
// upstream Open WebUI destroying the token supply on a schedule:
//
//   - The authorization server issues OAuth access tokens with a 3600 second
//     lifetime.
//   - OAuthManager.get_oauth_token refreshes once `now + 5 minutes` reaches
//     `expires_at`, so the refresh path is first entered roughly 55 minutes
//     into a signed-in session.
//   - _perform_token_refresh returns None when the refresh cannot be completed.
//   - get_oauth_token then calls delete_session_by_id and the OAuth session is
//     gone. `__oauth_token__` is empty from then on, hive_jwt_forward injects
//     nothing, and every completion 401s until the user signs in again.
//
// #787 read that chain as two broken inputs and fixed both. Only one of them
// was ever broken, and the other "fix" took chat sign-in down outright.
//
// The half that was never broken: the scope on the authorize request. #787
// added `offline_access` to OAUTH_SCOPES because hosted Supabase advertised it
// and OAuth convention says a refresh token needs it. The self-hosted GoTrue
// this stack cut over to does not advertise it, and does not need it either.
// Read at the pinned supabase/gotrue v2.189.0 rather than assumed:
// SupportedOAuthScopes in internal/models/oauth_scope.go is openid, profile,
// email, phone; and the authorization code grant in
// internal/api/oauthserver/handlers.go calls tokenService.IssueRefreshToken
// unconditionally and always puts refresh_token in the token response. So the
// refresh token exists with or without the scope, and asking for the scope is
// fatal: GoTrue 302s the authorize request straight back to the callback with
// error=invalid_request, no consent screen, no code, no session. Every user
// locked out at the front door instead of 55 minutes in.
//
// Which is why there is no assertion here that any particular scope is
// requested. That question cannot be answered from inside this repository at
// all, and answering it from convention is precisely what shipped #787: the
// set of legal scopes is a property of the deployed authorization server, and
// it changed underneath a compose file that did not. It is checked against the
// live discovery document instead, by scripts/check-oauth-scopes.py, on every
// pull request touching deploy YAML and again after every deploy.
// TestOWUIOAuthScopeGateIsWired below asserts that wiring still exists, since
// a gate that lives outside this test binary is a gate that can be deleted
// without turning anything in here red.
//
// The half that was genuinely broken, and that stays fixed: the client
// authentication on the refresh request. Open WebUI hand builds the refresh
// POST with the client credentials in the form body, which is
// client_secret_post, while authlib performs the authorization code exchange
// with client_secret_basic. The server enforces the registered method exactly,
// so a refresh token that exists is still refused and the session destroyed
// anyway. That is fixed inside the image, by the patch
// TestOWUIImageAppliesOAuthRefreshClientAuthPatch asserts is still wired into
// Dockerfile.open-webui, and its behaviour is covered by
// scripts/test_owui_oauth_client_auth.py.
//
// This lives here, next to the middleware that emits the customer-facing
// error, because that middleware is where the #782 defect surfaces and where
// the next person will start reading.
//
// Why no e2e test catches #782, and why these are configuration invariants
// instead of another chat spec: every phase-19 chat spec runs all of its turns
// inside one short session. The multi-turn one at
// apps/web-console/e2e/phase-19/owui/02-chat-multi-turn.spec.ts budgets 360
// seconds for the whole file and sends its two turns back to back. Nothing in
// the suite holds a session open past the access token lifetime or edits
// expires_at, so nothing reaches the refresh path at all. A test that sends
// one message, or several messages inside a minute, passes against a build
// that locks every user out an hour later. That is the shape of defect that
// ships.
//
// And why no e2e test caught #787 either, which is the harder one: the suite
// does exercise sign-in, but only against a deployment that already exists.
// Nothing runs between "the compose file changed" and "the deployment is
// rebuilt from it", so a scope the server rejects is not observable until the
// change is already live. That gap is what the live gate closes.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestOWUIOAuthScopesRequestOpenID asserts that every Open WebUI OIDC scope
// declaration under deploy/ still asks for an identity.
//
// This is the only thing about the scope value that is knowable offline.
// `openid` is what makes the exchange OIDC rather than a bare OAuth grant, and
// without it the server issues no id_token at all (GoTrue gates GenerateIDToken
// on exactly this scope), so the unwrap middleware has nobody to attribute a
// request to. Whether every OTHER scope in the list is one the deployed server
// will accept is checked against the live discovery document, not from here.
//
// It scans all deploy YAML rather than one known line so a second compose
// file, override, or profile cannot reintroduce a defect by copying a value
// into a new place.
func TestOWUIOAuthScopesRequestOpenID(t *testing.T) {
	declarations := envDeclarations(t, "OAUTH_SCOPES")
	require.NotEmpty(t, declarations,
		"expected at least one OAUTH_SCOPES declaration under deploy/; if Open "+
			"WebUI's OIDC wiring moved, move this guard with it rather than "+
			"deleting it")

	for _, d := range declarations {
		scopes := strings.Fields(d.value)
		require.Contains(t, scopes, "openid",
			"%s:%d declares OAUTH_SCOPES %q without openid; the unwrap middleware "+
				"needs an OIDC identity token exchange, not a bare OAuth grant",
			d.path, d.line, d.value)
	}
}

// TestOWUIOAuthScopeGateIsWired asserts the live capability check still exists
// and is still invoked from every place that is supposed to invoke it.
//
// Without this, the gate that catches #787 is three files nothing in this test
// binary references. Deleting the workflow step, or dropping the self-check
// from `make test-scripts`, would leave every Go test green while the only
// thing that can compare a requested scope against a real authorization
// server quietly stops running. That is the same failure shape as #787 itself:
// a repository fully in agreement with itself and wrong about the deployment.
//
// Three call sites, because they catch different directions of the same
// mismatch and no one of them subsumes another:
//
//   - the pull-request gate catches a scope added here that the server does
//     not advertise, which is #787;
//   - the deploy step catches a capability the server stops advertising while
//     this repository stands still, which is what the self-hosted cutover did;
//   - `make test-scripts` runs the comparator's own tests in a required check,
//     so the comparator cannot rot into something that returns 0 regardless.
func TestOWUIOAuthScopeGateIsWired(t *testing.T) {
	root := repoRootForAuth(t)

	script := filepath.Join(root, "scripts", "check-oauth-scopes.py")
	_, err := os.Stat(script)
	require.NoError(t, err,
		"scripts/check-oauth-scopes.py is the only check that compares a "+
			"requested OAuth scope against what the deployed authorization "+
			"server actually advertises (#787). It must exist.")

	for _, wiring := range []struct {
		file   string
		needle string
		why    string
	}{
		{
			file:   filepath.Join(".github", "workflows", "oauth-scope-gate.yml"),
			needle: "scripts/check-oauth-scopes.py",
			why: "the pull-request gate is what catches an unadvertised scope " +
				"BEFORE it is merged and deployed, which is the whole gap #787 " +
				"went through",
		},
		{
			file:   filepath.Join(".github", "workflows", "deploy-demo-box.yml"),
			needle: "scripts/check-oauth-scopes.py",
			why: "the post-deploy step is what catches the authorization server " +
				"dropping a capability while this repository stands still, which " +
				"is the direction the self-hosted cutover broke and which no " +
				"repository-only check can ever see",
		},
		{
			file:   "Makefile",
			needle: "test_check_oauth_scopes.py",
			why: "the comparator's own tests must run in a required check, or a " +
				"subset check that can no longer go red is indistinguishable " +
				"from one that passes unconditionally",
		},
	} {
		body, readErr := os.ReadFile(filepath.Join(root, wiring.file))
		require.NoError(t, readErr, "%s must exist: %s", wiring.file, wiring.why)
		// require.True rather than require.Contains: a failed Contains prints
		// the entire file into the test log and buries the explanation.
		require.True(t, strings.Contains(string(body), wiring.needle),
			"%s no longer references %q, so %s", wiring.file, wiring.needle, wiring.why)
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
