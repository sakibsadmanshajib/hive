package routing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

// Guards over the capability truth of the hive-free pool, and over the shape
// that makes a per-member capability claim mean anything at all.
//
// THE DEFECT THESE EXIST FOR
//
// 20260824_02_free_pool_router.sql seeded all four pool members with
// tools_supported = false, on the honest but untested reasoning that
// cross-member tool parity had never been probed. Nobody probed it afterwards
// either, so the placeholder became the catalog's answer. tools_supported is
// the column PR #206 wired to tools, tool_choice AND response_format, so the
// gateway answered its own 400 ("does not support parameter: response_format")
// to every structured-output request on hive-free, and CI moved its capability
// lane onto a paid alias to get around it. An under-claim is not the safe
// direction: it is a wrong answer that nothing can distinguish from a correct
// one.
//
// The probes are recorded in 20260830_03_free_pool_capability_truth.sql's
// header, per member, with the date and the method. They did NOT all come back
// the same, which is the part that shaped the fix: both Groq slots pass
// cleanly, Gemini is documented rather than probed, and the OpenRouter member
// is a per-request router whose answer to an identical strict json_schema
// request conformed once in five attempts.
//
// hive-free stays ONE group by owner decision: it and a tools variant cannot be
// two endpoints, they must be one model. A group can only honestly declare what
// its weakest member supports, so the pool is made UNIFORM instead of split.
// The OpenRouter member leaves it. Pinning that member to a specific capable
// model was tried first and failed on the same evidence, per the migration
// header: no zero-priced OpenRouter model honours a strict schema reliably.
//
// WHY THE SECOND GUARD IS ABOUT GROUPS, NOT MODELS
//
// A test cannot re-probe a vendor offline, and a hardcoded per-model
// expectation table would rot into exactly the stale claim above. What IS
// checkable offline, and is the failure mode that actually bites, is
// coherence: members of a group share one litellm_model_name, LiteLLM
// load-balances every deployment under that name, edge-api dispatches
// route.LiteLLMModelName and never the route id, and SelectRoute's own
// reasoning-reserve block already states that it cannot know which member will
// answer before it dispatches. So a per-route capability flag inside a shared
// group is only meaningful if every member of the group agrees. A group whose
// members disagree makes the tool gate decorative: selection filters to the
// capable row, dispatch hands the request to the group, and a member that
// cannot serve it answers.

const freePoolCapabilityMigrationRelPath = "supabase/migrations/20260830_03_free_pool_capability_truth.sql"

// freePoolAliasID is the alias every pool member row hangs off.
const freePoolAliasID = "hive-free"

// migrationsDirRelPath is the folder scripts/apply-migrations.sh applies in
// filename order. The fold below reads the same set in the same order.
const migrationsDirRelPath = "supabase/migrations"

// effectiveCatalog is the catalog state the migration chain leaves behind, as
// far as it can be read offline: INSERT tuples folded in filename order, then
// single-row UPDATEs applied over them.
//
// Deliberately approximate, and the approximation is stated rather than
// hidden. It reads the two statement shapes sqlparse_test.go understands:
// INSERT ... VALUES (first row wins, matching the ON CONFLICT DO NOTHING every
// catalog migration here carries) and UPDATE ... WHERE route_id = '...'. It
// does NOT read join-form or IN-list updates (20260612_01 and 20260414_01 are
// both join-form), so a flag those set is invisible here. That costs nothing
// for the coherence check below, which compares members of a group against
// each other: a statement that misses one member of a group misses all of
// them, since they are written as one tuple list.
type effectiveCatalog struct {
	routes map[string]map[string]string
	caps   map[string]map[string]string
}

func migrationFiles(t *testing.T) []string {
	t.Helper()

	dir := filepath.Join(repoRoot(t), filepath.FromSlash(migrationsDirRelPath))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", migrationsDirRelPath, err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("no migrations found under %s", migrationsDirRelPath)
	}
	return names
}

