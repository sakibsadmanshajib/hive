//go:build integration

package routing

import (
	"context"
	"testing"
)

// Live-Postgres half of the #1171 guards (see reasoning_reserve_test.go for
// the offline halves).
//
// These run against a database with the FULL migration chain applied,
// including 20260826_01_route_reasoning_reserve.sql, via ROUTING_TEST_DB_URL
// (same contract as catalog_pricing_integration_test.go). They exist because
// fake-backed settlement-adjacent data guards have proven nothing twice this
// month: here the real schema, the real seeded rows, and the real pgx scan +
// selection aggregate are what answer.
//
// Run:
//
//	go test -tags integration ./apps/control-plane/internal/routing/...

// TestFreePoolReasoningReserveLiveRows pins the per-member reserves as DATA,
// not as fixture: every member of the pool carries 4096.
//
// CHANGED 2026-08-30, and the reason is the whole point of the assertion. This
// used to expect 0 on route-free-pool-free, because 20260826_01 set it that
// way for a member pinned to dots-studio/dots-3-note-preview:free, a model
// that does not reason. 20260830_01 repoints that member at openrouter/free,
// OpenRouter's Free Models Router, which selects among whatever is free at the
// moment of the request and advertises `reasoning` and `include_reasoning`; a
// reasoning model can now answer there, and provider_capabilities has declared
// supports_reasoning true for the route since 20260824_02. A reasoning model on
// a zero reserve is the failure issue #1171 added this column for, so the
// member joins the other three at 4096 rather than keeping a 0 whose
// justification was deleted.
func TestFreePoolReasoningReserveLiveRows(t *testing.T) {
	pool := connectCatalogDB(t)
	ctx := context.Background()

	for routeID, want := range map[string]int{
		"route-free-pool-free":   4096,
		"route-free-pool-gemini": 4096,
		"route-free-pool-groq":   4096,
		"route-free-pool-groq-2": 4096,
	} {
		var got int
		if err := pool.QueryRow(
			ctx,
			"select reasoning_reserve_tokens from public.provider_routes where route_id = $1",
			routeID,
		).Scan(&got); err != nil {
			t.Fatalf("route %s: %v", routeID, err)
		}
		if got != want {
			t.Errorf("route %s reasoning_reserve_tokens = %d, want %d", routeID, got, want)
		}
	}
}

// TestFreePoolReasoningReserveLiveSelection runs the REAL pgx repository and
// service against the real seeded rows: selecting hive-free must surface the
// pool-max reserve (4096) to edge-api, which is the figure dispatch inflates
// by.
func TestFreePoolReasoningReserveLiveSelection(t *testing.T) {
	pool := connectCatalogDB(t)

	repo := NewPgxRepository(pool)
	svc := NewService(repo, nil)
	result := singleSelect(t, svc, "hive-free")

	if result.ReasoningReserveTokens != 4096 {
		t.Errorf("hive-free selection reserve = %d, want 4096 (pool max across the four live members)", result.ReasoningReserveTokens)
	}
}
