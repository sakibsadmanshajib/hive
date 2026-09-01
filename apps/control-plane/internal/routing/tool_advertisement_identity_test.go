package routing

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

// A6, and the reason slice S3 of the web tools spec needed its own guard.
//
// THE RISK
//
// edge-api folds tools, tool_choice, response_format and functions into one
// RequireToolCapable bool, and SelectRoute then narrows the candidate set to
// tool-capable routes. Advertising the two web tools by default therefore
// touches route selection on EVERY chat turn, not only the turns that call a
// tool. The free pool is two Groq routes since PR #1556 disabled the Gemini and
// OpenRouter members, so a narrowing has almost no room: it would concentrate
// every chat turn on fewer pool members.
//
// THE CONTAINMENT, WHICH THIS HOLDS
//
// Advertisement is decided from hive_capabilities.tools, which is
// catalog.ToolCapableAliases, which reports true only when every enabled route
// of the alias is tool capable. Filtering an all-capable set is the identity
// function, so the flag removes nothing. That is an assertion, not a hope, and
// this is the assertion: for every alias the model list advertises tools on,
// SelectRoute must return the byte-identical selection with and without the
// flag, over the catalog the migration chain actually produces.
//
// WHY IT CANNOT PASS VACUOUSLY
//
// An empty catalog satisfies for-every trivially, and a zero-versus-zero
// comparison passes a naive equality test while proving nothing. So the test
// also requires that a real comparison happened: at least one alias compared,
// and hive-free specifically advertised, since that is the alias the chat
// surface runs on. If the catalog ever stops being uniformly tool-capable
// there, this goes red rather than quietly comparing nothing.

// advertisedAliases derives hive_capabilities.tools for every alias from the
// folded migration chain, through the SAME function the model list publishes.
// Not a second copy of the rule: a copy would let the published claim and the
// checked claim drift, which is the whole failure this slice is about.
func advertisedAliases(t *testing.T, state effectiveCatalog) map[string]bool {
	t.Helper()

	rows := make([]catalog.RouteToolCapability, 0, len(state.routes))
	for routeID, route := range state.routes {
		caps, ok := state.caps[routeID]
		if !ok {
			// Inner join, mirroring both ListRouteCandidates and
			// ListRouteToolCapabilities: a route with no capabilities row can
			// never be selected, so it must not veto an alias it cannot serve.
			continue
		}
		rows = append(rows, catalog.RouteToolCapability{
			AliasID:        route["alias_id"],
			HealthState:    route["health_state"],
			ToolsSupported: isTrue(caps["tools_supported"]),
		})
	}
	if len(rows) == 0 {
		t.Fatal("the migration chain folded to no route-plus-capability rows at all; this guard is reading the wrong tables, and every assertion below would pass over nothing")
	}

	return catalog.ToolCapableAliases(rows)
}

// aliasCandidates builds RouteCandidates for one alias out of the folded
// migration state, so these tests read the rows the database will actually hold
// rather than a fixture somebody has to remember to update.
func aliasCandidates(t *testing.T, state effectiveCatalog, aliasID string) []RouteCandidate {
	t.Helper()

	var routeIDs []string
	for routeID, route := range state.routes {
		if route["alias_id"] == aliasID {
			routeIDs = append(routeIDs, routeID)
		}
	}
	sort.Strings(routeIDs)
	if len(routeIDs) == 0 {
		t.Fatalf("the migration chain leaves no provider_routes row on alias %s at all", aliasID)
	}

	candidates := make([]RouteCandidate, 0, len(routeIDs))
	for _, routeID := range routeIDs {
		route := state.routes[routeID]
		caps, ok := state.caps[routeID]
		if !ok {
			t.Fatalf("no provider_capabilities row survives the migration chain for %s", routeID)
		}
		priority := 10
		if p, err := strconv.Atoi(strings.TrimSpace(route["priority"])); err == nil {
			priority = p
		}
		candidates = append(candidates, RouteCandidate{
			RouteID:                 routeID,
			AliasID:                 route["alias_id"],
			Provider:                route["provider"],
			ProviderModel:           route["provider_model"],
			LiteLLMModelName:        route["litellm_model_name"],
			PriceClass:              route["price_class"],
			HealthState:             route["health_state"],
			Priority:                priority,
			SupportsResponses:       isTrue(caps["supports_responses"]),
			SupportsChatCompletions: isTrue(caps["supports_chat_completions"]),
			SupportsCompletions:     isTrue(caps["supports_completions"]),
			SupportsEmbeddings:      isTrue(caps["supports_embeddings"]),
			SupportsStreaming:       isTrue(caps["supports_streaming"]),
			SupportsReasoning:       isTrue(caps["supports_reasoning"]),
			SupportsCacheRead:       isTrue(caps["supports_cache_read"]),
			SupportsCacheWrite:      isTrue(caps["supports_cache_write"]),
			SupportsImageGeneration: isTrue(caps["supports_image_generation"]),
			SupportsImageEdit:       isTrue(caps["supports_image_edit"]),
			SupportsBatch:           isTrue(caps["supports_batch"]),
			SupportsTools:           isTrue(caps["tools_supported"]),
		})
	}
	return candidates
}