func foldMigrations(t *testing.T) effectiveCatalog {
	t.Helper()

	state := effectiveCatalog{
		routes: map[string]map[string]string{},
		caps:   map[string]map[string]string{},
	}

	for _, name := range migrationFiles(t) {
		sql := stripSQLComments(readRepoFile(t, migrationsDirRelPath+"/"+name))

		applyInserts(state.routes, insertRows(sql, "public.provider_routes"))
		applyInserts(state.caps, insertRows(sql, "public.provider_capabilities"))
		applyUpdates(state.routes, updateAssignments(sql, "public.provider_routes", "route_id"))
		applyUpdates(state.caps, updateAssignments(sql, "public.provider_capabilities", "route_id"))
	}

	return state
}

// applyInserts folds INSERT tuples in. First writer wins: every catalog
// migration in this repo ends its route and capability inserts with
// ON CONFLICT (route_id) DO NOTHING, so a later file re-inserting the same
// route_id changes nothing in the database and must change nothing here.
func applyInserts(into map[string]map[string]string, rows []map[string]string) {
	for _, row := range rows {
		routeID := row["route_id"]
		if routeID == "" {
			continue
		}
		if _, exists := into[routeID]; exists {
			continue
		}
		copied := make(map[string]string, len(row))
		for k, v := range row {
			copied[k] = v
		}
		into[routeID] = copied
	}
}

// applyUpdates overwrites the named columns of an existing row. An UPDATE
// against a route no migration inserted is ignored rather than invented: it
// would be a no-op against the real database too.
func applyUpdates(into map[string]map[string]string, assignments map[string]map[string]string) {
	for routeID, assigns := range assignments {
		row, exists := into[routeID]
		if !exists {
			continue
		}
		for col, val := range assigns {
			row[col] = val
		}
	}
}

func isTrue(v string) bool { return strings.EqualFold(strings.TrimSpace(v), "true") }

// foldedCapabilityColumns returns every capability flag column present anywhere in
// the folded state. Derived from the data rather than listed, so a column
// added by a future migration is covered the day it appears instead of the day
// somebody remembers to extend a list here.
func foldedCapabilityColumns(caps map[string]map[string]string) []string {
	seen := map[string]bool{}
	for _, row := range caps {
		for col := range row {
			if strings.HasPrefix(col, "supports_") || col == "tools_supported" {
				seen[col] = true
			}
		}
	}
	cols := make([]string, 0, len(seen))
	for col := range seen {
		cols = append(cols, col)
	}
	sort.Strings(cols)
	return cols
}

// freePoolKnownMembers maps every route this alias has ever carried to the
// provider slug it runs through, so a typo in a provider column is still caught.
// Membership of the LIVE pool is NOT read from here: it is read from the folded
// health_state, deliberately.
//
// The invariant this file defends is uniformity, not a roster. Naming three
// specific routes as "must be active" would make an unrelated capacity decision
// fail this guard for the wrong reason: the Gemini member is capped at 20
// requests a day on Google's free tier (issue #1566) and may be disabled or
// repointed by a change that has nothing to do with capability. That change
// must not turn this guard red, and equally it must not be able to quietly
// empty the pool. Hence: whoever is active must be uniform, and enough of them
// must remain.
var freePoolKnownMembers = map[string]string{
	"route-free-pool-free":   "openrouter",
	"route-free-pool-gemini": "gemini",
	"route-free-pool-groq":   "groq",
	"route-free-pool-groq-2": "groq-2",
}

// freePoolRetiredMember is the member that leaves so the rest can make an
// honest claim. It is the OpenRouter one, now on the Free Models Router, which
// picks a different model per request: of the 20 zero-priced models only 10
// support response_format at all, and five identical strict-schema probes
// conformed once. It is disabled rather than given a flag it cannot honour.
const freePoolRetiredMember = "route-free-pool-free"

