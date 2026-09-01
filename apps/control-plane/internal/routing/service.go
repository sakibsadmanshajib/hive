package routing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

var (
	ErrAliasNotFound    = errors.New("routing: alias not found")
	ErrRouteNotEligible = errors.New("routing: no eligible routes")
	// ErrNoCapableRoute is returned when RequireToolCapable=true but no route
	// for the alias has tools_supported=true in provider_capabilities.
	ErrNoCapableRoute = errors.New("routing: no tool-capable route")
	// ErrModelNotEntitled is returned when the requesting tenant is not
	// entitled to the alias (an admin hid it, or it is restricted and the
	// tenant holds no grant). It is a policy verdict, not a capability
	// mismatch and not an outage, so callers map it to 403.
	ErrModelNotEntitled = errors.New("routing: model not entitled for tenant")
	// ErrAliasNotPriced is returned when an alias resolves to a route but
	// carries no usable price: either no public.model_aliases row at all, or
	// a row whose input and output prices are both zero. Selecting it would
	// price every request at nothing, so routing refuses instead. Issue #617.
	ErrAliasNotPriced = errors.New("routing: alias has no usable price")
)

// TenantEntitlements resolves per-tenant model entitlement. *catalog.Service is
// the production implementation; the interface lives here so routing depends on
// the behaviour rather than on a concrete service.
type TenantEntitlements interface {
	IsAliasVisibleToTenant(ctx context.Context, tenantID uuid.UUID, aliasID string) (bool, error)
}

type Service struct {
	repo         Repository
	entitlements TenantEntitlements
}

// NewService wires route selection. entitlements is a required argument rather
// than an option so a deployment cannot omit the per-tenant entitlement source
// by accident; when it is nil, tenant-scoped selections fail closed (see
// SelectRoute) instead of quietly resolving every alias.
func NewService(repo Repository, entitlements TenantEntitlements) *Service {
	return &Service{repo: repo, entitlements: entitlements}
}

