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

// The unit tests for IsTenantOwner stub TenantRoleStore, so they say nothing
// about whether the production DB role can read public.tenant_users at all.
// That table was created for the PostgREST path only: it granted TO
// authenticated and its policies evaluate auth.jwt(), which is NULL outside a
// PostgREST request. hive_app was never granted anything on it, so before
// 20260726_01_tenant_users_hive_app_grant.sql this query permission-failed or
// returned zero rows in production while passing on a superuser dev connection,
// which would have left the egress-policy admin surface 403 for every real
// tenant owner.
//
// These tests connect as hive_app specifically, mirroring
// apps/control-plane/internal/egress/repository_test.go, so that class of gap
// fails here instead of in production.

// newRLSTestPool connects as hive_app, which is NOT BYPASSRLS in production, so
// the tenant_users policy is exercised rather than bypassed by whatever
// superuser role HIVE_TEST_DB_URL otherwise connects as. MaxConns is pinned to 1
// so every Acquire returns the same physical connection the SET ROLE was issued
// on, and the pool is closed at test end so the role change never leaks.
func newRLSTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	if dsn == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: HIVE_TEST_DB_URL must point at a test database (DSN missing 'test' marker)")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse HIVE_TEST_DB_URL: %v", err)
	}
	cfg.MaxConns = 1

	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := pool.Exec(ctx, "SET ROLE hive_app"); err != nil {
		pool.Close()
		t.Skipf("SET ROLE hive_app failed (is hive_app provisioned + migrations applied on this test DB?): %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedMembership inserts a tenant and one tenant_users row over an unscoped
// connection. hive_app has SELECT only on tenant_users, and no policy at all on
// tenants, so fixture setup is deliberately not done through the role under test.
func seedMembership(t *testing.T, tenantID, userID uuid.UUID, role, status string) {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()

	if _, err := setup.Exec(ctx,
		`INSERT INTO public.tenants (id, slug, name, deployment)
		 VALUES ($1, $2, 'tenant role test', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		tenantID, "role-test-"+tenantID.String()); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	// tenant_users.user_id is an FK onto auth.users, so the membership row needs
	// a real user behind it.
	if _, err := setup.Exec(ctx,
		`INSERT INTO auth.users (id, email)
		 VALUES ($1, $2)
		 ON CONFLICT (id) DO NOTHING`,
		userID, "role-test-"+userID.String()+"@hive-test.invalid"); err != nil {
		t.Fatalf("seed auth user: %v", err)
	}
	if _, err := setup.Exec(ctx,
		`INSERT INTO public.tenant_users (tenant_id, user_id, role, status)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (tenant_id, user_id) DO UPDATE SET role = EXCLUDED.role, status = EXCLUDED.status`,
		tenantID, userID, role, status); err != nil {
		t.Fatalf("seed membership: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM auth.users WHERE id = $1`, userID)
	})
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, tenantID)
	})
}

func TestIsTenantOwner_RLS_ReadsOwnerMembershipAsHiveApp(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantID, userID := uuid.New(), uuid.New()
	seedMembership(t, tenantID, userID, "OWNER", "ACTIVE")

	svc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))
	isOwner, err := svc.IsTenantOwner(context.Background(), userID, tenantID)
	if err != nil {
		t.Fatalf("IsTenantOwner: %v (hive_app cannot read public.tenant_users, is the grant migration applied?)", err)
	}
	if !isOwner {
		t.Fatal("expected the seeded ACTIVE OWNER to be recognized as tenant owner under hive_app")
	}
}

func TestIsTenantOwner_RLS_NonOwnerAndInactiveAreNotOwners(t *testing.T) {
	pool := newRLSTestPool(t)
	svc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		role   string
		status string
	}{
		{"member is not owner", "MEMBER", "ACTIVE"},
		{"admin is not owner", "ADMIN", "ACTIVE"},
		{"suspended owner is not owner", "OWNER", "SUSPENDED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, userID := uuid.New(), uuid.New()
			seedMembership(t, tenantID, userID, tc.role, tc.status)

			isOwner, err := svc.IsTenantOwner(ctx, userID, tenantID)
			if err != nil {
				t.Fatalf("IsTenantOwner: %v", err)
			}
			if isOwner {
				t.Fatalf("role=%s status=%s must not count as tenant owner", tc.role, tc.status)
			}
		})
	}
}

// The tenant_users policy for hive_app is keyed on app.current_tenant_id, which
// GetTenantRole sets per call. A membership in another tenant must therefore
// stay invisible even though the row exists.
func TestIsTenantOwner_RLS_ForeignTenantMembershipIsInvisible(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantA, tenantB := uuid.New(), uuid.New()
	userID := uuid.New()
	seedMembership(t, tenantA, userID, "OWNER", "ACTIVE")
	seedMembership(t, tenantB, uuid.New(), "OWNER", "ACTIVE")

	svc := platform.NewTenantRoleService(platform.NewPgxTenantRoleStore(pool))
	isOwner, err := svc.IsTenantOwner(context.Background(), userID, tenantB)
	if err != nil {
		t.Fatalf("IsTenantOwner: %v", err)
	}
	if isOwner {
		t.Fatal("a user owning tenant A must not be reported as owner of tenant B")
	}
}
