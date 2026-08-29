package accounts_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// Issue #896: public.account_memberships RLS was a blanket
// `FOR ALL TO hive_app USING (true) WITH CHECK (true)`, so hive_app -- the DB
// role this repository's migrations have been building toward, and which is
// not BYPASSRLS -- saw every tenant's membership rows regardless of what any
// Go WHERE clause said. A Go query missing an account_id/user_id predicate
// would have returned every tenant's rows.
//
// What this file does and does not prove (issue #1444). Every test here opts
// into the role under test with an explicit SET ROLE hive_app below, which is
// exactly what the deployed system does NOT do: hive_app is NOLOGIN with zero
// role members, no production code path sets that role, and control-plane
// connects as postgres, which is BYPASSRLS. So these tests prove the policy is
// correct and that the Go call sites set the session variables it needs. They
// do not prove that anything is enforced in production today, and issue #896
// stays open until the connecting role changes. Proven live against a throwaway Postgres before writing the
// fix: with the old policy, `SET ROLE hive_app; SELECT * FROM
// account_memberships` with NO session scope set returned both of two
// unrelated fixture accounts' rows.
// supabase/migrations/20260829_04_account_memberships_hive_app_scope.sql is
// the fix: two PERMISSIVE policies scoped by app.current_actor_user_id
// ("my own memberships") and app.current_account_id ("members of one
// account I already administer"), matching accounts.pgxRepository's two
// access shapes (see that migration's header and withActorTx/withAccountTx's
// doc comments in repository.go for the full shape enumeration).
//
// MaxConns is pinned to 1, mirroring marketplace/repository_test.go's
// newRLSTestPool, so SET ROLE hive_app applies to every connection this
// pool ever hands out (the pool has exactly one physical connection).

func newAccountMembershipsRLSPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.PoolWithConfig(t, "HIVE_TEST_DB_URL", func(cfg *pgxpool.Config) {
		cfg.MaxConns = 1
	})
	if _, err := pool.Exec(context.Background(), "SET ROLE hive_app"); err != nil {
		pool.Close()
		t.Skipf("SET ROLE hive_app failed (is hive_app provisioned + migrations applied on this test DB?): %v", err)
	}
	return pool
}

// seedAccountMembership inserts a user, an account, and one
// account_memberships row over an unscoped connection. hive_app's new
// policies deny an unscoped write by design, so fixture setup does not go
// through the role under test, mirroring every other RLS suite in this
// repository (role_rls_test.go's seedMembership, marketplace's seedTenant).
func seedAccountMembership(t *testing.T, accountID, userID uuid.UUID, role, status string) {
	t.Helper()
	dsn := dbtest.RequireURL(t, "HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()

	if _, err := setup.Exec(ctx,
		`INSERT INTO auth.users (id, email) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`,
		userID, "membership-scope-"+userID.String()+"@hive-test.invalid"); err != nil {
		t.Fatalf("seed auth user: %v", err)
	}
	if _, err := setup.Exec(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES ($1, $2, 'membership scope test', 'business', $3)
		 ON CONFLICT (id) DO NOTHING`,
		accountID, "membership-scope-"+accountID.String(), userID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := setup.Exec(ctx,
		`INSERT INTO public.account_memberships (id, account_id, user_id, role, status)
		 VALUES ($1, $2, $3, $4, $5)
		 ON CONFLICT (account_id, user_id) DO UPDATE SET role = EXCLUDED.role, status = EXCLUDED.status`,
		uuid.New(), accountID, userID, role, status); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, userID)
	})
}

// TestAccountMembershipsRLS_HiveAppWithoutSessionScopeSeesNothing is the
// core #896 regression guard: a query that forgets to set either session
// variable at all (the "one missed predicate anywhere" failure mode the
// issue describes) must see zero rows under the new policy, not every
// tenant's rows the way the old USING(true) policy gave.
func TestAccountMembershipsRLS_HiveAppWithoutSessionScopeSeesNothing(t *testing.T) {
	pool := newAccountMembershipsRLSPool(t)
	accountID, userID := uuid.New(), uuid.New()
	seedAccountMembership(t, accountID, userID, "owner", "active")

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM public.account_memberships WHERE account_id = $1`, accountID).
		Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows visible to hive_app with no session scope set, got %d", count)
	}
}

