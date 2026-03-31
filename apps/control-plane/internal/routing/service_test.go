package routing

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hivegpt/hive/apps/control-plane/internal/catalog"
)

type stubRepository struct {
	policy          catalog.AliasPolicySnapshot
	policyErr       error
	candidates      []RouteCandidate
	candidatesErr   error
	listCalls       int
	loadPolicyCalls int
}

func (s *stubRepository) LoadAliasPolicy(_ context.Context, _ string) (catalog.AliasPolicySnapshot, error) {
	s.loadPolicyCalls++
	if s.policyErr != nil {
		return catalog.AliasPolicySnapshot{}, s.policyErr
	}

	return s.policy, nil
}

func (s *stubRepository) ListRouteCandidates(_ context.Context, _ string) ([]RouteCandidate, error) {
	s.listCalls++
	if s.candidatesErr != nil {
		return nil, s.candidatesErr
	}

	return append([]RouteCandidate(nil), s.candidates...), nil
}

func TestSelectRouteHonorsCapabilityMatrix(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                  "hive-fast",
			PolicyMode:               "latency",
			AllowPriceClassWidening:  false,
			FallbackOrder:            []string{"route-groq-fast", "route-openrouter-fast-fallback"},
		},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-groq-fast",
				AliasID:                 "hive-fast",
				Provider:                "groq",
				ProviderModel:           "groq/llama-fast",
				LiteLLMModelName:        "route-groq-fast",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
				SupportsStreaming:       true,
			},
			{
				RouteID:                 "route-openrouter-fast-fallback",
				AliasID:                 "hive-fast",
				Provider:                "openrouter",
				ProviderModel:           "openrouter/meta-llama/3.1-8b-instruct",
				LiteLLMModelName:        "route-openrouter-fast-fallback",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                20,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
				SupportsStreaming:       true,
			},
			{
				RouteID:                 "route-openrouter-disabled",
				AliasID:                 "hive-fast",
				Provider:                "openrouter",
				ProviderModel:           "openrouter/meta-llama/3.1-8b-instruct",
				LiteLLMModelName:        "route-openrouter-disabled",
				PriceClass:              "standard",
				HealthState:             "disabled",
				Priority:                1,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
				SupportsStreaming:       true,
			},
			{
				RouteID:                 "route-openrouter-no-stream",
				AliasID:                 "hive-fast",
				Provider:                "openrouter",
				ProviderModel:           "openrouter/meta-llama/3.1-8b-instruct",
				LiteLLMModelName:        "route-openrouter-no-stream",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                5,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
				SupportsStreaming:       false,
			},
		},
	}

	svc := NewService(repo)

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:              "hive-fast",
		NeedResponses:        true,
		NeedChatCompletions:  true,
		NeedStreaming:        true,
		AllowedProviders:     []string{"groq", "openrouter"},
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}

	if result.RouteID != "route-groq-fast" {
		t.Fatalf("expected route-groq-fast, got %q", result.RouteID)
	}
	if result.Provider != "groq" {
		t.Fatalf("expected groq provider, got %q", result.Provider)
	}
	if result.LiteLLMModelName != "route-groq-fast" {
		t.Fatalf("expected route-groq-fast litellm group, got %q", result.LiteLLMModelName)
	}

	wantFallbacks := []string{"route-openrouter-fast-fallback"}
	if !reflect.DeepEqual(result.FallbackRouteIDs, wantFallbacks) {
		t.Fatalf("expected fallback routes %v, got %v", wantFallbacks, result.FallbackRouteIDs)
	}
}

func TestSelectRouteRejectsDisallowedAlias(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-fast",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
		},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-groq-fast",
				AliasID:                 "hive-fast",
				Provider:                "groq",
				LiteLLMModelName:        "route-groq-fast",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
			},
		},
	}

	svc := NewService(repo)

	_, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:         "hive-fast",
		AllowedAliases:  []string{"hive-default"},
		NeedResponses:   true,
	})
	if err == nil {
		t.Fatal("expected alias allowlist rejection")
	}
	if repo.listCalls != 0 {
		t.Fatalf("expected candidate list to be skipped for disallowed alias, got %d calls", repo.listCalls)
	}
}

func TestSelectRouteKeepsSamePriceClassByDefault(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-fast",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
			FallbackOrder: []string{
				"route-groq-fast",
				"route-openrouter-fast-fallback",
				"route-openrouter-fast-premium",
			},
		},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-groq-fast",
				AliasID:                 "hive-fast",
				Provider:                "groq",
				LiteLLMModelName:        "route-groq-fast",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
			},
			{
				RouteID:                 "route-openrouter-fast-fallback",
				AliasID:                 "hive-fast",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-openrouter-fast-fallback",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                20,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
			},
			{
				RouteID:                 "route-openrouter-fast-premium",
				AliasID:                 "hive-fast",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-openrouter-fast-premium",
				PriceClass:              "premium",
				HealthState:             "healthy",
				Priority:                30,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
			},
		},
	}

	svc := NewService(repo)

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		NeedResponses:       true,
		NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}

	wantFallbacks := []string{"route-openrouter-fast-fallback"}
	if !reflect.DeepEqual(result.FallbackRouteIDs, wantFallbacks) {
		t.Fatalf("expected same-price fallback only %v, got %v", wantFallbacks, result.FallbackRouteIDs)
	}
}

func TestSelectRouteAllowsExplicitPriceClassWidening(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-auto",
			PolicyMode:              "weighted",
			AllowPriceClassWidening: true,
			FallbackOrder: []string{
				"route-openrouter-auto",
				"route-openrouter-auto-premium",
			},
		},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-openrouter-auto",
				AliasID:                 "hive-auto",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-openrouter-auto",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
			},
			{
				RouteID:                 "route-openrouter-auto-premium",
				AliasID:                 "hive-auto",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-openrouter-auto-premium",
				PriceClass:              "premium",
				HealthState:             "healthy",
				Priority:                20,
				SupportsResponses:       true,
				SupportsChatCompletions: true,
			},
		},
	}

	svc := NewService(repo)

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-auto",
		NeedResponses:       true,
		NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}

	wantFallbacks := []string{"route-openrouter-auto-premium"}
	if !reflect.DeepEqual(result.FallbackRouteIDs, wantFallbacks) {
		t.Fatalf("expected widened fallback routes %v, got %v", wantFallbacks, result.FallbackRouteIDs)
	}
}

func TestSelectRoutePropagatesAliasLookupErrors(t *testing.T) {
	repo := &stubRepository{
		policyErr: errors.New("alias not found"),
	}

	svc := NewService(repo)

	_, err := svc.SelectRoute(context.Background(), SelectionInput{AliasID: "missing"})
	if err == nil {
		t.Fatal("expected alias lookup error")
	}
	if repo.loadPolicyCalls != 1 {
		t.Fatalf("expected one policy load, got %d", repo.loadPolicyCalls)
	}
}
