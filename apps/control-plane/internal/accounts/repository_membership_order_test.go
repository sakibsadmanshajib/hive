package accounts_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
)

// newMembershipOrderTestPool mirrors profiles.newBillingJoinTestPool: skip
// unless HIVE_TEST_DB_URL is set, and refuse a DSN without a "test" marker
// since this file removes rows it inserts.
func newMembershipOrderTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedMembershipOrderUser inserts one auth user, plus two accounts each with
// a membership for that user, at the given created_at timestamps (set
// explicitly, bypassing the column default, so the test controls ordering
// instead of racing the clock). Registers cleanup for all four rows.
func seedMembershipOrderUser(t *testing.T, pool *pgxpool.Pool, prefix string, createdAt [2]time.Time) (userID uuid.UUID, accountIDs [2]uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	email := prefix + "@example.test"

	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		email).Scan(&userID); err != nil {
		t.Fatalf("insert auth user: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, userID) })

	for i := range accountIDs {
		slug := prefix + "-" + []string{"a", "b"}[i] + "-" + uuid.NewString()[:8]
		if err := pool.QueryRow(ctx,
			`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id)
			 VALUES (gen_random_uuid(), $1, $1, 'personal', $2) RETURNING id`,
			slug, userID).Scan(&accountIDs[i]); err != nil {
			t.Fatalf("insert account %d: %v", i, err)
		}
		t.Cleanup(func(id uuid.UUID) func() {
			return func() { _, _ = pool.Exec(ctx, `DELETE FROM public.accounts WHERE id = $1`, id) }
		}(accountIDs[i]))

		if _, err := pool.Exec(ctx,
			`INSERT INTO public.account_memberships(id, account_id, user_id, role, status, created_at)
			 VALUES (gen_random_uuid(), $1, $2, 'member', 'active', $3)`,
			accountIDs[i], userID, createdAt[i]); err != nil {
			t.Fatalf("insert membership %d: %v", i, err)
		}
	}
	return userID, accountIDs
}

// TestListMembershipsByUserID_OrdersByCreatedAt is the regression guard for
// EnsureViewerContext's nondeterministic default-workspace pick (repository.go
// had no ORDER BY, so Postgres's returned row order for a user with more than
// one membership was unspecified and could vary across calls). Inserts the
// chronologically later membership FIRST, to prove the result reflects
// created_at rather than insertion order.
func TestListMembershipsByUserID_OrdersByCreatedAt(t *testing.T) {
	pool := newMembershipOrderTestPool(t)
	repo := accounts.NewPgxRepository(pool)
	ctx := context.Background()

	older := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	// Insert order is [newer, older]; expected read order is [older, newer].
	userID, accountIDs := seedMembershipOrderUser(t, pool, "orderby-"+uuid.NewString()[:8], [2]time.Time{newer, older})
	newerAccountID, olderAccountID := accountIDs[0], accountIDs[1]

	memberships, err := repo.ListMembershipsByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("ListMembershipsByUserID: %v", err)
	}
	if len(memberships) != 2 {
		t.Fatalf("got %d memberships, want 2", len(memberships))
	}
	if memberships[0].AccountID != olderAccountID {
		t.Fatalf("memberships[0].AccountID = %s, want the older membership %s (insertion order was [newer, older])",
			memberships[0].AccountID, olderAccountID)
	}
	if memberships[1].AccountID != newerAccountID {
		t.Fatalf("memberships[1].AccountID = %s, want the newer membership %s", memberships[1].AccountID, newerAccountID)
	}
}

// TestListMembershipsByUserID_TiebreaksByID covers the pathological case a
// single bulk upsert produces: two memberships sharing one created_at (the
// same INSERT statement stamps every row with one transaction timestamp).
// id is the deterministic tiebreaker so row order still cannot vary by call.
func TestListMembershipsByUserID_TiebreaksByID(t *testing.T) {
	pool := newMembershipOrderTestPool(t)
	repo := accounts.NewPgxRepository(pool)
	ctx := context.Background()

	tied := time.Now()
	userID, accountIDs := seedMembershipOrderUser(t, pool, "tiebreak-"+uuid.NewString()[:8], [2]time.Time{tied, tied})

	memberships, err := repo.ListMembershipsByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("ListMembershipsByUserID: %v", err)
	}
	if len(memberships) != 2 {
		t.Fatalf("got %d memberships, want 2", len(memberships))
	}
	if memberships[0].ID.String() >= memberships[1].ID.String() {
		t.Fatalf("memberships not ordered by id ascending on a created_at tie: [0]=%s [1]=%s",
			memberships[0].ID, memberships[1].ID)
	}
	gotAccounts := map[uuid.UUID]bool{memberships[0].AccountID: true, memberships[1].AccountID: true}
	for _, id := range accountIDs {
		if !gotAccounts[id] {
			t.Fatalf("expected account %s in result, got %v", id, memberships)
		}
	}
}
