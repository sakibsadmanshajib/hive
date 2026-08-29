package agentsched_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agentsched"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
)

// launchFloor is unexported in agentsched, so the live suite restates the
// figure it is pinning: 100,000,000 credits, 0.10 USD at the D-046 rate of
// 1 USD = 1,000,000,000 credits.
const liveLaunchFloor int64 = 100_000_000

// adminPool connects as the migration owner rather than hive_app. The
// solvency query reads public.tenants and public.tenant_billing_accounts,
// which are control-plane-only tables with no authenticated policy, so the
// production pool is the owner pool too (main.go hands NewPgxSolvency the
// same pool every other control-plane repository uses).
func adminPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		// Same loudness contract as newRLSTestPool: in CI a missing DSN is a
		// wiring defect, not a laptop without Postgres. ./internal/agentsched/...
		// is in ci.yml's live-Postgres package list, so this suite must run
		// there rather than skip green.
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatal("HIVE_TEST_DB_URL not set in CI: this suite guards the solvency gate's real SQL and must not silently skip")
		}
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedTenantWithDeployment inserts one tenant at the given posture and returns
// its id.
func seedTenantWithDeployment(t *testing.T, pool *pgxpool.Pool, deployment string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenants (id, slug, name, deployment)
		 VALUES ($1, $2, 'agentsched solvency test', $3)`,
		id, "agentsched-solvency-"+id.String(), deployment); err != nil {
		t.Fatalf("seed %s tenant: %v", deployment, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, id)
	})
	return id
}

// seedBillingAccount creates an account, maps it to tenantID, and grants it
// grantCredits. A zero grant posts no ledger row at all, so the account reads
// as an honest empty balance rather than one propped up by a zero entry.
func seedBillingAccount(t *testing.T, pool *pgxpool.Pool, ownerUserID, tenantID uuid.UUID, grantCredits int64) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, 'agentsched solvency', 'personal', $2) RETURNING id`,
		"agentsched-solvency-"+uuid.NewString(), ownerUserID).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.accounts WHERE id = $1`, accountID)
	})

	if _, err := pool.Exec(ctx,
		`INSERT INTO public.tenant_billing_accounts (tenant_id, account_id) VALUES ($1, $2)`,
		tenantID, accountID); err != nil {
		t.Fatalf("seed tenant_billing_accounts: %v", err)
	}

	if grantCredits > 0 {
		if _, err := ledger.NewPgxRepository(pool).PostEntry(ctx, accountID, ledger.PostEntryInput{
			EntryType:      ledger.EntryTypeGrant,
			CreditsDelta:   grantCredits,
			IdempotencyKey: "agentsched-solvency-grant-" + uuid.NewString(),
			RequestID:      "agentsched-solvency-" + uuid.NewString(),
		}); err != nil {
			t.Fatalf("seed grant: %v", err)
		}
	}
	return accountID
}

// TestPgxSolvency_AgainstRealDB pins the gate's actual SQL and its posture
// rule. The unit suite drives every caller through a fake Solvency, so this is
// the only place the real query, the LEFT JOIN, and the Enterprise
// short-circuit are executed at all; a fake cannot catch a dropped join or a
// swapped scan column.
func TestPgxSolvency_AgainstRealDB(t *testing.T) {
	pool := adminPool(t)
	ctx := context.Background()
	userID := seedUser(t)
	gate := agentsched.NewPgxSolvency(pool, ledger.NewService(ledger.NewPgxRepository(pool)))

	t.Run("enterprise tenant passes with no billing account at all", func(t *testing.T) {
		// The regression this case exists to prevent: Hive Enterprise is a
		// shipped mode with no prepaid relationship, so an Enterprise tenant
		// holds no credits and never will. Refusing it for an empty balance
		// would take routines off the air across the whole self-hosted
		// product. The billing account is ABSENT here on purpose, because
		// that absence is the normal Enterprise state, not a misconfiguration.
		tenantID := seedTenantWithDeployment(t, pool, "ENTERPRISE_EDGE")
		if err := gate.Check(ctx, tenantID, liveLaunchFloor); err != nil {
			t.Fatalf("Enterprise tenant refused: %v", err)
		}
	})

	t.Run("enterprise posture wins even when a billing account exists and is empty", func(t *testing.T) {
		// Some Enterprise tenants carry a tenant_billing_accounts row for
		// identity reasons rather than billing ones. The posture must still
		// decide, which is why the check sits before the account is read.
		tenantID := seedTenantWithDeployment(t, pool, "ENTERPRISE_EDGE")
		seedBillingAccount(t, pool, userID, tenantID, 0)
		if err := gate.Check(ctx, tenantID, liveLaunchFloor); err != nil {
			t.Fatalf("Enterprise tenant with an empty account refused: %v", err)
		}
	})

	t.Run("cloud tenant with no billing account is refused", func(t *testing.T) {
		tenantID := seedTenantWithDeployment(t, pool, "HIVE_CLOUD")
		err := gate.Check(ctx, tenantID, liveLaunchFloor)
		if err == nil {
			t.Fatal("an unmapped Hive Cloud tenant was admitted; nothing could be charged for what its sandbox spends")
		}
		if !errors.Is(err, agentsched.ErrInsufficientCredits) {
			t.Fatalf("error = %v, want ErrInsufficientCredits", err)
		}
	})

	t.Run("cloud tenant below the floor is refused", func(t *testing.T) {
		tenantID := seedTenantWithDeployment(t, pool, "HIVE_CLOUD")
		seedBillingAccount(t, pool, userID, tenantID, liveLaunchFloor-1)
		err := gate.Check(ctx, tenantID, liveLaunchFloor)
		if !errors.Is(err, agentsched.ErrInsufficientCredits) {
			t.Fatalf("error = %v, want ErrInsufficientCredits one credit below the floor", err)
		}
	})

	t.Run("cloud tenant exactly at the floor passes", func(t *testing.T) {
		// The boundary is inclusive: the floor is what the tenant must be able
		// to cover, not what it must exceed.
		tenantID := seedTenantWithDeployment(t, pool, "HIVE_CLOUD")
		seedBillingAccount(t, pool, userID, tenantID, liveLaunchFloor)
		if err := gate.Check(ctx, tenantID, liveLaunchFloor); err != nil {
			t.Fatalf("funded tenant refused at exactly the floor: %v", err)
		}
	})

	t.Run("tenant that does not exist is refused", func(t *testing.T) {
		err := gate.Check(ctx, uuid.New(), liveLaunchFloor)
		if !errors.Is(err, agentsched.ErrInsufficientCredits) {
			t.Fatalf("error = %v, want ErrInsufficientCredits for an absent tenant", err)
		}
	})
}