// freePoolMinimumActiveMembers is the floor below which the pool has stopped
// being a load-balanced pool. Two, so one exhausted key still has somewhere to
// fail over to, which is the whole reason the group exists.
const freePoolMinimumActiveMembers = 2

// TestFreePoolIsUniformlyToolCapable is the correction itself, and the shape
// the owner's one-endpoint rule forces.
//
// Every ACTIVE member of the single group carries tools_supported, so the claim
// holds whichever member LiteLLM picks. The member that could not hold it is
// out of the group rather than dragging the group's claim down to false, which
// is what the original seeding did to three capable members.
func TestFreePoolIsUniformlyToolCapable(t *testing.T) {
	state := foldMigrations(t)

	active := 0
	for routeID, wantProvider := range freePoolKnownMembers {
		route, ok := state.routes[routeID]
		if !ok {
			t.Errorf("no provider_routes row survives the migration chain for pool member %s", routeID)
			continue
		}
		if route["alias_id"] != freePoolAliasID {
			t.Errorf("pool member %s hangs off alias %q, want %q", routeID, route["alias_id"], freePoolAliasID)
		}
		if route["provider"] != wantProvider {
			t.Errorf("pool member %s provider = %q, want %q", routeID, route["provider"], wantProvider)
		}
		if route["litellm_model_name"] != freePoolGroupName {
			t.Errorf("pool member %s litellm_model_name = %q, want the one shared group %q; hive-free is a single endpoint by owner decision, so a second group name here is a second endpoint", routeID, route["litellm_model_name"], freePoolGroupName)
		}

		// A disabled member is emitted by nothing and can answer nothing, so it
		// carries no obligation. Only the live ones have to agree.
		if strings.EqualFold(route["health_state"], "disabled") {
			continue
		}
		active++

		caps, ok := state.caps[routeID]
		if !ok {
			t.Errorf("no provider_capabilities row survives the migration chain for pool member %s; every column defaults false and the group would serve nothing", routeID)
			continue
		}
		if !isTrue(caps["tools_supported"]) {
			t.Errorf("pool member %s carries tools_supported = %q. Every active member must, or the group cannot declare it and hive-free answers its own 400 to every response_format request again", routeID, caps["tools_supported"])
		}
		if !isTrue(caps["supports_chat_completions"]) {
			t.Errorf("pool member %s dropped supports_chat_completions", routeID)
		}
		if !isTrue(caps["supports_reasoning"]) {
			t.Errorf("pool member %s dropped supports_reasoning", routeID)
		}
		if isTrue(caps["supports_embeddings"]) {
			t.Errorf("pool member %s claims supports_embeddings; this is a chat pool", routeID)
		}
		for _, flag := range soleCarrierFlags {
			if isTrue(caps[flag]) {
				t.Errorf("pool member %s claims media flag %s; those live on the auto-router successor, not in the pool", routeID, flag)
			}
		}
	}

	// The retired member must actually be out. Left active it rejoins the
	// group at dispatch time, and the group's claim becomes false again for
	// whatever fraction of requests it answers.
	retired, ok := state.routes[freePoolRetiredMember]
	if !ok {
		t.Fatalf("no provider_routes row for %s at all; this guard is reading the wrong rows", freePoolRetiredMember)
	}
	if !strings.EqualFold(retired["health_state"], "disabled") {
		t.Errorf("%s health_state = %q, want disabled. It serves openrouter/openrouter/free, which picks among the zero-priced catalog per request; of the 20 zero-priced models only 10 list response_format, and five identical strict-schema probes conformed once. An active member that cannot honour the group's claim makes the claim false", freePoolRetiredMember, retired["health_state"])
	}

	if active < freePoolMinimumActiveMembers {
		t.Errorf("only %d active member(s) left on alias %s, want at least %d. Uniformity is satisfied vacuously by an empty pool, so this is the floor that stops a capacity decision from quietly turning a load-balanced pool into a single point of failure", active, freePoolAliasID, freePoolMinimumActiveMembers)
	}

	// Exactly one group under this alias, which is the owner's requirement
	// stated as a check rather than as a comment.
	groups := map[string]bool{}
	for routeID, route := range state.routes {
		if route["alias_id"] != freePoolAliasID || strings.EqualFold(route["health_state"], "disabled") {
			continue
		}
		groups[route["litellm_model_name"]] = true
		if _, known := freePoolKnownMembers[routeID]; !known {
			t.Errorf("unexpected active member %s on alias %s", routeID, freePoolAliasID)
		}
	}
	if len(groups) != 1 || !groups[freePoolGroupName] {
		t.Errorf("alias %s resolves to groups %v, want exactly one, %q. Hive Free and a Hive Free tools variant cannot be two endpoints", freePoolAliasID, groups, freePoolGroupName)
	}
}

