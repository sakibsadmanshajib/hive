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
		 VALUES (gen_random_uuid(), $1, 'chat balance live', 'HIVE_CLOUD')
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

	t.Run("selected tenant wins over newer membership", func(t *testing.T) {
		// Second billed tenant the user joined more recently.
		var newerTenant, newerAccount uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO public.tenants(id, slug, name, deployment)
			 VALUES (gen_random_uuid(), $1, 'chat balance newer', 'HIVE_CLOUD') RETURNING id`,
			"chat-balance-newer-"+uuid.NewString()).Scan(&newerTenant); err != nil {
			t.Fatalf("seed second tenant: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM public.tenants WHERE id = $1`, newerTenant) })
		if err := pool.QueryRow(ctx,
			`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
			 VALUES (gen_random_uuid(), $1, 'chat balance newer', 'personal', $2) RETURNING id`,
			"chat-balance-newer-"+uuid.NewString(), userID).Scan(&newerAccount); err != nil {
			t.Fatalf("seed second account: %v", err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM public.accounts WHERE id = $1`, newerAccount) })
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.tenant_billing_accounts(tenant_id, account_id) VALUES ($1, $2)`,
			newerTenant, newerAccount); err != nil {
			t.Fatalf("seed second tba: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.tenant_users(tenant_id, user_id, role, status)
			 VALUES ($1, $2, 'MEMBER', 'ACTIVE')`, newerTenant, userID); err != nil {
			t.Fatalf("seed second membership: %v", err)
		}

		// No selection: newest membership answers.
		got, err := repo.ResolveAccountIDForEmail(ctx, email)
		if err != nil {
			t.Fatalf("resolve without selection: %v", err)
		}
		if got != newerAccount {
			t.Fatalf("no-selection resolve %s, want newest %s", got, newerAccount)
		}

		// Selection names the older tenant: it wins despite being older.
		var olderAccount uuid.UUID
		if err := pool.QueryRow(ctx,
			`SELECT account_id FROM public.tenant_billing_accounts WHERE tenant_id = $1`, tenantID).Scan(&olderAccount); err != nil {
			t.Fatalf("read older account: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE auth.users SET raw_user_meta_data = jsonb_set(
				COALESCE(raw_user_meta_data, '{}'::jsonb),
				'{selected_tenant_id}', to_jsonb($1::text)) WHERE id = $2`, tenantID.String(), userID); err != nil {
			t.Fatalf("set selection: %v", err)
		}
		got, err = repo.ResolveAccountIDForEmail(ctx, email)
		if err != nil {
			t.Fatalf("resolve with selection: %v", err)
		}
		if got != olderAccount {
			t.Fatalf("selection resolve %s, want selected %s", got, olderAccount)
		}

		// A malformed selection degrades to the fallback order instead of erroring.
		if _, err := pool.Exec(ctx,
			`UPDATE auth.users SET raw_user_meta_data = jsonb_set(
				COALESCE(raw_user_meta_data, '{}'::jsonb),
				'{selected_tenant_id}', '"not-a-uuid"') WHERE id = $1`, userID); err != nil {
			t.Fatalf("break selection: %v", err)
		}
		got, err = repo.ResolveAccountIDForEmail(ctx, email)
		if err != nil {
			t.Fatalf("resolve with malformed selection: %v", err)
		}
		if got != newerAccount {
			t.Fatalf("malformed-selection resolve %s, want fallback %s", got, newerAccount)
		}
	})
}