// TestAccountMembershipsRLS_AccountScopeIsolatesAcrossAccounts is issue
// #896's literal cross-tenant read: two unrelated accounts, hive_app scoped
// to one via app.current_account_id, must never see the other's row.
// Proven live before the fix: the same query with the old blanket policy
// returned both accounts' rows regardless of which one the session variable
// named.
func TestAccountMembershipsRLS_AccountScopeIsolatesAcrossAccounts(t *testing.T) {
	pool := newAccountMembershipsRLSPool(t)
	ctx := context.Background()
	accountA, accountB := uuid.New(), uuid.New()
	userA, userB := uuid.New(), uuid.New()
	seedAccountMembership(t, accountA, userA, "owner", "active")
	seedAccountMembership(t, accountB, userB, "owner", "active")

	if _, err := pool.Exec(ctx,
		"SELECT set_config('app.current_account_id', $1, false)", accountA.String()); err != nil {
		t.Fatalf("set account scope: %v", err)
	}

	rows, err := pool.Query(ctx,
		`SELECT user_id FROM public.account_memberships WHERE account_id = ANY($1)`,
		[]uuid.UUID{accountA, accountB})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var seen []uuid.UUID
	for rows.Next() {
		var u uuid.UUID
		if err := rows.Scan(&u); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen = append(seen, u)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(seen) != 1 || seen[0] != userA {
		t.Fatalf("expected only account A's member visible with app.current_account_id set to A, got %v", seen)
	}
}

// TestAccountMembershipsRLS_ActorScopeIsolatesAcrossOtherUsers is the
// mirror check for the "my own memberships" shape: scoped to one user's own
// id, a different user's row in a DIFFERENT account must not be visible.
func TestAccountMembershipsRLS_ActorScopeIsolatesAcrossOtherUsers(t *testing.T) {
	pool := newAccountMembershipsRLSPool(t)
	ctx := context.Background()
	accountA, accountB := uuid.New(), uuid.New()
	userA, userB := uuid.New(), uuid.New()
	seedAccountMembership(t, accountA, userA, "owner", "active")
	seedAccountMembership(t, accountB, userB, "owner", "active")

	if _, err := pool.Exec(ctx,
		"SELECT set_config('app.current_actor_user_id', $1, false)", userA.String()); err != nil {
		t.Fatalf("set actor scope: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.account_memberships WHERE user_id = $1`, userB).
		Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected user B's row to be invisible while scoped to user A's own actor id, got %d rows", count)
	}
}

// TestPgxRepository_ListMembersByAccountID_DoesNotLeakAcrossAccounts exercises
// the real production code path (accounts.pgxRepository, the same withAccountTx
// wiring apps/control-plane actually runs), not just the raw policy, so a
// regression in the Go wiring itself (forgetting to route a call through
// withActorTx/withAccountTx) fails here too.
func TestPgxRepository_ListMembersByAccountID_DoesNotLeakAcrossAccounts(t *testing.T) {
	pool := newAccountMembershipsRLSPool(t)
	ctx := context.Background()
	accountA, accountB := uuid.New(), uuid.New()
	userA, userB := uuid.New(), uuid.New()
	seedAccountMembership(t, accountA, userA, "owner", "active")
	seedAccountMembership(t, accountB, userB, "owner", "active")

	repo := accounts.NewPgxRepository(pool)
	members, err := repo.ListMembersByAccountID(ctx, accountA)
	if err != nil {
		t.Fatalf("ListMembersByAccountID: %v", err)
	}
	if len(members) != 1 || members[0].UserID != userA {
		t.Fatalf("expected exactly account A's one member, got %+v", members)
	}
}

// TestPgxRepository_ListMembershipsByUserID_DoesNotLeakOtherUsers is the
// Shape A counterpart, through the real repository method
// signup/AcceptInvitation and the account-switch route both call.
func TestPgxRepository_ListMembershipsByUserID_DoesNotLeakOtherUsers(t *testing.T) {
	pool := newAccountMembershipsRLSPool(t)
	ctx := context.Background()
	accountA, accountB := uuid.New(), uuid.New()
	userA, userB := uuid.New(), uuid.New()
	seedAccountMembership(t, accountA, userA, "owner", "active")
	seedAccountMembership(t, accountB, userB, "owner", "active")

	repo := accounts.NewPgxRepository(pool)
	memberships, err := repo.ListMembershipsByUserID(ctx, userA)
	if err != nil {
		t.Fatalf("ListMembershipsByUserID: %v", err)
	}
	if len(memberships) != 1 || memberships[0].AccountID != accountA {
		t.Fatalf("expected exactly user A's one membership (account A), got %+v", memberships)
	}
}