// TestRouteGroupsAgreeOnEveryCapability is the non-rotting half. It says
// nothing about which capabilities any model has; it says that members sharing
// a litellm_model_name must claim the SAME ones, because dispatch addresses the
// group and cannot address a member.
//
// This is the guard the free pool needed on the day it was built. Had it
// existed, the choice would have been forced then: probe the members and agree,
// or split the pool into one group per honest profile. What happened instead
// was the weakest common denominator applied to members that did not need it.
func TestRouteGroupsAgreeOnEveryCapability(t *testing.T) {
	state := foldMigrations(t)

	// Group the live routes by the name dispatch actually uses. A disabled
	// route is emitted by nothing and cannot answer, so it cannot make a group
	// incoherent.
	groups := map[string][]string{}
	for routeID, row := range state.routes {
		if strings.EqualFold(row["health_state"], "disabled") {
			continue
		}
		name := row["litellm_model_name"]
		if name == "" {
			continue
		}
		groups[name] = append(groups[name], routeID)
	}

	cols := foldedCapabilityColumns(state.caps)
	if len(cols) == 0 {
		t.Fatal("folded no capability columns at all; the migration reader is broken, not the catalog")
	}

	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		members := groups[name]
		if len(members) < 2 {
			continue
		}
		sort.Strings(members)

		for _, col := range cols {
			first, firstSeen := "", false
			for _, routeID := range members {
				row, ok := state.caps[routeID]
				if !ok {
					t.Errorf("litellm group %q has member %s with no provider_capabilities row; every column defaults false, so that member silently serves nothing the others advertise", name, routeID)
					continue
				}
				val := strings.ToLower(strings.TrimSpace(row[col]))
				if !firstSeen {
					first, firstSeen = val, true
					continue
				}
				if val != first {
					t.Errorf("litellm group %q disagrees on %s (%s claims %q, another member claims %q). LiteLLM load-balances every deployment under one model_name, so the catalog cannot express a per-member capability inside a group: either probe and agree, or split the group so each profile has its own litellm_model_name", name, col, routeID, val, first)
				}
			}
		}
	}
}

// TestFreePoolPassesTheToolCapabilityGate walks the corrected rows through the
// real selection code, which is the thing that produced the 400. Parsing the
// migration proves the row says true; this proves the row reaching SelectRoute
// with RequireToolCapable set resolves instead of returning ErrNoCapableRoute,
// which is what edge-api's guardToolCapability maps to that 400.
func TestFreePoolPassesTheToolCapabilityGate(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:       freePoolAliasID,
			PolicyMode:    "pinned",
			FallbackOrder: []string{"route-free-pool-groq"},
		},
		candidates: freePoolCandidates(t),
	}

	result, err := NewService(repo, &stubEntitlements{visible: true}).SelectRoute(
		context.Background(),
		SelectionInput{
			AliasID:             freePoolAliasID,
			NeedChatCompletions: true,
			RequireToolCapable:  true,
		},
	)
	if err != nil {
		t.Fatalf("SelectRoute(RequireToolCapable) on %s: %v. This is the exact path behind the gateway's own 400 on response_format", freePoolAliasID, err)
	}
	if result.LiteLLMModelName != freePoolGroupName {
		t.Errorf("selected litellm_model_name = %q, want the pool group %q", result.LiteLLMModelName, freePoolGroupName)
	}
}

