package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/rcache"
)

type fakeCatalogRepo struct {
	catalog.Repository
	publicCalls int
	tenantCalls int
	visCalls    int
	upsertCalls int
	deleteCalls int

	public      []catalog.ModelAlias
	tenantLists map[uuid.UUID][]catalog.ModelAlias
	visible     bool
}

func (r *fakeCatalogRepo) ListPublicAliases(ctx context.Context) ([]catalog.ModelAlias, error) {
	r.publicCalls++
	return r.public, nil
}

func (r *fakeCatalogRepo) ListAliasesForTenant(ctx context.Context, tenantID uuid.UUID) ([]catalog.ModelAlias, error) {
	r.tenantCalls++
	return r.tenantLists[tenantID], nil
}

func (r *fakeCatalogRepo) IsAliasVisibleToTenant(ctx context.Context, tenantID uuid.UUID, aliasID string) (bool, error) {
	r.visCalls++
	return r.visible, nil
}

func (r *fakeCatalogRepo) UpsertVisibility(ctx context.Context, row catalog.TenantModelVisibility) error {
	r.upsertCalls++
	r.visible = row.Visible
	return nil
}

func (r *fakeCatalogRepo) DeleteVisibility(ctx context.Context, tenantID uuid.UUID, aliasID string) error {
	r.deleteCalls++
	return nil
}

func newCatalogCache(t *testing.T) *rcache.Cache {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return rcache.New(client, "test:v1", 30*time.Second)
}

func TestCachedCatalog_PublicListCached(t *testing.T) {
	ctx := context.Background()
	repo := &fakeCatalogRepo{public: []catalog.ModelAlias{{AliasID: "hive-fast"}}}
	cached := catalog.NewCachedRepository(repo, newCatalogCache(t))

	for i := 0; i < 3; i++ {
		got, err := cached.ListPublicAliases(ctx)
		if err != nil || len(got) != 1 || got[0].AliasID != "hive-fast" {
			t.Fatalf("read %d: got=%v err=%v", i, got, err)
		}
	}
	if repo.publicCalls != 1 {
		t.Fatalf("expected one DB read, got %d", repo.publicCalls)
	}
}

func TestCachedCatalog_TenantListsAreIsolatedByTenantKey(t *testing.T) {
	ctx := context.Background()
	t1 := uuid.New()
	t2 := uuid.New()
	repo := &fakeCatalogRepo{tenantLists: map[uuid.UUID][]catalog.ModelAlias{
		t1: {{AliasID: "alias-t1"}},
		t2: {{AliasID: "alias-t2"}, {AliasID: "extra-t2"}},
	}}
	cached := catalog.NewCachedRepository(repo, newCatalogCache(t))

	for i := 0; i < 2; i++ {
		l1, _ := cached.ListAliasesForTenant(ctx, t1)
		l2, _ := cached.ListAliasesForTenant(ctx, t2)
		if len(l1) != 1 || len(l2) != 2 {
			t.Fatalf("tenant lists leaked across cache keys: t1=%d t2=%d (pass %d)", len(l1), len(l2), i)
		}
	}
	if repo.tenantCalls != 2 {
		t.Fatalf("each tenant should load once, got %d", repo.tenantCalls)
	}
}

func TestCachedCatalog_VisibleVerdictPerTenantAndInvalidatedOnWrite(t *testing.T) {
	ctx := context.Background()
	tid := uuid.New()
	repo := &fakeCatalogRepo{visible: false}
	cached := catalog.NewCachedRepository(repo, newCatalogCache(t))

	v, err := cached.IsAliasVisibleToTenant(ctx, tid, "hive-fast")
	if err != nil || v {
		t.Fatalf("initial verdict: v=%v err=%v", v, err)
	}
	v, _ = cached.IsAliasVisibleToTenant(ctx, tid, "hive-fast")
	if v {
		t.Fatal("second read should come from cache with same verdict")
	}
	if repo.visCalls != 1 {
		t.Fatalf("verdict should be cached: calls=%d", repo.visCalls)
	}

	if err := cached.UpsertVisibility(ctx, catalog.TenantModelVisibility{TenantID: tid, AliasID: "hive-fast", Visible: true}); err != nil {
		t.Fatal(err)
	}
	v, err = cached.IsAliasVisibleToTenant(ctx, tid, "hive-fast")
	if err != nil || !v {
		t.Fatalf("post-invalidation read must see the write: v=%v err=%v", v, err)
	}

	// A different tenant must never reuse this tenant's cached verdict: its
	// read must be a fresh load against the store (a distinct cache key).
	// The fake's verdict is global, so the assertion is on load behavior,
	// not on the boolean.
	other := uuid.New()
	callsBefore := repo.visCalls
	if _, err := cached.IsAliasVisibleToTenant(ctx, other, "hive-fast"); err != nil {
		t.Fatal(err)
	}
	if repo.visCalls != callsBefore+1 {
		t.Fatal("cross-tenant read reused another tenant's cache entry")
	}
}

func TestCachedCatalog_DeleteVisibilityInvalidates(t *testing.T) {
	ctx := context.Background()
	tid := uuid.New()
	repo := &fakeCatalogRepo{visible: true}
	cached := catalog.NewCachedRepository(repo, newCatalogCache(t))

	if _, err := cached.IsAliasVisibleToTenant(ctx, tid, "a"); err != nil {
		t.Fatal(err)
	}
	if err := cached.DeleteVisibility(ctx, tid, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.IsAliasVisibleToTenant(ctx, tid, "a"); err != nil {
		t.Fatal(err)
	}
	if repo.visCalls != 2 {
		t.Fatalf("delete must invalidate the cached verdict: calls=%d", repo.visCalls)
	}
}
