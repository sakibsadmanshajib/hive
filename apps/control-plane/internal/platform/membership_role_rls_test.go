package platform_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// IsWorkspaceOwner is the predicate behind PermBillingWrite on
// PUT /api/v1/budgets/{ws} and POST /api/v1/spend-alerts/{ws}. PR #768 mounted
// both of those routes, so this predicate now sits on a money path.
//
// The unit tests for it stub RoleStore, which proves the service maps the
// owner role to true and says nothing about the SQL underneath. The SQL is
// where issue #803 lives: GetMembershipRole selected role without constraining
// account_memberships.status, so a row with role=owner and status=invited
// passed the owner check on a billing write.
//
// These tests connect on an unscoped pool rather than SET ROLE hive_app, unlike
// the tenant_users suite in role_rls_test.go. hive_app holds no grant on
// public.accounts or public.account_memberships, so this account-scoped path
// does not run as that role. The defect under test is the predicate, not RLS.

const (
	skipNoTestDSN  = "HIVE_TEST_DB_URL not set"
	errNotTestDSN  = "refusing to run: HIVE_TEST_DB_URL must name a database whose name carries the test marker"
	errParseDSN    = "parse HIVE_TEST_DB_URL: %v"
	errConnect     = "connect: %v"
	errSeedPool    = "seed pool: %v"
	errSeedUser    = "seed auth user: %v"
	errSeedAccount = "seed account: %v"
	errSeedMember  = "seed membership: %v"
	errOwnerCall   = "IsWorkspaceOwner: %v"
)

// The marker is checked on the parsed database name, not on the whole DSN.
// Searching the raw string matches a host, a user, a password or a query
// parameter that happens to contain the substring, and this suite inserts and
// deletes rows, so a loose check is a licence to write to whatever it was
// pointed at.
func requireAccountRoleTestDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		// CI wires HIVE_TEST_DB_URL for the live-Postgres step and passes
		// -short for the step that has none, so a missing DSN there is a wiring
		// defect (the silent-green never-runs shape of issues #701/#708/#797),
		// not a laptop without Postgres. Fail loudly in CI live leg; local runs
		// without a test database still skip.
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatal(skipNoTestDSN + " in CI: this membership RLS suite must not silently skip")
		}
		t.Skip(skipNoTestDSN)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf(errParseDSN, err)
	}
	if !strings.Contains(strings.ToLower(cfg.ConnConfig.Database), "test") {
		t.Fatal(errNotTestDSN)
	}
	return dsn
}

func newAccountRoleTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(requireAccountRoleTestDSN(t))
	if err != nil {
		t.Fatalf(errParseDSN, err)
	}
	cfg.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf(errConnect, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedSubjectAccountMembership inserts the subject user, a separate user who created
// and owns the account, the account itself, and one account_memberships row
// putting the subject on that account with the given role and status.
//
// The creator is a separate user deliberately. An earlier version of this
// fixture set accounts.owner_user_id to the subject and then inserted the same
// subject as status='invited', so the two rows contradicted each other: the
// account said the user owned it outright while the membership said the
// invitation was still outstanding. A test asserting "not an owner" against a
// row that also says "owner" proves nothing about which of the two the
// predicate reads. Here every case is one shape, a user added to somebody
// else's workspace, and the only variable is the role and status on the
// membership row, which is the predicate under test.
func seedSubjectAccountMembership(t *testing.T, accountID, userID uuid.UUID, role, status string) {
	t.Helper()
	dsn := requireAccountRoleTestDSN(t)
	ctx := context.Background()

	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf(errSeedPool, err)
	}
	defer setup.Close()

	creatorID := uuid.New()
	for _, seeded := range []uuid.UUID{creatorID, userID} {
		if _, err := setup.Exec(ctx, insertUserSQL, seeded,
			"membership-role-"+seeded.String()+"@hive-test.invalid"); err != nil {
			t.Fatalf(errSeedUser, err)
		}
	}
	if _, err := setup.Exec(ctx, insertAccountSQL, accountID,
		"membership-role-"+accountID.String(), creatorID); err != nil {
		t.Fatalf(errSeedAccount, err)
	}
	if _, err := setup.Exec(ctx, insertMembershipSQL, accountID, userID, role, status); err != nil {
		t.Fatalf(errSeedMember, err)
	}

	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		// account_memberships cascades from accounts, and accounts.owner_user_id
		// references auth.users, so the account row goes first.
		_, _ = cleanup.Exec(context.Background(), dropAccountSQL, accountID)
		_, _ = cleanup.Exec(context.Background(), dropUserSQL, userID)
		_, _ = cleanup.Exec(context.Background(), dropUserSQL, creatorID)
	})
}

