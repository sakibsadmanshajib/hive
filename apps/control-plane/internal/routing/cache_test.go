package routing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/rcache"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing"
)

type countingRepo struct {
	routing.Repository
	polCalls   int
	candCalls  int
	priceCalls int

	policy  catalog.AliasPolicySnapshot
	cands   []routing.RouteCandidate
	pricing catalog.CatalogPricing
	unit    string
	polErr  error
}

func (r *countingRepo) LoadAliasPolicy(ctx context.Context, aliasID string) (catalog.AliasPolicySnapshot, error) {
	r.polCalls++
	return r.policy, r.polErr
}

func (r *countingRepo) ListRouteCandidates(ctx context.Context, aliasID string) ([]routing.RouteCandidate, error) {
	r.candCalls++
	return r.cands, nil
}

func (r *countingRepo) LoadAliasPricing(ctx context.Context, aliasID string) (catalog.CatalogPricing, string, error) {
	r.priceCalls++
	return r.pricing, r.unit, nil
}

func newRoutingCache(t *testing.T) *rcache.Cache {
	t.Helper()
	mr := miniredis.RunT(t)
	cache, err := rcache.New("redis://"+mr.Addr(), "test:v1", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestCachedRepository_PolicyAndCandidatesHitCache(t *testing.T) {
	ctx := context.Background()
	repo := &countingRepo{
		policy: catalog.AliasPolicySnapshot{AliasID: "hive-fast", PolicyMode: "priority"},
		cands:  []routing.RouteCandidate{{RouteID: "r1", AliasID: "hive-fast", Provider: "groq"}},
	}
	cached := routing.NewCachedRepository(repo, newRoutingCache(t))

	for i := 0; i < 3; i++ {
		if _, err := cached.LoadAliasPolicy(ctx, "hive-fast"); err != nil {
			t.Fatal(err)
		}
		if _, err := cached.ListRouteCandidates(ctx, "hive-fast"); err != nil {
			t.Fatal(err)
		}
	}
	if repo.polCalls != 1 || repo.candCalls != 1 {
		t.Fatalf("expected single DB read each, got pol=%d cand=%d", repo.polCalls, repo.candCalls)
	}
}

func TestCachedRepository_PricingRoundTrip(t *testing.T) {
	ctx := context.Background()
	in := int64(5)
	repo := &countingRepo{
		pricing: catalog.CatalogPricing{InputPriceCredits: &in, PricingMode: catalog.PricingModeFixed},
		unit:    "tokens",
	}
	cached := routing.NewCachedRepository(repo, newRoutingCache(t))

	firstP, firstU, err := cached.LoadAliasPricing(ctx, "hive-fast")
	if err != nil {
		t.Fatal(err)
	}
	secondP, secondU, err := cached.LoadAliasPricing(ctx, "hive-fast")
	if err != nil {
		t.Fatal(err)
	}
	if repo.priceCalls != 1 {
		t.Fatalf("expected one DB pricing read, got %d", repo.priceCalls)
	}
	if firstU != secondU || secondU != "tokens" {
		t.Fatalf("price unit must round-trip: %q vs %q", firstU, secondU)
	}
	if firstP.InputPriceCredits == nil || *secondP.InputPriceCredits != 5 {
		t.Fatalf("pricing pointer fields must round-trip")
	}
}

func TestCachedRepository_NotFoundErrorNotCached(t *testing.T) {
	ctx := context.Background()
	notFound := errors.New("routing: alias not found: nope")
	repo := &countingRepo{polErr: notFound}
	cached := routing.NewCachedRepository(repo, newRoutingCache(t))

	for i := 0; i < 2; i++ {
		if _, err := cached.LoadAliasPolicy(ctx, "nope"); !errors.Is(err, notFound) {
			t.Fatalf("want not-found error, got %v", err)
		}
	}
	if repo.polCalls != 2 {
		t.Fatalf("error reads must re-query the database: calls=%d", repo.polCalls)
	}
}

func TestCachedRepository_EmptyCandidatesCachedAsEmpty(t *testing.T) {
	ctx := context.Background()
	repo := &countingRepo{}
	cached := routing.NewCachedRepository(repo, newRoutingCache(t))

	got1, err := cached.ListRouteCandidates(ctx, "ghost-alias")
	if err != nil {
		t.Fatal(err)
	}
	got2, err := cached.ListRouteCandidates(ctx, "ghost-alias")
	if err != nil {
		t.Fatal(err)
	}
	// The raw pgx repository returns a nil slice for zero rows; the cache must
	// preserve that exactly (nil stays nil through both the miss and the hit).
	if got1 != nil || got2 != nil {
		t.Fatalf("nil candidate list changed shape through the cache: %T/%T", got1, got2)
	}
	if repo.candCalls != 1 {
		t.Fatalf("empty result must be cached too: calls=%d", repo.candCalls)
	}
}
