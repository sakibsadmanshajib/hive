package artifacts

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newRLSTestPool connects as the hive_app role -- NOT BYPASSRLS in
// production -- so the artifacts / artifact_versions tenant-isolation and
// public-read RLS policies are actually exercised. Mirrors
// apps/edge-api/internal/rag/repository_rls_test.go.
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

// seedArtifactTenant inserts an FK row into public.tenants via a
// short-lived, unscoped connection (hive_app has no INSERT policy on
// tenants).
func seedArtifactTenant(t *testing.T, id uuid.UUID) {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()
	_, err = setup.Exec(ctx,
		`INSERT INTO public.tenants (id, slug, name, deployment)
		 VALUES ($1, $2, 'artifacts test tenant', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		id, "edge-artifacts-test-"+id.String())
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, id)
	})
}

func TestRepo_RLS_CreateAddVersionThenGetRoundTrips(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantID := uuid.New()
	seedArtifactTenant(t, tenantID)

	repo := NewRepo(pool)
	ctx := context.Background()

	artifactID, err := repo.CreateArtifact(ctx, tenantID, "demo")
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	version, err := repo.AddVersion(ctx, tenantID, artifactID, "tenant/artifact/blob.html", 42)
	if err != nil {
		t.Fatalf("AddVersion: %v", err)
	}
	if version != 1 {
		t.Fatalf("version = %d, want 1", version)
	}

	got, err := repo.GetVersion(ctx, tenantID, artifactID, nil)
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if got.StoragePath != "tenant/artifact/blob.html" || got.Version != 1 {
		t.Fatalf("unexpected round-trip: %+v", got)
	}
}

func TestRepo_RLS_AddVersionMintsSequentialVersionsAtSameID(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantID := uuid.New()
	seedArtifactTenant(t, tenantID)

	repo := NewRepo(pool)
	ctx := context.Background()
	artifactID, err := repo.CreateArtifact(ctx, tenantID, "demo")
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	if _, err := repo.AddVersion(ctx, tenantID, artifactID, "v1.html", 1); err != nil {
		t.Fatalf("AddVersion v1: %v", err)
	}
	v2, err := repo.AddVersion(ctx, tenantID, artifactID, "v2.html", 2)
	if err != nil {
		t.Fatalf("AddVersion v2: %v", err)
	}
	if v2 != 2 {
		t.Fatalf("second version = %d, want 2", v2)
	}

	// v1 must still be reachable at its own version, latest must be v2.
	v1Row, err := repo.GetVersion(ctx, tenantID, artifactID, intPtr(1))
	if err != nil {
		t.Fatalf("GetVersion v1: %v", err)
	}
	if v1Row.StoragePath != "v1.html" {
		t.Fatalf("v1 storage path = %q, want v1.html", v1Row.StoragePath)
	}
	latest, err := repo.GetVersion(ctx, tenantID, artifactID, nil)
	if err != nil {
		t.Fatalf("GetVersion latest: %v", err)
	}
	if latest.StoragePath != "v2.html" || latest.Version != 2 {
		t.Fatalf("latest = %+v, want v2.html/2", latest)
	}
}

// TestRepo_RLS_CrossTenantCannotReadPrivateArtifact proves the database
// policy itself blocks cross-tenant reads of a private artifact.
func TestRepo_RLS_CrossTenantCannotReadPrivateArtifact(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantA, tenantB := uuid.New(), uuid.New()
	seedArtifactTenant(t, tenantA)
	seedArtifactTenant(t, tenantB)

	repo := NewRepo(pool)
	ctx := context.Background()
	artifactID, err := repo.CreateArtifact(ctx, tenantA, "tenant-a-only")
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	if _, err := repo.AddVersion(ctx, tenantA, artifactID, "secret.html", 1); err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	if _, err := repo.GetVersion(ctx, tenantB, artifactID, nil); err != ErrNotFound {
		t.Fatalf("GetVersion under tenant B context: err = %v, want ErrNotFound", err)
	}
}

// TestRepo_RLS_PublicArtifactReadableWithNoTenantContext proves the
// anonymous path (viewerTenantID = uuid.Nil, no app.current_tenant_id set
// at all) can still read a version once its artifact is marked public,
// while a private artifact stays unreadable in that same anonymous path.
func TestRepo_RLS_PublicArtifactReadableWithNoTenantContext(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantID := uuid.New()
	seedArtifactTenant(t, tenantID)

	repo := NewRepo(pool)
	ctx := context.Background()

	privateID, err := repo.CreateArtifact(ctx, tenantID, "private")
	if err != nil {
		t.Fatalf("CreateArtifact private: %v", err)
	}
	if _, err := repo.AddVersion(ctx, tenantID, privateID, "private.html", 1); err != nil {
		t.Fatalf("AddVersion private: %v", err)
	}

	publicID, err := repo.CreateArtifact(ctx, tenantID, "public")
	if err != nil {
		t.Fatalf("CreateArtifact public: %v", err)
	}
	if _, err := repo.AddVersion(ctx, tenantID, publicID, "public.html", 1); err != nil {
		t.Fatalf("AddVersion public: %v", err)
	}
	if err := repo.SetPublic(ctx, tenantID, publicID, true); err != nil {
		t.Fatalf("SetPublic: %v", err)
	}

	if _, err := repo.GetVersion(ctx, uuid.Nil, privateID, nil); err != ErrNotFound {
		t.Fatalf("anonymous read of private artifact: err = %v, want ErrNotFound", err)
	}

	got, err := repo.GetVersion(ctx, uuid.Nil, publicID, nil)
	if err != nil {
		t.Fatalf("anonymous read of public artifact: %v", err)
	}
	if got.StoragePath != "public.html" {
		t.Fatalf("storage path = %q, want public.html", got.StoragePath)
	}
}

// TestRepo_RLS_NoSessionLeakAcrossBorrows proves the tenant context set by
// one repository call does not survive onto the pooled connection for
// whoever borrows it next.
func TestRepo_RLS_NoSessionLeakAcrossBorrows(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantA := uuid.New()
	seedArtifactTenant(t, tenantA)

	repo := NewRepo(pool)
	ctx := context.Background()
	artifactID, err := repo.CreateArtifact(ctx, tenantA, "tenant-a-only")
	if err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	var setting string
	if err := pool.QueryRow(ctx, "SELECT current_setting('app.current_tenant_id', true)").Scan(&setting); err != nil {
		t.Fatalf("read current_setting: %v", err)
	}
	if setting != "" {
		t.Fatalf("session leak: app.current_tenant_id still %q after CreateArtifact committed, want empty", setting)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.artifacts WHERE id = $1`,
		artifactID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("session leak: bare borrow saw %d row(s) with no tenant context and is_public=false (RLS should fail-closed on NULL)", count)
	}
}

func intPtr(v int) *int { return &v }