// TestFreePoolServesPlainChatFromTheSameEndpoint is the owner's requirement as
// a test: a plain request and a tool request must resolve to the SAME
// litellm_model_name. Two groups would be two endpoints, which is the thing
// this alias is not allowed to become, and it is an easy thing to reintroduce
// by adding one route row with a new group name.
func TestFreePoolServesPlainChatFromTheSameEndpoint(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:       freePoolAliasID,
			PolicyMode:    "pinned",
			FallbackOrder: []string{"route-free-pool-groq"},
		},
		candidates: freePoolCandidates(t),
	}

	result, err := NewService(repo, &stubEntitlements{visible: true}).SelectRoute(
		context.Background(),
		SelectionInput{
			AliasID:             freePoolAliasID,
			NeedChatCompletions: true,
		},
	)
	if err != nil {
		t.Fatalf("SelectRoute for plain chat on %s: %v", freePoolAliasID, err)
	}
	if result.LiteLLMModelName != freePoolGroupName {
		t.Errorf("plain chat selected %q, want the one pool group %q, the same endpoint a tool request resolves to", result.LiteLLMModelName, freePoolGroupName)
	}
}

// TestSelectRouteRefusesToolsWhenAGroupMemberCannotServeThem is the runtime
// half of the coherence rule, and it is why the filter is not a plain "any
// capable route exists" test.
//
// The reserve block a few lines below the filter in SelectRoute already takes
// the MAX reasoning reserve across every member sharing the selected group
// name, because edge-api cannot know which member answers. The tool filter used
// ANY: one capable member admitted the whole group, and the request could then
// be answered by a member that cannot serve it, mid-request, past the gate that
// existed to prevent exactly that. ANY is the unsafe direction of the same
// question the reserve block answers with MAX.
func TestSelectRouteRefusesToolsWhenAGroupMemberCannotServeThem(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{AliasID: "mixed-pool", PolicyMode: "pinned"},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-mixed-capable",
				AliasID:                 "mixed-pool",
				Provider:                "groq",
				LiteLLMModelName:        "route-mixed-pool",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsChatCompletions: true,
				SupportsTools:           true,
			},
			{
				RouteID:                 "route-mixed-plain",
				AliasID:                 "mixed-pool",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-mixed-pool",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsChatCompletions: true,
				SupportsTools:           false,
			},
		},
	}

	_, err := NewService(repo, &stubEntitlements{visible: true}).SelectRoute(
		context.Background(),
		SelectionInput{
			AliasID:             "mixed-pool",
			NeedChatCompletions: true,
			RequireToolCapable:  true,
		},
	)
	if err == nil {
		t.Fatal("SelectRoute admitted a tool request to a group with a member that cannot serve tools; dispatch addresses the group, so that member can answer and fail mid-request")
	}
	if !errors.Is(err, ErrNoCapableRoute) {
		t.Errorf("error = %v, want ErrNoCapableRoute so edge-api answers its documented 400 rather than a generic routing failure", err)
	}
}