func TestAdvertisingToolsNeverNarrowsTheCandidateSet(t *testing.T) {
	state := foldMigrations(t)
	advertised := advertisedAliases(t, state)

	aliasIDs := make([]string, 0, len(advertised))
	for aliasID, tools := range advertised {
		if tools {
			aliasIDs = append(aliasIDs, aliasID)
		}
	}
	sort.Strings(aliasIDs)

	compared := 0
	for _, aliasID := range aliasIDs {
		candidates := aliasCandidates(t, state, aliasID)

		// The candidate-count half of A6, read straight off the catalog: on an
		// advertised alias, the set the tool filter keeps and the set it starts
		// from are the same size.
		//
		// Attribution, corrected after a reviewer mutated the predicate to
		// at-least-one-capable-group and found this line stayed green: it does
		// NOT catch that loosening today, because no alias in the folded chain
		// has a mixed capability set, so a loosened rule advertises the same ten
		// aliases and enabled still equals capable for each. What catches the
		// loosening is TestToolCapableAliases, on two subtests, and
		// TestTheAdvertisementIdentityCheckCanFail, on its synthetic mixed
		// fixture. This line is the same statement made against real catalog
		// data, and it starts biting the day the catalog grows a mixed alias.
		enabled, capable := 0, 0
		for _, candidate := range candidates {
			if strings.EqualFold(candidate.HealthState, "disabled") {
				continue
			}
			enabled++
			if candidate.SupportsTools {
				capable++
			}
		}
		if enabled == 0 {
			t.Errorf("alias %s is advertised as tool capable with no enabled route at all", aliasID)
			continue
		}
		if capable != enabled {
			t.Errorf("alias %s advertises tools but only %d of its %d enabled routes are tool capable; setting RequireToolCapable would drop %d of them and concentrate traffic on the rest", aliasID, capable, enabled, enabled-capable)
			continue
		}

		newStub := func() *stubRepository {
			return &stubRepository{
				policy:     catalog.AliasPolicySnapshot{AliasID: aliasID, PolicyMode: "pinned"},
				candidates: candidates,
			}
		}
		input := SelectionInput{
			AliasID:             aliasID,
			NeedChatCompletions: true,
			NeedStreaming:       true,
		}
		withTools := input
		withTools.RequireToolCapable = true

		plain, plainErr := NewService(newStub(), &stubEntitlements{visible: true}).SelectRoute(context.Background(), input)
		tooled, toolErr := NewService(newStub(), &stubEntitlements{visible: true}).SelectRoute(context.Background(), withTools)

		if plainErr != nil {
			// An alias with no chat-completions route at all refuses both ways.
			// That is not a narrowing, but it must refuse for the SAME reason,
			// and it must never be the tool filter that did it.
			if toolErr == nil {
				t.Errorf("alias %s refused a plain turn (%v) and admitted a tool turn; the flag cannot widen anything", aliasID, plainErr)
			}
			if errors.Is(toolErr, ErrNoCapableRoute) {
				t.Errorf("alias %s is advertised as tool capable yet the tool filter itself refused it: %v", aliasID, toolErr)
			}
			continue
		}
		if toolErr != nil {
			t.Errorf("alias %s resolves for a plain turn but not with RequireToolCapable set: %v. This is the narrowing this test exists to refuse", aliasID, toolErr)
			continue
		}
		if !reflect.DeepEqual(plain, tooled) {
			t.Errorf("alias %s selected %+v plain and %+v with tools; advertising tools changed route selection", aliasID, plain, tooled)
			continue
		}
		compared++
	}

	// Anti-vacuity. Both of these fail loudly rather than letting an empty or
	// narrowed catalog pass a for-every loop over nothing.
	if compared == 0 {
		t.Error("no alias was actually compared, so the equality above proves nothing. Either the catalog advertises tools nowhere, or the fold read no routes")
	}
	t.Logf("compared %d advertised alias(es) with and without RequireToolCapable: %v", compared, aliasIDs)

	// Blast radius of the other half of this slice, reported rather than
	// asserted. Setting RequireToolCapable on the chat path means a body
	// carrying tools, tool_choice, response_format or functions now gets a
	// clean 400 on an alias that is not advertised, where before it was
	// dispatched unchecked and failed upstream (or did not, by luck). These are
	// the aliases that behaviour change can reach. An empty list means the
	// change can refuse nothing the catalog can currently serve; a growing one
	// is worth a second look, because Open WebUI sends response_format for its
	// title and tag tasks.
	var chatCapableButNotAdvertised []string
	for aliasID := range advertised {
		if advertised[aliasID] {
			continue
		}
		for _, candidate := range aliasCandidates(t, state, aliasID) {
			if strings.EqualFold(candidate.HealthState, "disabled") || !candidate.SupportsChatCompletions {
				continue
			}
			chatCapableButNotAdvertised = append(chatCapableButNotAdvertised, aliasID)
			break
		}
	}
	sort.Strings(chatCapableButNotAdvertised)
	t.Logf("chat-capable aliases NOT advertised, so a tool-shaped body on them now answers 400 instead of being dispatched unchecked: %v", chatCapableButNotAdvertised)

	if !advertised[freePoolAliasID] {
		t.Errorf("alias %s is not advertised as tool capable. It is the alias the chat surface runs on, so the web tools would be advertised to nobody. Uniformity there is held by TestFreePoolIsUniformlyToolCapable; if that one is red too, fix it first", freePoolAliasID)
	}
}