func (s *Service) SelectRoute(ctx context.Context, input SelectionInput) (SelectionResult, error) {
	aliasID := strings.TrimSpace(input.AliasID)
	if aliasID == "" {
		return SelectionResult{}, fmt.Errorf("%w: alias_id is required", ErrAliasNotFound)
	}

	policy, err := s.repo.LoadAliasPolicy(ctx, aliasID)
	if err != nil {
		return SelectionResult{}, err
	}

	// Per-tenant entitlement, enforced here because every inference path
	// (JWT chat, RAG chat, audio, images, embeddings) resolves its route
	// through this one function. Enforcing it only on the catalog listing left
	// the admin visibility toggle decorative: a tenant could still invoke a
	// model that had been hidden from it.
	//
	// The verdict comes from catalog.AliasVisibleToTenant, the same predicate
	// behind the catalog listing, so the two can never disagree. An untenanted
	// principal carries uuid.Nil: today that is only the batch executor
	// (control-plane-internal, runs per account, never resolves a tenant).
	// An API key IS tenant-scoped as of D-030 (its account's tenant, via
	// public.tenant_billing_accounts) -- edge-api resolves and binds that
	// tenant before calling here, and fails the request closed itself when
	// no tenant is resolvable, so a uuid.Nil TenantID no longer means
	// "API-key principal, skip this check" the way it used to.
	if input.TenantID != uuid.Nil {
		if s.entitlements == nil {
			// Fail closed. A tenant-scoped request with no entitlement source
			// is refused loudly rather than admitted silently, which is exactly
			// how the original gap went unnoticed.
			return SelectionResult{}, fmt.Errorf("%w: entitlement lookup unavailable", ErrModelNotEntitled)
		}
		entitled, err := s.entitlements.IsAliasVisibleToTenant(ctx, input.TenantID, aliasID)
		if err != nil {
			return SelectionResult{}, fmt.Errorf("routing: entitlement lookup for alias %s: %w", aliasID, err)
		}
		if !entitled {
			return SelectionResult{}, fmt.Errorf("%w: alias %s", ErrModelNotEntitled, aliasID)
		}
	}

	if !aliasAllowed(aliasID, input.AllowedAliases) {
		return SelectionResult{}, fmt.Errorf("%w: alias %s is not allowlisted", ErrRouteNotEligible, aliasID)
	}

	candidates, err := s.repo.ListRouteCandidates(ctx, aliasID)
	if err != nil {
		return SelectionResult{}, err
	}

	// When RequireToolCapable is set, narrow candidates to routes whose whole
	// GATEWAY GROUP is tool-capable. If nothing survives we return
	// ErrNoCapableRoute so the caller can surface a specific 400 rather than a
	// generic routing failure.
	//
	// Group, not route, and that is the whole point. This filter used to admit
	// any candidate row carrying tools_supported, on the reading that route
	// granularity was the safe granularity. It is not, because a route is not
	// what gets dispatched: routes sharing a litellm_model_name are emitted as
	// several deployments under one model_name and LiteLLM load-balances across
	// them, so edge-api cannot choose which member answers. The reserve block
	// below already says exactly this, and answers it by taking the MAX
	// reasoning reserve across the group. ANY was the same question answered in
	// the unsafe direction: one capable member admitted the whole group, and an
	// incapable sibling could then answer a request the gate had already
	// approved, failing mid-request with a provider error instead of the
	// gateway's own clean refusal.
	//
	// Disabled members are excluded from the veto: the config sync does not
	// emit them, so they cannot receive a dispatch and cannot make a live group
	// incapable. That mirrors the health filter a few lines below, which is the
	// only other place this distinction matters.
	if input.RequireToolCapable {
		incapableGroups := make(map[string]bool)
		enabledCount := 0
		for _, c := range candidates {
			if strings.EqualFold(c.HealthState, "disabled") {
				continue
			}
			enabledCount++
			if !c.SupportsTools {
				incapableGroups[c.LiteLLMModelName] = true
			}
		}

		capable := make([]RouteCandidate, 0, len(candidates))
		enabledCapable := 0
		for _, c := range candidates {
			if c.SupportsTools && !incapableGroups[c.LiteLLMModelName] {
				capable = append(capable, c)
				if !strings.EqualFold(c.HealthState, "disabled") {
					enabledCapable++
				}
			}
		}
		if len(capable) == 0 {
			return SelectionResult{}, fmt.Errorf("%w: alias %s has no tool-capable routes", ErrNoCapableRoute, aliasID)
		}
		// A narrowing that is not supposed to be possible, made observable.
		//
		// Tool advertisement is decided upstream from catalog.ToolCapableAliases,
		// which reports true only when every enabled route of the alias is tool
		// capable, so on an advertised alias this filter is the identity and
		// this branch never fires. That property is proven against the MIGRATION
		// CHAIN, at fold time, by TestAdvertisingToolsNeverNarrowsTheCandidateSet.
		// It is not proven at runtime, and it cannot be: provider_routes and
		// provider_capabilities are mutated by the DB-managed provider surface
		// and health_state moves under the health system, while the model list
		// is served from a 30 second cache that clients then cache for longer.
		//
		// So there is a real window where an added or degraded route makes a
		// still-advertised alias non-uniform, every loaded chat surface goes on
		// attaching a tools block, and this filter quietly concentrates traffic
		// on the survivors. On the free pool, two Groq routes since PR #1556,
		// that is a concentration onto one. Silent traffic concentration is
		// exactly the regression this whole slice exists to prevent, so it gets
		// a WARN rather than being invisible.
		//
		// ponytail: a log line, not a metric, because nothing meters this
		// function today. Upgrade path if it ever fires in normal operation:
		// a counter keyed by alias, so the window can be measured rather than
		// grepped for.
		if enabledCapable < enabledCount {
			slog.Warn("tool capability filter narrowed the candidate set",
				"alias", aliasID,
				"enabled", enabledCount,
				"enabled_capable", enabledCapable,
				"detail", "an advertised alias should be uniformly tool capable; traffic for this alias is now concentrated on fewer routes")
		}
		candidates = capable
	}

	filtered := make([]RouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !matchesRequestedCapabilities(candidate, input) {
			continue
		}
		if !providerAllowed(candidate.Provider, input.AllowedProviders) {
			continue
		}
		if strings.EqualFold(candidate.HealthState, "disabled") {
			continue
		}

		filtered = append(filtered, candidate)
	}

	if len(filtered) == 0 {
		return SelectionResult{}, fmt.Errorf("%w: alias %s has no eligible routes", ErrRouteNotEligible, aliasID)
	}

	ordered := orderCandidates(policy, filtered)
	selected := ordered[0]

	// Reasoning headroom is a pool property, not a route property (issue
	// #1171). LiteLLM load-balances every deployment sharing the selected
	// litellm_model_name under that one name, so edge-api cannot know which
	// member will answer before it dispatches; carrying the MAX reserve
	// across those members is what makes the headroom hold whichever one
	// does. Candidates on a different litellm_model_name are a different
	// gateway group and contribute nothing. Computed over filtered, not
	// ordered: eligibility filters already ran, so a disabled member cannot
	// inflate an alias whose healthy members never reserve.
	reserve := 0
	for _, candidate := range filtered {
		if candidate.LiteLLMModelName == selected.LiteLLMModelName && candidate.ReasoningReserveTokens > reserve {
			reserve = candidate.ReasoningReserveTokens
		}
	}

	fallbacks := make([]string, 0, len(ordered)-1)
	for _, candidate := range ordered[1:] {
		if !policy.AllowPriceClassWidening && candidate.PriceClass != selected.PriceClass {
			continue
		}
		fallbacks = append(fallbacks, candidate.RouteID)
	}

	// Pricing is a property of the alias, not of whichever candidate ended up
	// primary vs. fallback, so it is resolved once here, after a route has
	// actually been selected. Deferring it to this point (rather than up
	// front, alongside LoadAliasPolicy) also means an alias that gets
	// refused earlier -- unentitled, disallowed, no eligible routes -- never
	// pays for a pricing lookup it will not use.
	pricing, priceUnit, err := s.repo.LoadAliasPricing(ctx, aliasID)
	if err != nil {
		return SelectionResult{}, fmt.Errorf("routing: load alias pricing for %s: %w", aliasID, err)
	}

	// Fail closed on an alias with no usable price (issue #617). Both prices
	// at zero means either no model_aliases row (LoadAliasPricing returns the
	// zero value for that case) or a row that was never priced. Returning it
	// would hand the caller a route that costs nothing, and the floor-at-1
	// rule in the metering gate would then charge one credit for a request of
	// any size. This is the same both-zero condition metering's
	// RouteInfo.HasCostBasis uses, so an alias that legitimately prices only
	// one side (embeddings are input-only, voice is output-only) stays
	// selectable.
	//
	// A PricingModeUpstreamActual alias has no fixed price BY DESIGN: the
	// charge is derived from the cost the upstream reports for that specific
	// generation. It is still refused unless it carries a positive
	// reservation estimate, because that hold is the only thing standing
	// between a variable-cost request and an account with no credits. An
	// upstream_actual row with no hold size is a misconfiguration, and the
	// fail-closed answer to a misconfigured money path is to refuse it, not
	// to reserve nothing and hope the settlement lands.
	if pricing.IsUpstreamActual() {
		if pricing.ReservationEstimateCredits == nil || *pricing.ReservationEstimateCredits <= 0 {
			return SelectionResult{}, fmt.Errorf("%w: %s (upstream_actual with no reservation estimate)", ErrAliasNotPriced, aliasID)
		}
	} else if !pricing.HasFixedPrice() {
		return SelectionResult{}, fmt.Errorf("%w: %s", ErrAliasNotPriced, aliasID)
	}

	return SelectionResult{
		AliasID:          aliasID,
		RouteID:          selected.RouteID,
		LiteLLMModelName: selected.LiteLLMModelName,
		Provider:         selected.Provider,
		FallbackRouteIDs: fallbacks,
		Pricing:          pricing,
		PriceUnit:        priceUnit,

		ReasoningReserveTokens: reserve,
	}, nil
}