// TestSelectRouteStillAdmitsAToolCapableGroupOfOne is the other side of that
// rule: the coherence requirement must not refuse a single-member group, which
// is the shape almost every alias in this catalog has.
func TestSelectRouteStillAdmitsAToolCapableGroupOfOne(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{AliasID: "solo", PolicyMode: "pinned"},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-solo-capable",
				AliasID:                 "solo",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-solo-capable",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsChatCompletions: true,
				SupportsTools:           true,
			},
			{
				RouteID:                 "route-solo-plain",
				AliasID:                 "solo",
				Provider:                "groq",
				LiteLLMModelName:        "route-solo-plain",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                20,
				SupportsChatCompletions: true,
				SupportsTools:           false,
			},
		},
	}

	result, err := NewService(repo, &stubEntitlements{visible: true}).SelectRoute(
		context.Background(),
		SelectionInput{
			AliasID:             "solo",
			NeedChatCompletions: true,
			RequireToolCapable:  true,
		},
	)
	if err != nil {
		t.Fatalf("SelectRoute refused a tool-capable single-member group: %v", err)
	}
	if result.RouteID != "route-solo-capable" {
		t.Errorf("selected %q, want the tool-capable route", result.RouteID)
	}
}

// TestCapabilityTruthMigrationIsRerunnable holds the house style this file's
// migration has to keep: every UPDATE carries a WHERE guard excluding rows
// already at the target value, and every INSERT carries ON CONFLICT DO NOTHING,
// so a second run touches nothing. The applier (scripts/apply-migrations.sh)
// records what it ran, but a migration that is only safe because a ledger row
// exists is one restore away from not being safe.
func TestCapabilityTruthMigrationIsRerunnable(t *testing.T) {
	sql := stripSQLComments(readRepoFile(t, freePoolCapabilityMigrationRelPath))

	updates, inserts := 0, 0
	for _, stmt := range splitStatements(sql) {
		lower := strings.ToLower(strings.TrimSpace(stmt))

		switch {
		case strings.HasPrefix(lower, "update "):
			updates++
			whereAt := strings.Index(lower, " where ")
			if whereAt < 0 {
				t.Errorf("unguarded UPDATE with no WHERE clause: %.120s", stmt)
				continue
			}
			where := lower[whereAt:]
			if !strings.Contains(where, "<>") && !strings.Contains(where, "is distinct from") {
				t.Errorf("UPDATE has no already-at-target guard, so a re-run rewrites the row: %.160s", stmt)
			}
		case strings.HasPrefix(lower, "insert "):
			inserts++
			if !strings.Contains(lower, "on conflict") || !strings.Contains(lower, "do nothing") {
				t.Errorf("INSERT without ON CONFLICT DO NOTHING, so a re-run raises a unique violation and aborts the transaction: %.160s", stmt)
			}
		}
	}
	// The file is all UPDATEs today. INSERTs are still checked above rather
	// than assumed absent, so adding a row later cannot slip an unguarded one
	// past this. What must never be zero is the total: a guard that reads a
	// file it cannot parse reports green over nothing at all, which is the
	// shape this suite exists to refuse.
	if updates+inserts == 0 {
		t.Fatalf("%s parsed as no UPDATE and no INSERT; this guard is reading the wrong file or the statement splitter stopped understanding it", freePoolCapabilityMigrationRelPath)
	}
	if updates == 0 {
		t.Errorf("%s carries no UPDATE at all, which no version of this correction can be true of", freePoolCapabilityMigrationRelPath)
	}
}

// freePoolCandidates builds RouteCandidates for every hive-free route out of
// the folded migration state, so these tests read the rows the database will
// actually hold rather than a fixture somebody has to remember to update.
//
// Selected by alias_id rather than by a route-id list on purpose: a member
// added to either group joins these tests automatically, which is what
// distinguishes this from the file-scoped guard that went on asserting a
// retired model after a repoint.
func freePoolCandidates(t *testing.T) []RouteCandidate {
	t.Helper()

	state := foldMigrations(t)

	var routeIDs []string
	for routeID, route := range state.routes {
		if route["alias_id"] == freePoolAliasID {
			routeIDs = append(routeIDs, routeID)
		}
	}
	sort.Strings(routeIDs)
	if len(routeIDs) == 0 {
		t.Fatalf("the migration chain leaves no provider_routes row on alias %s at all", freePoolAliasID)
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
