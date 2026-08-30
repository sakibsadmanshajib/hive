package routing

import (
	"strings"
	"testing"
)

// Offline guards over the 2026-08-30 migration that moves the free pool's
// OpenRouter member off ONE pinned free model and onto OpenRouter's own Free
// Models Router.
//
// Why this file exists rather than an edit to free_pool_router_test.go: that
// file pins what 20260824_02 inserted, and 20260824_02 is a frozen, already
// applied file whose literals must not be rewritten. Live intent is the sum of
// both migrations, so the successor gets its own guards.
const openrouterFreeRouterMigrationRelPath = "supabase/migrations/20260830_01_openrouter_free_models_router.sql"

// freeModelsRouterID is OpenRouter's own "pick a currently free model" router,
// verified live against https://openrouter.ai/api/v1/models on 2026-08-30:
// id openrouter/free, name "Free Models Router", pricing prompt "0" and
// completion "0". The doubled prefix is the same LiteLLM provider selector
// every sibling row carries.
const freeModelsRouterProviderModel = "openrouter/openrouter/free"

func openrouterFreeRouterSQL(t *testing.T) string {
	t.Helper()
	return stripSQLComments(readRepoFile(t, openrouterFreeRouterMigrationRelPath))
}

// TestOpenRouterPoolMemberMovesToTheFreeModelsRouter is the directive itself:
// the OpenRouter member routes to the free TIER, not to one model that
// OpenRouter can rate limit or retire without telling us.
func TestOpenRouterPoolMemberMovesToTheFreeModelsRouter(t *testing.T) {
	assigns := updateAssignments(openrouterFreeRouterSQL(t), "public.provider_routes", "route_id")

	row, ok := assigns["route-free-pool-free"]
	if !ok {
		t.Fatalf("%s does not update route-free-pool-free", openrouterFreeRouterMigrationRelPath)
	}
	if got := row["provider_model"]; got != freeModelsRouterProviderModel {
		t.Fatalf("route-free-pool-free provider_model = %q, want %q", got, freeModelsRouterProviderModel)
	}
}

// TestOpenRouterPoolMemberPinsNoSingleModel is the defect being fixed, stated
// as a property rather than as one banned string. A vendor-scoped slug with a
// :free suffix IS a pinned free model, whichever vendor it names.
func TestOpenRouterPoolMemberPinsNoSingleModel(t *testing.T) {
	assigns := updateAssignments(openrouterFreeRouterSQL(t), "public.provider_routes", "route_id")

	got := assigns["route-free-pool-free"]["provider_model"]
	if strings.HasSuffix(got, ":free") {
		t.Errorf("route-free-pool-free provider_model = %q; a :free suffix is a pin on one model, which is the failure mode this migration removes", got)
	}
	if strings.Contains(got, "dots-3-note-preview") {
		t.Errorf("route-free-pool-free provider_model = %q; still the pinned model", got)
	}
}

// TestOpenRouterPoolMemberGetsAReasoningReserve.
//
// 20260826_01 pinned route-free-pool-free at reasoning_reserve_tokens 0, with
// the stated reason that "openrouter's dots-studio/dots-3-note-preview:free
// does not reason, so its deployments keep exactly the ceiling the caller
// asked for". That reason dies with the pin. openrouter/free selects among
// whatever is free at that moment and advertises `reasoning` and
// `include_reasoning` in its supported_parameters, so a reasoning-capable
// model can now answer on this member, while provider_capabilities has
// declared supports_reasoning TRUE for this route since 20260824_02.
//
// Scope, corrected after review so this comment does not mislead: SelectRoute
// takes the MAX reserve across the group (service.go:154-158), and the three
// siblings already carry 4096, so the pool's effective reserve does not move.
// This makes the row honest and covers the degenerate case where this member is
// the sole eligible candidate. It is not a live instance of issue #1171.
func TestOpenRouterPoolMemberGetsAReasoningReserve(t *testing.T) {
	assigns := updateAssignments(openrouterFreeRouterSQL(t), "public.provider_routes", "route_id")

	row, ok := assigns["route-free-pool-free"]
	if !ok {
		t.Fatalf("%s does not update route-free-pool-free", openrouterFreeRouterMigrationRelPath)
	}
	got, present := row["reasoning_reserve_tokens"]
	if !present {
		t.Fatalf("%s repoints route-free-pool-free at a router that can select a reasoning model but leaves reasoning_reserve_tokens at the 0 that 20260826_01 set for a model that could not reason", openrouterFreeRouterMigrationRelPath)
	}
	if got != "4096" {
		t.Errorf("route-free-pool-free reasoning_reserve_tokens = %q, want 4096 to match the pool's other reasoning members", got)
	}
}

// TestOpenRouterFreeRouterMigrationLeavesTheOtherMembersAlone. The pool's
// value is that four keys fail over for each other; a migration that touches
// a second member while repointing the first would shrink it silently.
func TestOpenRouterFreeRouterMigrationLeavesTheOtherMembersAlone(t *testing.T) {
	assigns := updateAssignments(openrouterFreeRouterSQL(t), "public.provider_routes", "route_id")

	for _, routeID := range []string{"route-free-pool-gemini", "route-free-pool-groq", "route-free-pool-groq-2"} {
		if _, touched := assigns[routeID]; touched {
			t.Errorf("%s updates %s; only the OpenRouter member is in scope", openrouterFreeRouterMigrationRelPath, routeID)
		}
	}
}

// TestOpenRouterFreeRouterMigrationIsRerunnable. Every migration in this repo
// is applied by scripts/apply-migrations.sh, which records what it ran, but a
// hand re-run must still be inert rather than clobbering a later correction.
func TestOpenRouterFreeRouterMigrationIsRerunnable(t *testing.T) {
	sql := openrouterFreeRouterSQL(t)
	lower := strings.ToLower(sql)

	if !strings.Contains(lower, "provider_model <>") && !strings.Contains(lower, "provider_model !=") {
		t.Errorf("%s has no provider_model guard in its WHERE clause; a re-run would overwrite whatever the row holds by then", openrouterFreeRouterMigrationRelPath)
	}
}

// TestOpenRouterFreeRouterMigrationDoesNotRepriceAnything. hive-free's
// customer price is an owner ruling (D-048) and is not derived from what the
// upstream costs, so a routing change must not drift into the money columns.
func TestOpenRouterFreeRouterMigrationDoesNotRepriceAnything(t *testing.T) {
	lower := strings.ToLower(openrouterFreeRouterSQL(t))

	for _, banned := range []string{
		"public.model_aliases",
		"input_price_credits",
		"output_price_credits",
		"pricing_mode",
	} {
		if strings.Contains(lower, banned) {
			t.Errorf("%s mentions %q; this migration repoints one route and must touch no pricing", openrouterFreeRouterMigrationRelPath, banned)
		}
	}
}