// TestTheAdvertisementIdentityCheckCanFail is the proof that the check above is
// not a tautology. A guard that cannot go red is worse than no guard, because
// it reads as evidence.
//
// The fixture is the shape the identity check exists to catch and the shape
// ToolCapableAliases exists to refuse advertising on: one alias, two gateway
// groups, one capable and one not, with the incapable group at the better
// priority. Plain selection takes the incapable group. Setting the flag drops
// that group entirely and selection moves. If the model list ever advertised
// tools on an alias like this, every chat turn would silently move to the other
// group, which on a two-member free pool is the whole hazard.
func TestTheAdvertisementIdentityCheckCanFail(t *testing.T) {
	candidates := []RouteCandidate{
		{
			RouteID:                 "route-mixed-plain",
			AliasID:                 "mixed-alias",
			Provider:                "provider-a",
			LiteLLMModelName:        "group-plain",
			PriceClass:              "standard",
			HealthState:             "healthy",
			Priority:                10,
			SupportsChatCompletions: true,
			SupportsStreaming:       true,
			SupportsTools:           false,
		},
		{
			RouteID:                 "route-mixed-capable",
			AliasID:                 "mixed-alias",
			Provider:                "provider-b",
			LiteLLMModelName:        "group-capable",
			PriceClass:              "standard",
			HealthState:             "healthy",
			Priority:                20,
			SupportsChatCompletions: true,
			SupportsStreaming:       true,
			SupportsTools:           true,
		},
	}

	rows := []catalog.RouteToolCapability{
		{AliasID: "mixed-alias", HealthState: "healthy", ToolsSupported: false},
		{AliasID: "mixed-alias", HealthState: "healthy", ToolsSupported: true},
	}
	if catalog.ToolCapableAliases(rows)["mixed-alias"] {
		t.Fatal("an alias with one incapable enabled route was advertised as tool capable; the model list would tell the chat surface to attach tools on it")
	}

	newStub := func() *stubRepository {
		return &stubRepository{
			policy:     catalog.AliasPolicySnapshot{AliasID: "mixed-alias", PolicyMode: "pinned"},
			candidates: candidates,
		}
	}
	input := SelectionInput{
		AliasID:             "mixed-alias",
		NeedChatCompletions: true,
		NeedStreaming:       true,
	}
	withTools := input
	withTools.RequireToolCapable = true

	plain, err := NewService(newStub(), &stubEntitlements{visible: true}).SelectRoute(context.Background(), input)
	if err != nil {
		t.Fatalf("plain selection on the mixed fixture: %v", err)
	}
	tooled, err := NewService(newStub(), &stubEntitlements{visible: true}).SelectRoute(context.Background(), withTools)
	if err != nil {
		t.Fatalf("tool selection on the mixed fixture: %v", err)
	}
	if reflect.DeepEqual(plain, tooled) {
		t.Fatal("the mixed fixture selected identically with and without RequireToolCapable, so TestAdvertisingToolsNeverNarrowsTheCandidateSet cannot detect a narrowing at all and its green means nothing")
	}
}
