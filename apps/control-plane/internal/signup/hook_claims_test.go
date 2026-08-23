package signup_test

// Tests for public.custom_access_token_hook, the Supabase Auth hook that mints
// the tenant claims every downstream consumer authorizes against.
//
// The hook lives in SQL, not Go, but it is tested from this package for two
// reasons. First, this is the package that owns the other half of the
// contract: signup provisioning writes the public.tenant_users row the hook
// reads. Second, .github/workflows/ci.yml already applies the full
// supabase/migrations/ chain to an ephemeral Postgres and then runs
// ./internal/signup/... against it with HIVE_TEST_DB_URL set, so a live-DB
// assertion placed here actually executes in CI instead of skipping.
//
// The static test guards the migration text; the two live tests prove the
// deployed behaviour. That split follows the precedent set by
// apps/control-plane/internal/rag/migration_schema_test.go (PR #431), which
// exists because a migration that was correct in the repo had never been
// applied to the live project and every string-match test stayed green
// throughout.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestTokenHookNeverRaisesForMembershiplessUser is the static guard against
// reintroducing the 500. Every user with no ACTIVE public.tenant_users row
// used to get HTTP 500 {"code":"P0001","message":"no_active_membership"} on
// every password grant, which made first sign-in unrecoverable for exactly
// the users who still needed provisioning. The newest migration defining the
// hook must not contain that RAISE.
func TestTokenHookNeverRaisesForMembershiplessUser(t *testing.T) {
	path, body := latestTokenHookMigration(t)

	// Assert against executable SQL only. The migration's own header explains
	// the bug it fixes and necessarily quotes the old error string, so matching
	// the raw file would flag its own documentation.
	code := stripSQLLineComments(body)

	if strings.Contains(code, "RAISE") {
		t.Fatalf("%s: the current custom_access_token_hook body must not raise; "+
			"a membership-less user must receive a token with no tenant claims "+
			"instead of a 500", filepath.Base(path))
	}

	// The membership-less branch must return early with the claims it was
	// handed, untouched.
	if !strings.Contains(code, "IF selected IS NULL THEN") ||
		!strings.Contains(code, "RETURN jsonb_build_object('claims', claims);") {
		t.Fatalf("%s: expected an early return of the unmodified claims when no "+
			"active membership resolves", filepath.Base(path))
	}

	// The member path must still emit all four claims. owui_role in
	// particular is what Open WebUI's OAUTH_ROLES_CLAIM reads
	// (deploy/docker/docker-compose.yml), and dropping it would strand every
	// member on Open WebUI's activation-pending screen. It must also never
	// carry the value OAUTH_ADMIN_ROLES names, because instance admin is
	// accounts.is_platform_admin and never a tenant role (issue #748).
	for _, want := range []string{
		"jsonb_build_object('tenant_id', selected)",
		"jsonb_build_object('tenants',   COALESCE(tenant_list, '[]'::jsonb))",
		"jsonb_build_object('role',      user_role)",
		"jsonb_build_object('owui_role', CASE WHEN user_role IN ('OWNER', 'ADMIN') THEN 'MEMBER' ELSE user_role END)",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("%s: member claim emission must still contain %q",
				filepath.Base(path), want)
		}
	}

	// This file can only match text. The behavioural guard on the same
	// property, which executes the hook for every tenant role against a real
	// database and asserts the emitted value is never Open WebUI's admin role,
	// is TestCustomAccessTokenHook_OwuiRoleNeverGrantsInstanceAdmin in
	// apps/control-plane/internal/tenants. A text check cannot tell an 'ADMIN'
	// on the input side of the CASE from one on the output side, so it is not
	// attempted here.

	// Absent, not null. An explicit null tenant_id claim invites a consumer
	// to read the key, find JSON null, and treat it as a wildcard.
	if strings.Contains(code, "'tenant_id', NULL") ||
		strings.Contains(code, "'tenant_id', null") {
		t.Fatalf("%s: must omit the tenant_id claim entirely for a "+
			"membership-less user, never emit it as null", filepath.Base(path))
	}
}

// stripSQLLineComments removes `--` line comments so assertions above run
// against executable SQL rather than the migration's explanatory header.
func stripSQLLineComments(body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// TestTokenHookOmitsTenantClaimsForMembershiplessUser is the live-DB half:
// it proves the hook actually applied to a real database issues a token for a
// user with no membership, and that the token carries none of the four tenant
// claims. Absent is the encoding every consumer fails closed on
// (apps/edge-api/internal/auth/middleware.go rejects a uuid.Nil TenantID with
// 401, and the RLS policies test role IN ('OWNER','ADMIN') which an untouched
// GoTrue 'authenticated' role fails).
func TestTokenHookOmitsTenantClaimsForMembershiplessUser(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t, ctx)
	defer pool.Close()

	userID := mustInsertAuthUser(t, ctx, pool, "membershipless-"+uuid.NewString()+"@example.com")

	claims := callTokenHook(t, ctx, pool, userID)

	// The token is issued at all. That is the whole point: the previous body
	// raised here and the user could never make the request that would
	// provision them.
	require.NotNil(t, claims, "hook must return claims for a membership-less user")

	for _, absent := range []string{"tenant_id", "tenants", "owui_role"} {
		_, present := claims[absent]
		require.Falsef(t, present,
			"claim %q must be absent for a membership-less user, got %v", absent, claims[absent])
	}

	// 'role' is GoTrue's own claim, which the hook overwrites only for
	// members. For a membership-less user it must be left exactly as supplied,
	// both because 'authenticated' is the correct PostgREST role and because
	// the RLS policies deny on it.
	require.Equal(t, "authenticated", claims["role"],
		"role must keep GoTrue's own value when there is no membership")
}

