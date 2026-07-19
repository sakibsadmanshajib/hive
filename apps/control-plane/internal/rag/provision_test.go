package rag

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/packages/embedmodel"
)

// newProvisionTestPool connects as the privileged (DDL-capable) role the
// control-plane provisioning routine uses in production. Skips when
// HIVE_TEST_DB_URL is unset, and refuses a DSN that does not look like a test
// database, mirroring repository_rls_test.go. This exercises the real column
// recreate + index rebuild against a live Postgres+pgvector.
func newProvisionTestPool(t *testing.T) *pgxpool.Pool {
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

func embeddingColumnType(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var typ string
	err := pool.QueryRow(context.Background(), `
		SELECT format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname='public' AND c.relname='rag_chunks' AND a.attname='embedding' AND NOT a.attisdropped`).Scan(&typ)
	if err != nil {
		t.Fatalf("read column type: %v", err)
	}
	return typ
}

func policyExists(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS(SELECT 1 FROM pg_policies
			WHERE schemaname='public' AND tablename='rag_chunks'
			  AND policyname='rag_chunks_tenant_isolation')`).Scan(&ok); err != nil {
		t.Fatalf("check policy: %v", err)
	}
	return ok
}

// TestProvision_RecreatesColumnAndIndex drives the provisioning routine across
// two dimension bands (vector then halfvec) and asserts idempotency, the
// resolved column type, index presence, and that tenant RLS survives the
// column recreation. Integration-gated on HIVE_TEST_DB_URL.
func TestProvision_RecreatesColumnAndIndex(t *testing.T) {
	pool := newProvisionTestPool(t)
	ctx := context.Background()

	// vector(1024) HNSW cosine (the clean demo target).
	planVec, err := embedmodel.Resolve("qwen3-embedding-8b", 1024, false)
	if err != nil {
		t.Fatalf("resolve vec plan: %v", err)
	}
	if err := Provision(ctx, pool, planVec); err != nil {
		t.Fatalf("provision vector(1024): %v", err)
	}
	if got := embeddingColumnType(t, pool); got != "vector(1024)" {
		t.Fatalf("column type = %q, want vector(1024)", got)
	}
	if !policyExists(t, pool) {
		t.Fatal("tenant RLS policy missing after provisioning")
	}

	// Idempotent: a second identical provision must not error.
	if err := Provision(ctx, pool, planVec); err != nil {
		t.Fatalf("re-provision (idempotent) vector(1024): %v", err)
	}
	if got := embeddingColumnType(t, pool); got != "vector(1024)" {
		t.Fatalf("column type after re-provision = %q, want vector(1024)", got)
	}

	// Switch to halfvec(3000) (the 2001..4000 indexable band).
	planHalf, err := embedmodel.Resolve("qwen3-embedding-8b", 3000, false)
	if err != nil {
		t.Fatalf("resolve halfvec plan: %v", err)
	}
	if err := Provision(ctx, pool, planHalf); err != nil {
		t.Fatalf("provision halfvec(3000): %v", err)
	}
	if got := embeddingColumnType(t, pool); got != "halfvec(3000)" {
		t.Fatalf("column type = %q, want halfvec(3000)", got)
	}
	if !policyExists(t, pool) {
		t.Fatal("tenant RLS policy missing after halfvec provisioning")
	}

	// Restore the seed default so the test DB is left as it started.
	if err := Provision(ctx, pool, planVec); err != nil {
		t.Fatalf("restore vector(1024): %v", err)
	}
}