func matchesRequestedCapabilities(candidate RouteCandidate, input SelectionInput) bool {
	if input.NeedResponses && !candidate.SupportsResponses {
		return false
	}
	if input.NeedChatCompletions && !candidate.SupportsChatCompletions {
		return false
	}
	if input.NeedEmbeddings && !candidate.SupportsEmbeddings {
		return false
	}
	if input.NeedStreaming && !candidate.SupportsStreaming {
		return false
	}
	if input.NeedReasoning && !candidate.SupportsReasoning {
		return false
	}
	if input.NeedCacheRead && !candidate.SupportsCacheRead {
		return false
	}
	if input.NeedCacheWrite && !candidate.SupportsCacheWrite {
		return false
	}
	if input.NeedImageGeneration && !candidate.SupportsImageGeneration {
		return false
	}
	if input.NeedImageEdit && !candidate.SupportsImageEdit {
		return false
	}
	if input.NeedTTS && !candidate.SupportsTTS {
		return false
	}
	if input.NeedSTT && !candidate.SupportsSTT {
		return false
	}
	if input.NeedBatch && !candidate.SupportsBatch {
		return false
	}

	return true
}

// NOTE: RequireToolCapable pre-filtering happens before matchesRequestedCapabilities
// is called (see SelectRoute). The SupportsTools field is checked there, not here,
// so that we can return the specific ErrNoCapableRoute sentinel.