// TestTokenHookClaimsUnchangedForMember pins the member path. This is the
// regression that would hurt most: the fix for membership-less users must not
// perturb the claims a real member receives, because those claims drive RLS
// across tenant_users, tenant_settings and the audit tables, the Go-side JWT
// parse in apps/edge-api/internal/auth/jwt_supabase.go, and Open WebUI's role
// and group resolution.
func TestTokenHookClaimsUnchangedForMember(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t, ctx)
	defer pool.Close()

	tenantID := mustInsertTenant(t, ctx, pool, "hookmember-"+uuid.NewString()[:8], "HIVE_CLOUD")
	userID := mustInsertAuthUser(t, ctx, pool, "hookmember-"+uuid.NewString()+"@example.com")

	// OWNER specifically, because that is the role the owui_role remap
	// rewrites. A MEMBER would pass a weaker version of this test.
	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status)
		 VALUES ($1, $2, 'OWNER', 'ACTIVE')`, tenantID, userID)
	require.NoError(t, err)

	claims := callTokenHook(t, ctx, pool, userID)

	require.Equal(t, tenantID.String(), claims["tenant_id"],
		"member must be bound to their tenant")
	require.Equal(t, "OWNER", claims["role"],
		"role claim is read verbatim by RLS policies and must stay OWNER")
	require.Equal(t, "MEMBER", claims["owui_role"],
		"owui_role must remap OWNER to a non-admin value in Open WebUI's "+
			"OAUTH_ALLOWED_ROLES: the OWNER to ADMIN remap made a customer an "+
			"administrator of the shared chat instance (issue #748)")

	tenants, ok := claims["tenants"].([]any)
	require.True(t, ok, "tenants claim must be a JSON array, got %T", claims["tenants"])
	require.Len(t, tenants, 1)
	entry, ok := tenants[0].(map[string]any)
	require.True(t, ok, "tenants entry must be an object, got %T", tenants[0])
	require.Equal(t, tenantID.String(), entry["id"])
	require.Equal(t, "OWNER", entry["role"])
}

// TestTokenHookOmitsTenantClaimsWhenMembershipRevoked covers the second way a
// user reaches the membership-less state: they had a membership and it was
// suspended, or the tenant was archived. Both must degrade to an inert token,
// not a 500, so a revoked user can still sign in and see a designed state.
func TestTokenHookOmitsTenantClaimsWhenMembershipRevoked(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t, ctx)
	defer pool.Close()

	tenantID := mustInsertTenant(t, ctx, pool, "hookrevoked-"+uuid.NewString()[:8], "HIVE_CLOUD")
	userID := mustInsertAuthUser(t, ctx, pool, "hookrevoked-"+uuid.NewString()+"@example.com")
	_, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status)
		 VALUES ($1, $2, 'MEMBER', 'SUSPENDED')`, tenantID, userID)
	require.NoError(t, err)

	claims := callTokenHook(t, ctx, pool, userID)

	_, present := claims["tenant_id"]
	require.False(t, present, "a SUSPENDED membership must not yield a tenant claim")
	require.Equal(t, "authenticated", claims["role"])
}

// callTokenHook invokes the hook exactly the way Supabase Auth does: one jsonb
// argument carrying user_id plus the base claims GoTrue has already built. The
// base claims include GoTrue's own 'role', which is what the member path
// overwrites and the membership-less path must leave alone.
func callTokenHook(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID) map[string]any {
	t.Helper()

	event := map[string]any{
		"user_id": userID.String(),
		"claims": map[string]any{
			"sub":  userID.String(),
			"role": "authenticated",
			"aud":  "authenticated",
		},
	}
	payload, err := json.Marshal(event)
	require.NoError(t, err)

	var raw []byte
	err = pool.QueryRow(ctx,
		`SELECT public.custom_access_token_hook($1::jsonb)`, payload).Scan(&raw)
	require.NoError(t, err, "hook must not raise for any user")

	var result struct {
		Claims map[string]any `json:"claims"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	return result.Claims
}

// latestTokenHookMigration returns the path and body of the most recent
// migration that redefines public.custom_access_token_hook. Migrations are
// lexicographically ordered by their date-prefixed filenames, which is also
// the order CI and `supabase db push` apply them in, so the last match is the
// live definition.
func latestTokenHookMigration(t *testing.T) (string, string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(repoRootForSignup(t), "supabase/migrations/*.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "expected at least one Supabase migration")

	var latestPath, latestBody string
	for _, path := range matches {
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		if strings.Contains(string(body), "FUNCTION public.custom_access_token_hook") {
			latestPath, latestBody = path, string(body)
		}
	}
	require.NotEmpty(t, latestPath,
		"expected a migration defining public.custom_access_token_hook")
	return latestPath, latestBody
}

func repoRootForSignup(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	require.NoError(t, err)

	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		if parent := filepath.Dir(dir); parent == dir {
			t.Fatalf("could not find repository root from %s", wd)
		}
	}
}
