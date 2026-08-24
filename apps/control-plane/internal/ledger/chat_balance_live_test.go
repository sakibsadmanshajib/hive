package ledger

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Issue #1063, the SQL half. ResolveAccountIDForEmail joins four tables; a
// wrong join or status filter either shows one user's balance to another or
// shows nobody anything. The unit stubs encode the same assumption twice, so
// only the real schema can catch a drift. Gated on HIVE_TEST_DB_URL like the
// rest of this suite; the CI leg that runs it bootstraps the full migration
// chain first.

func TestResolveAccountIDForEmail_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	ctx := context.Background()

	email := "chat-balance-" + uuid.NewString() + "@test.local"

	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users (id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`, email).Scan(&userID); err != nil {
		t.Fatalf("seed auth.users failed (is this a migrated test DB?): %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, userID) })

	var tenantID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.tenants(id, slug, name, deployment)
		 VALUES (gen_random_uuid(), $1, 'chat balance live', 'cloud')
		 RETURNING id`,
		"chat-balance-"+uuid.NewString()).Scan(&tenantID); err != nil {
		t.Fatalf("seed tenants: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM public.tenants WHERE id = $1`, tenantID) })

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_users(tenant_id, user_id, role, status)
		 VALUES ($1, $2, 'MEMBER', 'ACTIVE')`, tenantID, userID); err != nil {
		t.Fatalf("seed tenant_users: %v", err)
	}

	repo := &pgxRepository{pool: pool}

	t.Run("unmapped email resolves to Nil without error", func(t *testing.T) {
		id, err := repo.ResolveAccountIDForEmail(ctx, "nobody-"+uuid.NewString()+"@test.local")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if id != uuid.Nil {
			t.Fatalf("expected uuid.Nil, got %s", id)
		}
	})

	t.Run("active membership on billed tenant resolves case-insensitively", func(t *testing.T) {
		var accountID uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
			 VALUES (gen_random_uuid(), $1, 'chat balance live', 'personal', $2) RETURNING id`,
			"chat-balance-"+uuid.NewString(), userID).Scan(&accountID); err != nil {
			t.Fatalf("seed account: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM public.accounts WHERE id = $1`, accountID) })

		if _, err := pool.Exec(ctx,
			`INSERT INTO public.tenant_billing_accounts(tenant_id, account_id) VALUES ($1, $2)`,
			tenantID, accountID); err != nil {
			t.Fatalf("seed tba: %v", err)
		}

		got, err := repo.ResolveAccountIDForEmail(ctx, strings.ToUpper(email))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != accountID {
			t.Fatalf("resolved %s, want %s", got, accountID)
		}
	})
}
