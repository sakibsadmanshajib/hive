package auditarchive_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditarchive"
)

// newRLSTestPool connects and SET ROLE hive_app -- the same NOT-BYPASSRLS role
// the control-plane runs as in production (20260518_04_phase19_audit_rls_and_indexes.sql;
// the platform pool does no SET ROLE, it connects as hive_app directly). This
// makes the audit_cold_archive_manifest RLS policies actually apply instead of
// being bypassed by whatever superuser HIVE_TEST_DB_URL otherwise connects as.
// MaxConns is pinned to 1 so every Acquire returns the same physical connection
// (the role change sticks), and the pool is closed at test end so the role
// never leaks to another test. Mirrors internal/rag/repository_rls_test.go.
//
// These tests are the regression guard for the 20260625_06 schema reconcile:
// that migration briefly replaced the hive_app service policy with a
// tenant-scoped SELECT-only policy, which silently broke the cross-tenant
// archiver (InsertManifest denied, FetchExpiredManifests returned zero rows,
// DeleteManifest denied). The compliance suite did not catch it because it
// connects as the raw superuser and bypasses RLS.
func newRLSTestPool(t *testing.T, role string) *pgxpool.Pool {
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
	if _, err := pool.Exec(ctx, "SET ROLE "+role); err != nil {
		pool.Close()
		t.Skipf("SET ROLE %s failed (is %s provisioned + migrations applied on this test DB?): %v", role, role, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedTenant inserts an FK row into public.tenants via a short-lived, unscoped
// (superuser) connection, and registers cleanup that TRUNCATEs the manifest
// then deletes the tenant. The manifest is TRUNCATEd rather than DELETEd
// because the immutability trigger (20260625_08) blocks a row-level DELETE
// while purge_after is in the future, and the manifest -> tenants FK is
// ON DELETE RESTRICT, so tenant cleanup would otherwise fail. TRUNCATE does not
// fire the BEFORE DELETE row trigger.
func seedTenant(t *testing.T, id uuid.UUID) {
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
		 VALUES ($1, $2, 'auditarchive rls test tenant', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		id, "auditarchive-rls-"+id.String())
	if err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), `TRUNCATE public.audit_cold_archive_manifest`)
		_, _ = cleanup.Exec(context.Background(), `DELETE FROM public.tenants WHERE id = $1`, id)
	})
}

func manifestEntry(tenantID uuid.UUID, month time.Time, purgeAfter time.Time) auditarchive.ManifestEntry {
	return auditarchive.ManifestEntry{
		ID:             uuid.New(),
		TenantID:       tenantID,
		PartitionMonth: month,
		ObjectKey:      "audit/cold/" + tenantID.String() + "/" + month.Format("2006-01") + ".jsonl.gz",
		SHA256Hash:     make([]byte, 32),
		RowCount:       1,
		FirstSeq:       1,
		LastSeq:        1,
		ArchivedAt:     time.Now().UTC(),
		PurgeAfter:     purgeAfter,
	}
}

// TestRLS_HiveAppServiceRoleIsCrossTenant is the core regression guard. Running
// as hive_app with NO app.current_tenant_id set (exactly how the archiver runs
// on the shared pool), the service role must be able to INSERT manifest rows
// for multiple tenants, read any tenant's row back, and have the purge scan see
// every tenant's expired rows. Under the broken tenant-scoped model this test
// fails: the INSERT is denied and the scan matches zero rows.
func TestRLS_HiveAppServiceRoleIsCrossTenant(t *testing.T) {
	pool := newRLSTestPool(t, "hive_app")
	ctx := context.Background()

	tenantA, tenantB := uuid.New(), uuid.New()
	seedTenant(t, tenantA)
	seedTenant(t, tenantB)

	repo := auditarchive.NewPgRepository(pool)
	month := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	past := time.Now().Add(-time.Hour).UTC()

	// Cross-tenant INSERT with no tenant context set.
	for _, tid := range []uuid.UUID{tenantA, tenantB} {
		inserted, err := repo.InsertManifest(ctx, manifestEntry(tid, month, past))
		if err != nil {
			t.Fatalf("InsertManifest as hive_app (no tenant ctx) for %s: %v", tid, err)
		}
		if !inserted {
			t.Fatalf("InsertManifest for %s reported not inserted", tid)
		}
	}

	// Cross-tenant read: hive_app sees another tenant's row with no tenant ctx.
	exists, err := repo.ManifestExists(ctx, tenantA, month)
	if err != nil {
		t.Fatalf("ManifestExists: %v", err)
	}
	if !exists {
		t.Fatalf("hive_app could not read tenant A manifest row with no app.current_tenant_id set (tenant-scoped policy regression)")
	}

	// Cross-tenant purge scan returns both tenants' expired rows.
	expired, err := repo.FetchExpiredManifests(ctx, time.Now())
	if err != nil {
		t.Fatalf("FetchExpiredManifests: %v", err)
	}
	seen := map[uuid.UUID]bool{}
	for _, m := range expired {
		seen[m.TenantID] = true
	}
	if !seen[tenantA] || !seen[tenantB] {
		t.Fatalf("purge scan did not return both tenants' expired rows (got A=%v B=%v, %d rows total) -- archiver would never purge", seen[tenantA], seen[tenantB], len(expired))
	}
}

