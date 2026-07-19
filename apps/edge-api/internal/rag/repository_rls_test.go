package rag

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newRLSTestPool connects as the hive_app role -- NOT BYPASSRLS in production
// (see 20260518_04_phase19_audit_rls_and_indexes.sql) -- so the rag_documents
// / rag_chunks tenant-isolation RLS policies are actually exercised rather
// than bypassed by whatever superuser role HIVE_TEST_DB_URL otherwise
// connects as. MaxConns is pinned to 1 so every Acquire inside a test returns
// the same physical connection, and the pool is closed (not returned to a
// shared pool) at test end so the role change never leaks to another test.
// Mirrors apps/control-plane/internal/rag/repository_rls_test.go.
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

// seedRAGTenant inserts an FK row into public.tenants via a short-lived,
// unscoped connection (hive_app has no INSERT policy on tenants).
func seedRAGTenant(t *testing.T, id uuid.UUID) {
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
		 VALUES ($1, $2, 'rag test tenant', 'HIVE_CLOUD')
		 ON CONFLICT (id) DO NOTHING`,
		id, "edge-rag-test-"+id.String())
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

// seedRAGChunk inserts a document + one chunk directly via an unscoped
// connection, bypassing RLS so cross-tenant fixtures can be planted without
// going through the (tenant-scoped) Repo under test.
func seedRAGChunk(t *testing.T, tenantID uuid.UUID, content string, vec []float32) uuid.UUID {
	t.Helper()
	dsn := os.Getenv("HIVE_TEST_DB_URL")
	ctx := context.Background()
	setup, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	defer setup.Close()

	var docID uuid.UUID
	err = setup.QueryRow(ctx, `
		INSERT INTO public.rag_documents (tenant_id, name, mime_type, size_bytes, status)
		VALUES ($1, 'seed.txt', 'text/plain', $2, 'embedded')
		RETURNING id`,
		tenantID, len(content),
	).Scan(&docID)
	if err != nil {
		t.Fatalf("seed document: %v", err)
	}

	encoded, err := encodeVector(vec)
	if err != nil {
		t.Fatalf("encode seed vector: %v", err)
	}

	var chunkID uuid.UUID
	err = setup.QueryRow(ctx, `
		INSERT INTO public.rag_chunks (tenant_id, document_id, chunk_index, content, token_count, embedding)
		VALUES ($1, $2, 0, $3, 1, $4::vector)
		RETURNING id`,
		tenantID, docID, content, encoded,
	).Scan(&chunkID)
	if err != nil {
		t.Fatalf("seed chunk: %v", err)
	}
	return chunkID
}

func fixedVector(v float32) []float32 {
	out := make([]float32, EmbeddingDimension)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestRepo_RLS_InsertThenGetRoundTripsUnderTenantContext(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantID := uuid.New()
	seedRAGTenant(t, tenantID)

	repo := NewRepo(pool, "vector")
	ctx := context.Background()

	docID, err := repo.InsertDocument(ctx, tenantID, "doc.txt", "text/plain", 3)
	if err != nil {
		t.Fatalf("InsertDocument: %v", err)
	}

	got, err := repo.GetDocument(ctx, tenantID, docID)
	if err != nil {
		t.Fatalf("GetDocument: %v", err)
	}
	if got.Name != "doc.txt" {
		t.Fatalf("expected round-tripped name %q, got %q", "doc.txt", got.Name)
	}
}

// TestRepo_RLS_CrossTenantContextCannotReadRows proves the database policy
// itself blocks cross-tenant reads, independent of the repository's own
// tenant_id filter.
func TestRepo_RLS_CrossTenantContextCannotReadRows(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantA, tenantB := uuid.New(), uuid.New()
	seedRAGTenant(t, tenantA)
	seedRAGTenant(t, tenantB)

	repo := NewRepo(pool, "vector")
	ctx := context.Background()
	docID, err := repo.InsertDocument(ctx, tenantA, "tenant-a-only.txt", "text/plain", 1)
	if err != nil {
		t.Fatalf("seed tenant A document: %v", err)
	}

	if _, err := pool.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, false)", tenantB.String()); err != nil {
		t.Fatalf("set_config tenant B: %v", err)
	}
	var count int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM public.rag_documents WHERE id = $1`,
		docID).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("RLS did not block cross-tenant read: got %d row(s) for tenant A's document while session claimed tenant B", count)
	}
}

// TestRepo_RLS_NoSessionLeakAcrossBorrows proves the tenant context set by one
// repository call does not survive onto the pooled connection for whoever
// borrows it next.
func TestRepo_RLS_NoSessionLeakAcrossBorrows(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantA := uuid.New()
	seedRAGTenant(t, tenantA)

	repo := NewRepo(pool, "vector")
	ctx := context.Background()
	docID, err := repo.InsertDocument(ctx, tenantA, "tenant-a-only.txt", "text/plain", 1)
	if err != nil {
		t.Fatalf("seed tenant A document: %v", err)
	}

	var setting string
	if err := pool.QueryRow(ctx, "SELECT current_setting('app.current_tenant_id', true)").Scan(&setting); err != nil {
		t.Fatalf("read current_setting: %v", err)
	}
	if setting != "" {
		t.Fatalf("session leak: app.current_tenant_id still %q after InsertDocument committed, want empty", setting)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.rag_documents WHERE id = $1`,
		docID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("session leak: bare borrow saw %d row(s) for tenant A's document with no tenant context set (RLS should fail-closed on NULL)", count)
	}
}

// TestRepo_RLS_SearchChunksCannotReadOtherTenantChunks proves that
// SearchChunks scoped to tenant A never returns tenant B's chunks, even when
// both chunks carry an identical embedding vector (so ANN proximity alone
// would otherwise rank them the same) -- the boundary must be RLS, not
// merely low vector similarity.
func TestRepo_RLS_SearchChunksCannotReadOtherTenantChunks(t *testing.T) {
	pool := newRLSTestPool(t)
	tenantA, tenantB := uuid.New(), uuid.New()
	seedRAGTenant(t, tenantA)
	seedRAGTenant(t, tenantB)

	vec := fixedVector(0.1)
	chunkA := seedRAGChunk(t, tenantA, "tenant a content", vec)
	chunkB := seedRAGChunk(t, tenantB, "tenant b content", vec)

	repo := NewRepo(pool, "vector")
	ctx := context.Background()

	results, err := repo.SearchChunks(ctx, tenantA, vec, 10)
	if err != nil {
		t.Fatalf("SearchChunks: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 result scoped to tenant A, got %d", len(results))
	}
	if results[0].ID != chunkA {
		t.Fatalf("expected tenant A's chunk %v, got %v", chunkA, results[0].ID)
	}
	for _, r := range results {
		if r.ID == chunkB {
			t.Fatalf("RLS did not block cross-tenant chunk read: tenant B's chunk %v leaked into tenant A's search results", chunkB)
		}
	}
}