// aliasAllowed applies the API-key policy allowlist. An empty list keeps meaning
// "this key is not narrowed to specific aliases", which is the documented
// api_key_policies semantic and is what an unrestricted key stores. That default
// is deliberately NOT reused for tenant entitlement: entitlement keys off the
// presence of a tenant identity and fails closed (see SelectRoute), so an
// unset allowlist can never stand in for an entitlement grant.
func aliasAllowed(aliasID string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}

	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), aliasID) {
			return true
		}
	}

	return false
}

func providerAllowed(provider string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}

	for _, item := range allowed {
		if strings.EqualFold(strings.TrimSpace(item), provider) {
			return true
		}
	}

	return false
}

func orderCandidates(policy catalog.AliasPolicySnapshot, candidates []RouteCandidate) []RouteCandidate {
	ordered := append([]RouteCandidate(nil), candidates...)

	orderIndex := make(map[string]int, len(policy.FallbackOrder))
	for idx, routeID := range policy.FallbackOrder {
		orderIndex[routeID] = idx
	}

	sort.SliceStable(ordered, func(i, j int) bool {
		left := ordered[i]
		right := ordered[j]

		leftRank, leftListed := orderIndex[left.RouteID]
		rightRank, rightListed := orderIndex[right.RouteID]
		if leftListed && rightListed && leftRank != rightRank {
			return leftRank < rightRank
		}
		if leftListed != rightListed {
			return leftListed
		}

		switch policy.PolicyMode {
		case "cost":
			if leftClass, rightClass := priceClassRank(left.PriceClass), priceClassRank(right.PriceClass); leftClass != rightClass {
				return leftClass < rightClass
			}
		case "latency", "stability", "weighted", "pinned":
			// Priority remains the tie-breaker for these seeded policy profiles.
		}

		if left.Priority != right.Priority {
			return left.Priority < right.Priority
		}

		return strings.Compare(left.RouteID, right.RouteID) < 0
	})

	return ordered
}

func priceClassRank(priceClass string) int {
	classes := []string{"budget", "standard", "premium"}
	idx := slices.Index(classes, strings.ToLower(strings.TrimSpace(priceClass)))
	if idx == -1 {
		return len(classes)
	}

	return idx
}