// TestRLS_ImmutabilityTriggerGovernsDelete confirms the write-once semantics the
// service model relies on instead of RLS: UPDATE is always blocked, DELETE is
// blocked while purge_after is in the future, and DELETE is allowed once
// purge_after has passed (the retention-expiry purge path).
func TestRLS_ImmutabilityTriggerGovernsDelete(t *testing.T) {
	pool := newRLSTestPool(t, "hive_app")
	ctx := context.Background()

	tenantID := uuid.New()
	seedTenant(t, tenantID)
	repo := auditarchive.NewPgRepository(pool)

	// A future-purge row: DELETE must be blocked by the trigger.
	future := manifestEntry(tenantID, time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), time.Now().Add(24*time.Hour).UTC())
	if _, err := repo.InsertManifest(ctx, future); err != nil {
		t.Fatalf("insert future-purge row: %v", err)
	}
	if err := repo.DeleteManifest(ctx, future.ID); err == nil {
		t.Fatalf("DeleteManifest of a future-purge row succeeded; immutability trigger should have blocked it")
	}

	// UPDATE is always blocked, even for the service role.
	if _, err := pool.Exec(ctx,
		`UPDATE public.audit_cold_archive_manifest SET row_count = row_count + 1 WHERE id = $1`,
		future.ID); err == nil {
		t.Fatalf("UPDATE of a manifest row succeeded; immutability trigger should have blocked it")
	}

	// A past-purge row: DELETE is the legitimate retention purge and must succeed.
	pastEntry := manifestEntry(tenantID, time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), time.Now().Add(-time.Hour).UTC())
	if _, err := repo.InsertManifest(ctx, pastEntry); err != nil {
		t.Fatalf("insert past-purge row: %v", err)
	}
	if err := repo.DeleteManifest(ctx, pastEntry.ID); err != nil {
		t.Fatalf("DeleteManifest of an expired (past purge_after) row failed; purge path is broken: %v", err)
	}
	if exists, err := repo.ManifestExists(ctx, tenantID, pastEntry.PartitionMonth); err != nil {
		t.Fatalf("ManifestExists after purge: %v", err)
	} else if exists {
		t.Fatalf("expired row still present after DeleteManifest")
	}
}

// TestRLS_AuditorReadOnlyAndPublicDenied confirms the two other roles: auditor_ro
// may read but not write, and PUBLIC has no grant at all (the M7 concern in
// 20260518_04 -- schema USAGE must not expose manifest filenames or digests).
func TestRLS_AuditorReadOnlyAndPublicDenied(t *testing.T) {
	tenantID := uuid.New()
	seedTenant(t, tenantID)

	// Seed one row as the service role so the auditor has something to read.
	hivePool := newRLSTestPool(t, "hive_app")
	ctx := context.Background()
	repo := auditarchive.NewPgRepository(hivePool)
	month := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	if _, err := repo.InsertManifest(ctx, manifestEntry(tenantID, month, time.Now().Add(-time.Hour).UTC())); err != nil {
		t.Fatalf("seed manifest row as hive_app: %v", err)
	}

	auditPool := newRLSTestPool(t, "auditor_ro")

	// auditor_ro can read.
	var count int
	if err := auditPool.QueryRow(ctx,
		`SELECT count(*) FROM public.audit_cold_archive_manifest WHERE tenant_id = $1`,
		tenantID).Scan(&count); err != nil {
		t.Fatalf("auditor_ro SELECT: %v", err)
	}
	if count != 1 {
		t.Fatalf("auditor_ro read %d rows, want 1", count)
	}

	// auditor_ro cannot write (no INSERT grant).
	if _, err := auditPool.Exec(ctx,
		`INSERT INTO public.audit_cold_archive_manifest
		   (tenant_id, partition_month, object_key, sha256_hash, row_count, first_seq, last_seq, purge_after)
		 VALUES ($1, $2, 'x', '\x00', 0, 0, 0, now())`,
		tenantID, month); err == nil {
		t.Fatalf("auditor_ro INSERT succeeded; auditor must be read-only")
	}

	// PUBLIC has no grant on the table.
	var pubGrants int
	if err := auditPool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.role_table_grants
		  WHERE table_schema = 'public'
		    AND table_name = 'audit_cold_archive_manifest'
		    AND grantee = 'PUBLIC'`).Scan(&pubGrants); err != nil {
		t.Fatalf("query PUBLIC grants: %v", err)
	}
	if pubGrants != 0 {
		t.Fatalf("PUBLIC holds %d grant(s) on audit_cold_archive_manifest, want 0", pubGrants)
	}
}
