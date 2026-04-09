package routing

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/hivegpt/hive/apps/control-plane/internal/catalog"
)

var (
	ErrAliasNotFound     = errors.New("routing: alias not found")
	ErrRouteNotEligible  = errors.New("routing: no eligible routes")
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
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

	if !aliasAllowed(aliasID, input.AllowedAliases) {
		return SelectionResult{}, fmt.Errorf("%w: alias %s is not allowlisted", ErrRouteNotEligible, aliasID)
	}

	candidates, err := s.repo.ListRouteCandidates(ctx, aliasID)
	if err != nil {
		return SelectionResult{}, err
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

	fallbacks := make([]string, 0, len(ordered)-1)
	for _, candidate := range ordered[1:] {
		if !policy.AllowPriceClassWidening && candidate.PriceClass != selected.PriceClass {
			continue
		}
		fallbacks = append(fallbacks, candidate.RouteID)
	}

	return SelectionResult{
		AliasID:          aliasID,
		RouteID:          selected.RouteID,
		LiteLLMModelName: selected.LiteLLMModelName,
		Provider:         selected.Provider,
		FallbackRouteIDs: fallbacks,
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
