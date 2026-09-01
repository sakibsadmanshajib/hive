package routing

import (
	"sort"
	"strings"
	"testing"
)

// The capacity half of the hive-free pool, which is issue #1566.
//
// WHAT HAPPENED
//
// The pool's Gemini member ran on Google's free tier, which capped it at 20
// requests per day on the model `gemini-flash-latest` resolved to. Measured
// over 43 to 48 hours of LiteLLM container logs (2026-08-29 00:00 to
// 2026-08-30 19:00 UTC) it produced 435 rate-limit responses, 100 percent of
// every 429 the pool emitted, spread across nearly every hour of both days
// rather than clustered. So it contributed at most 20 successes a day and 435
// failures in two: the pool served users better without it.
//
// WHY LiteLLM COULD NOT ABSORB THIS ON ITS OWN
//
// A rate limit takes the member out of rotation for the cooldown and no
// longer, and the effective cooldown on this stack is 5 seconds
// (deploy/litellm/config.yaml records the runtime confirmation, not an
// inference: `allowed_fails` and `cooldown_time` sit under litellm_settings
// where the Router never reads them, so DEFAULT_COOLDOWN_TIME_SECONDS applies).
// Five seconds is the right answer for a per-minute burst and the wrong one for
// an allowance that resets once a day: the exhausted member is put straight
// back into the group and drawn again, all day, every day.
//
// LiteLLM cannot tell those two apart, and it cannot be told. Read from
// litellm/types/router.py at the pinned image tag v1.98.0 on 2026-09-01: a
// deployment's `rpm` and `tpm` are tracked under cache keys ending
// `:{current_minute}`, so the only request-rate window the router models is a
// minute; `max_budget` with `budget_duration` is spend-based and every member
// of this pool costs zero, so nothing ever accrues against it. A per-deployment
// `cooldown_time` IS expressible (LiteLLMParamsTypedDict carries it), but it is
// a fixed duration counted from a failure the router has already misclassified,
// so setting it to a day would evict a member for a day on one transient
// minute-limit 429. That is issue #1566's option 2, and it is the reason the
// option was assessed and not taken.
//
// THE QUOTA CHECK THE ISSUE ASKED FOR, RUN 2026-09-01
//
// Acceptance criterion 1 asked for Google's current free-tier daily request
// quota for the candidate models, checked against live documentation. It was
// checked, and the finding is that the figure is no longer published:
// https://ai.google.dev/gemini-api/docs/rate-limits carries no per-model
// free-tier RPM/TPM/RPD table at all any more. Its "Gemini API rate limits"
// section says the limits "depend on a variety of factors (such as your usage
// tier) and can be viewed in Google AI Studio", and adds that "specified rate
// limits are not guaranteed and actual capacity may vary". The only per-model
// numbers still on the page are Batch API enqueued-token ceilings, which this
// member does not use. So option 1, repointing the member at a
// higher-allowance Google model, cannot be chosen on evidence: the allowance
// is readable only from AI Studio for the specific project, and GEMINI_API_KEY
// exists only as a repository secret.
//
// THE DECISION
//
// Option 3, the member leaves the pool. It is already the state of the tree:
// 20260830_03_free_pool_capability_truth.sql disabled this row while making
// the pool's tool claim uniform, for the separate reason that its capability
// could not be measured. That migration says outright that it "partly
// addresses" this issue and leaves the capacity question open. This file
// closes it, and is the thing that keeps it closed: the capability guard in
// free_pool_capability_truth_test.go deliberately reads membership from
// health_state rather than from a roster, precisely so a capacity decision can
// move a member without turning it red, which means nothing over there objects
// if this member comes back.
//
// Re-enabling it is not forbidden, it is gated. Whoever does it has to change
// this file, and to change this file they have to record the allowance the
// member actually has, which is the fact whose absence cost 48 hours of
// failures.

// freePoolDailyCappedRoute is the member issue #1566 is about, and the reason
// it is out. Named as one route rather than a table because there is exactly
// one, and a map of one entry is a pattern pretending to be a rule.
const freePoolDailyCappedRoute = "route-free-pool-gemini"

// freePoolDailyCappedProvider is the provider slug that route runs through. The
// route id alone is not enough: a second Google row under a different id would
// reintroduce the same allowance, and the guard below would have nothing to say
// about it.
const freePoolDailyCappedProvider = "gemini"

// TestFreePoolExcludesTheDailyCappedMember pins the capacity decision above.
//
// Two assertions, because the roster and the provider are two different ways to
// let the member back in. The first is the exact row; the second is any active
// pool row on the same provider, which catches a re-add under a new route id.
// The ceiling, stated so this file is not over-trusted on its own:
// foldMigrations is an offline approximation of the migration chain and reads
// only INSERT tuples and single-row `UPDATE ... WHERE route_id = '...'`.
// free_pool_capability_truth_test.go documents that limit in full and it
// applies to every assertion below, but the specific shape it misses is worth
// naming here rather than left as a general caveat, because it is the first
// thing somebody re-enabling a whole provider would reach for:
//
//	UPDATE public.provider_routes SET health_state = 'healthy'
//	 WHERE alias_id = 'hive-free' AND provider = 'gemini';
//
// `updateAssignments` keys on `route_id`, so a statement with no `route_id`
// predicate is invisible to both assertions below and this guard would stay
// green through it. Closing that needs the offline reader to understand
// predicate-form updates, which is a change to the shared fold rather than to
// this file, and it is not attempted here.
func TestFreePoolExcludesTheDailyCappedMember(t *testing.T) {
	state := foldMigrations(t)

	route, ok := state.routes[freePoolDailyCappedRoute]
	if !ok {
		t.Fatalf("no provider_routes row survives the migration chain for %s. Either it was DELETED, or the fold stopped reading the statement that inserts it. The member is meant to sit at health_state 'disabled' rather than be deleted, so that the reason it left stays readable; if deleting it was deliberate, this guard has to be rewritten rather than made to pass", freePoolDailyCappedRoute)
	}
	if !strings.EqualFold(route["health_state"], "disabled") {
		t.Errorf(
			"%s carries health_state = %q, want \"disabled\". This member is capped at 20 requests per day on Google's free tier and produced 435 rate-limit failures in 48 hours (issue #1566). LiteLLM cools a rate-limited member down for 5 seconds and then draws it again, so an active row here means the pool spends the day routing traffic to a member that cannot answer it. If the allowance has genuinely changed, record the new figure and where it was read from in this file's header, then change this guard deliberately.",
			freePoolDailyCappedRoute, route["health_state"],
		)
	}

	var offenders []string
	for routeID, row := range state.routes {
		if row["alias_id"] != freePoolAliasID {
			continue
		}
		if strings.EqualFold(row["health_state"], "disabled") {
			continue
		}
		// Case-insensitive on purpose. These values are folded out of raw
		// migration text rather than read from the database, so nothing has
		// normalised them, and a row inserted as 'Gemini' would otherwise
		// satisfy this assertion while failing its intent.
		if strings.EqualFold(row["provider"], freePoolDailyCappedProvider) {
			offenders = append(offenders, routeID)
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf(
			"active %s members on provider %q: %v. Whatever the route id, this is the same free-tier project and the same daily allowance issue #1566 removed. Google no longer publishes that allowance (checked 2026-09-01), so a member here needs its quota read from AI Studio and written into this file's header before it can be active.",
			freePoolAliasID, freePoolDailyCappedProvider, offenders,
		)
	}
}