const (
	insertUserSQL = `INSERT INTO auth.users (id, email) VALUES ($1, $2)
		ON CONFLICT (id) DO NOTHING`

	insertAccountSQL = `INSERT INTO public.accounts
		(id, slug, display_name, account_type, owner_user_id)
		VALUES ($1, $2, 'membership role test', 'business', $3)
		ON CONFLICT (id) DO NOTHING`

	insertMembershipSQL = `INSERT INTO public.account_memberships
		(account_id, user_id, role, status)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (account_id, user_id)
		DO UPDATE SET role = EXCLUDED.role, status = EXCLUDED.status`

	dropAccountSQL = `DELETE FROM public.accounts WHERE id = $1`
	dropUserSQL    = `DELETE FROM auth.users WHERE id = $1`
)

// Positive control. Without it, a GetMembershipRole that returned the empty
// role for every input would pass every negative case below, and the suite
// would report that a completely broken predicate is safe.
func TestIsWorkspaceOwner_ActiveOwnerIsOwner(t *testing.T) {
	pool := newAccountRoleTestPool(t)
	accountID, userID := uuid.New(), uuid.New()
	seedSubjectAccountMembership(t, accountID, userID, "owner", "active")

	svc := platform.NewRoleService(platform.NewPgxRoleStore(pool))
	isOwner, err := svc.IsWorkspaceOwner(context.Background(), userID, accountID)
	if err != nil {
		t.Fatalf(errOwnerCall, err)
	}
	if !isOwner {
		t.Fatal("an active owner membership must be recognized as workspace owner")
	}
}

// Issue #803. account_memberships.status is constrained to active or invited
// today, so invited is the reachable non-active value: an owner who was invited
// to a workspace and has not accepted. That row must not authorize a billing
// write on PUT /api/v1/budgets/{ws} or POST /api/v1/spend-alerts/{ws}.
func TestIsWorkspaceOwner_NonActiveOwnerIsNotOwner(t *testing.T) {
	pool := newAccountRoleTestPool(t)
	svc := platform.NewRoleService(platform.NewPgxRoleStore(pool))
	ctx := context.Background()

	cases := []struct {
		name   string
		role   string
		status string
	}{
		{"invited owner is not owner", "owner", "invited"},
		{"active member is not owner", "member", "active"},
		{"invited member is not owner", "member", "invited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accountID, userID := uuid.New(), uuid.New()
			seedSubjectAccountMembership(t, accountID, userID, tc.role, tc.status)

			isOwner, err := svc.IsWorkspaceOwner(ctx, userID, accountID)
			if err != nil {
				t.Fatalf(errOwnerCall, err)
			}
			if isOwner {
				t.Fatalf(errNotOwnerFmt, tc.role, tc.status)
			}
		})
	}
}

const errNotOwnerFmt = "role=%s status=%s must not authorize a workspace-owner operation"

// A membership row in another workspace must not carry over. This is the
// account-scoped twin of the foreign-tenant case in role_rls_test.go.
func TestIsWorkspaceOwner_ForeignWorkspaceMembershipDoesNotCarry(t *testing.T) {
	pool := newAccountRoleTestPool(t)
	accountA, accountB := uuid.New(), uuid.New()
	userID := uuid.New()
	seedSubjectAccountMembership(t, accountA, userID, "owner", "active")
	seedSubjectAccountMembership(t, accountB, uuid.New(), "owner", "active")

	svc := platform.NewRoleService(platform.NewPgxRoleStore(pool))
	isOwner, err := svc.IsWorkspaceOwner(context.Background(), userID, accountB)
	if err != nil {
		t.Fatalf(errOwnerCall, err)
	}
	if isOwner {
		t.Fatal("owning workspace A must not report ownership of workspace B")
	}
}
