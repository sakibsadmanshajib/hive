package rcache_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/rcache"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing"
)

// TestLiveBench measures the exact read sequence a metered request performs
// through SelectRoute (alias policy + route candidates + alias pricing + the
// per-tenant entitlement check), cached vs uncached, against a real Postgres
// and Redis. Skipped unless HIVE_LIVE_BENCH=1, so CI never runs it; it exists
// so the PR's latency numbers can be reproduced against any live stack:
//
//	go test -c -o bench.test ./apps/control-plane/internal/platform/rcache/
//	HIVE_LIVE_BENCH=1 SUPABASE_DB_URL=... REDIS_URL=redis://... ./bench.test -test.run TestLiveBench
//
// Read-only: it only SELECTs catalog rows and writes cache keys.
func TestLiveBench(t *testing.T) {
	if os.Getenv("HIVE_LIVE_BENCH") != "1" {
		t.Skip("set HIVE_LIVE_BENCH=1 to run")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("SUPABASE_DB_URL"))
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	defer pool.Close()

	cache, err := rcache.New(os.Getenv("REDIS_URL"), "hivecp:v1", rcache.DefaultTTL)
	if err != nil {
		t.Fatalf("rcache: %v", err)
	}
	defer cache.Close()

	var alias string
	if err := pool.QueryRow(ctx, "select alias_id from public.model_aliases where lifecycle='stable' order by alias_id limit 1").Scan(&alias); err != nil {
		t.Fatalf("pick alias: %v", err)
	}
	var tid uuid.UUID
	if err := pool.QueryRow(ctx, "select tenant_id from public.tenant_billing_accounts limit 1").Scan(&tid); err != nil {
		t.Fatalf("pick tenant: %v", err)
	}

	base := routing.NewPgxRepository(pool)
	cbase := catalog.NewPgxRepository(pool)

	uncachedRouting := routing.NewCachedRepository(base, nil)
	cachedRouting := routing.NewCachedRepository(base, cache)
	entUncached := catalog.NewService(cbase)
	entCached := catalog.NewService(catalog.NewCachedRepository(cbase, cache))

	const n = 300
	run := func(repo routing.Repository, ent routing.TenantEntitlements) []float64 {
		ts := make([]float64, 0, n)
		for i := 0; i < n; i++ {
			start := time.Now()
			benchSeq(t, ctx, repo, ent, tid, alias)
			ts = append(ts, time.Since(start).Seconds())
		}
		sort.Float64s(ts)
		return ts
	}

	benchSeq(t, ctx, cachedRouting, entCached, tid, alias) // warm cache
	run(uncachedRouting, entUncached)                      // warm the DB pool too, so neither timed pass pays a cold-start penalty
	u := run(uncachedRouting, entUncached)
	c := run(cachedRouting, entCached)

	fmt.Printf("\nLIVE_BENCH alias=%s tenant=%s n=%d\n", alias, tid.String(), n)
	fmt.Printf("LIVE_BENCH uncached_ms: min=%.3f p50=%.3f p90=%.3f max=%.3f\n",
		u[0]*1000, pct(u, 0.5)*1000, pct(u, 0.9)*1000, u[len(u)-1]*1000)
	fmt.Printf("LIVE_BENCH   cached_ms: min=%.3f p50=%.3f p90=%.3f max=%.3f\n",
		c[0]*1000, pct(c, 0.5)*1000, pct(c, 0.9)*1000, c[len(c)-1]*1000)
}

// benchSeq runs one full SelectRoute read sequence (policy + candidates +
// pricing + entitlement verdict) through repo and entitlement source.
func benchSeq(t *testing.T, ctx context.Context, repo routing.Repository, ent routing.TenantEntitlements, tid uuid.UUID, alias string) {
	if _, err := repo.LoadAliasPolicy(ctx, alias); err != nil {
		t.Fatal(err)
	}
	cands, err := repo.ListRouteCandidates(ctx, alias)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 {
		t.Fatalf("no candidates for %s", alias)
	}
	if _, _, err := repo.LoadAliasPricing(ctx, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ent.IsAliasVisibleToTenant(ctx, tid, alias); err != nil {
		t.Fatal(err)
	}
}

func pct(sorted []float64, p float64) float64 {
	i := int(p * float64(len(sorted)-1))
	return sorted[i]
}
