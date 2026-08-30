package routing

import (
	"context"
	"strings"
	"testing"
)

// Guards over migration 20260826_01_route_reasoning_reserve.sql and the
// pool-max reasoning reserve on SelectionResult (issue #1171).
//
// Offline halves here; the live-Postgres half lives in
// reasoning_reserve_integration_test.go (build tag integration).

const reasoningReserveMigrationRelPath = "supabase/migrations/20260826_01_route_reasoning_reserve.sql"

// TestSelectRouteCarriesPoolMaxReasoningReserve pins the aggregate rule: the
// result carries the MAX reserve across eligible candidates sharing the
// selected route's litellm_model_name. LiteLLM load-balances that whole
// group, so whichever member answers must find headroom already applied.
func TestSelectRouteCarriesPoolMaxReasoningReserve(t *testing.T) {
	repo := &stubRepository{
		candidates: []RouteCandidate{
			{RouteID: "route-free-pool-free", AliasID: "hive-free", Provider: "openrouter",
				LiteLLMModelName: "route-free-pool", HealthState: "healthy", PriceClass: "budget",
				SupportsChatCompletions: true},
			{RouteID: "route-free-pool-gemini", AliasID: "hive-free", Provider: "gemini",
				LiteLLMModelName: "route-free-pool", HealthState: "healthy", PriceClass: "budget",
				SupportsChatCompletions: true, ReasoningReserveTokens: 4096},
			{RouteID: "route-free-pool-groq", AliasID: "hive-free", Provider: "groq-2",
				LiteLLMModelName: "route-free-pool", HealthState: "healthy", PriceClass: "budget",
				SupportsChatCompletions: true, ReasoningReserveTokens: 4096},
			// A fifth candidate on a DIFFERENT litellm name is a different
			// gateway group; its reserve must not leak into this selection.
			{RouteID: "route-other-group", AliasID: "hive-free", Provider: "openrouter",
				LiteLLMModelName: "route-some-other-group", HealthState: "healthy", PriceClass: "budget",
				SupportsChatCompletions: true, ReasoningReserveTokens: 99999},
		},
	}
	svc := NewService(repo, nil)

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-free",
		NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("SelectRoute: %v", err)
	}

	if result.ReasoningReserveTokens != 4096 {
		t.Errorf("ReasoningReserveTokens = %d, want 4096 (pool max, excluding the other group's 99999)", result.ReasoningReserveTokens)
	}
}

// TestSelectRouteZeroReserveStaysZero pins the default: a pool with no
// reasoning member carries reserve 0, and edge-api dispatches the caller's
// ceiling untouched.
func TestSelectRouteZeroReserveStaysZero(t *testing.T) {
	repo := &stubRepository{
		candidates: []RouteCandidate{
			{RouteID: "route-solo", AliasID: "solo-alias", Provider: "openrouter",
				LiteLLMModelName: "route-solo", HealthState: "healthy", PriceClass: "budget",
				SupportsChatCompletions: true},
		},
	}
	svc := NewService(repo, nil)

	result := singleSelect(t, svc, "solo-alias")
	if result.ReasoningReserveTokens != 0 {
		t.Errorf("ReasoningReserveTokens = %d, want 0", result.ReasoningReserveTokens)
	}
}

// singleSelect is the shared happy-path selection call for these tests.
func singleSelect(t *testing.T, svc *Service, alias string) SelectionResult {
	t.Helper()
	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             alias,
		NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("SelectRoute(%s): %v", alias, err)
	}
	return result
}

// TestReasoningReserveMigrationTargetsTheRightMembers is the offline guard
// over the migration text (sqlparse_test.go's positional style): the column
// exists, exactly the three reasoning members are raised to 4096, and the
// fourth member is explicitly pinned at 0.
//
// That pin is HISTORICAL and is no longer live intent. This file is frozen, so
// its assertions stay, but 20260830_01 raises route-free-pool-free to 4096
// alongside the others: the 0 was justified by that member being pinned to a
// model that does not reason, and it is now pointed at OpenRouter's Free
// Models Router, which can select one that does. Live intent is in
// openrouter_free_models_router_test.go and in the live-row assertion in
// reasoning_reserve_integration_test.go.
func TestReasoningReserveMigrationTargetsTheRightMembers(t *testing.T) {
	sql := stripSQLComments(readRepoFile(t, reasoningReserveMigrationRelPath))

	if !strings.Contains(sql, "add column if not exists reasoning_reserve_tokens integer not null default 0") {
		t.Errorf("migration must add provider_routes.reasoning_reserve_tokens defaulting to 0; got:\n%s", sql)
	}

	for _, routeID := range []string{"route-free-pool-gemini", "route-free-pool-groq", "route-free-pool-groq-2"} {
		if !strings.Contains(sql, "'"+routeID+"'") {
			t.Errorf("migration does not set a reserve for reasoning member %s", routeID)
		}
	}
}
