package routing

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
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
			AliasID:                 "hive-fast",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
			FallbackOrder:           []string{"route-groq-fast", "route-openrouter-fast-fallback"},
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
		AliasID:             "hive-fast",
		NeedResponses:       true,
		NeedChatCompletions: true,
		NeedStreaming:       true,
		AllowedProviders:    []string{"groq", "openrouter"},
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
		AliasID:        "hive-fast",
		AllowedAliases: []string{"hive-default"},
		NeedResponses:  true,
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

func TestSelectRouteSucceedsForSeededMediaAndBatchCapabilities(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-auto",
			PolicyMode:              "weighted",
			AllowPriceClassWidening: true,
			FallbackOrder:           []string{"route-openrouter-auto"},
		},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-openrouter-auto",
				AliasID:                 "hive-auto",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-openrouter-auto",
				HealthState:             "healthy",
				Priority:                10,
				SupportsImageGeneration: true,
				SupportsImageEdit:       true,
				SupportsTTS:             true,
				SupportsSTT:             true,
				SupportsBatch:           true,
			},
		},
	}

	svc := NewService(repo)

	tests := []struct {
		name  string
		input SelectionInput
	}{
		{
			name: "image generation",
			input: SelectionInput{
				AliasID:             "hive-auto",
				NeedImageGeneration: true,
			},
		},
		{
			name: "tts",
			input: SelectionInput{
				AliasID: "hive-auto",
				NeedTTS: true,
			},
		},
		{
			name: "stt",
			input: SelectionInput{
				AliasID: "hive-auto",
				NeedSTT: true,
			},
		},
		{
			name: "batch",
			input: SelectionInput{
				AliasID:   "hive-auto",
				NeedBatch: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := svc.SelectRoute(context.Background(), tt.input)
			if err != nil {
				t.Fatalf("SelectRoute returned error: %v", err)
			}
			if result.RouteID != "route-openrouter-auto" {
				t.Fatalf("expected route-openrouter-auto, got %q", result.RouteID)
			}
		})
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

// TestRequireToolCapable_CapableRouteSelected verifies that when RequireToolCapable=true
// and at least one route has SupportsTools=true, that route is selected.
func TestRequireToolCapable_CapableRouteSelected(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-tools",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
			FallbackOrder:           []string{"route-capable", "route-incapable"},
		},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-capable",
				AliasID:                 "hive-tools",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-capable",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsChatCompletions: true,
				SupportsTools:           true,
			},
			{
				RouteID:                 "route-incapable",
				AliasID:                 "hive-tools",
				Provider:                "groq",
				LiteLLMModelName:        "route-incapable",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                20,
				SupportsChatCompletions: true,
				SupportsTools:           false,
			},
		},
	}

	svc := NewService(repo)

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-tools",
		NeedChatCompletions: true,
		RequireToolCapable:  true,
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}
	if result.RouteID != "route-capable" {
		t.Fatalf("expected route-capable, got %q", result.RouteID)
	}
}

// TestRequireToolCapable_NoCapableRoute verifies that ErrNoCapableRoute is returned
// when RequireToolCapable=true and all routes have SupportsTools=false.
func TestRequireToolCapable_NoCapableRoute(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-basic",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
		},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-no-tools",
				AliasID:                 "hive-basic",
				Provider:                "groq",
				LiteLLMModelName:        "route-no-tools",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsChatCompletions: true,
				SupportsTools:           false,
			},
		},
	}

	svc := NewService(repo)

	_, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-basic",
		NeedChatCompletions: true,
		RequireToolCapable:  true,
	})
	if err == nil {
		t.Fatal("expected ErrNoCapableRoute")
	}
	if !errors.Is(err, ErrNoCapableRoute) {
		t.Fatalf("expected ErrNoCapableRoute, got %v", err)
	}
}

// TestRequireToolCapable_FalseAllowsMixedRoutes verifies that with RequireToolCapable=false
// (default), both capable and incapable routes are considered (existing behaviour).
func TestRequireToolCapable_FalseAllowsMixedRoutes(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-mixed",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
			FallbackOrder:           []string{"route-incapable"},
		},
		candidates: []RouteCandidate{
			{
				RouteID:                 "route-incapable",
				AliasID:                 "hive-mixed",
				Provider:                "groq",
				LiteLLMModelName:        "route-incapable",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsChatCompletions: true,
				SupportsTools:           false,
			},
		},
	}

	svc := NewService(repo)

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-mixed",
		NeedChatCompletions: true,
		RequireToolCapable:  false,
	})
	if err != nil {
		t.Fatalf("expected success with RequireToolCapable=false, got %v", err)
	}
	if result.RouteID != "route-incapable" {
		t.Fatalf("expected route-incapable, got %q", result.RouteID)
	}
}

// TestRequireToolCapable_MixedProviderOnlyCapableSelected verifies that per-route
// filtering applies: a provider with one capable and one incapable route only passes
// the capable route when RequireToolCapable=true.
func TestRequireToolCapable_MixedProviderOnlyCapableSelected(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-tools",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
			FallbackOrder:           []string{"route-vision-only", "route-tools"},
		},
		candidates: []RouteCandidate{
			{
				// Same provider, non-capable route (e.g. vision-only model).
				RouteID:                 "route-vision-only",
				AliasID:                 "hive-tools",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-vision-only",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                5,
				SupportsChatCompletions: true,
				SupportsTools:           false,
			},
			{
				// Same provider, capable route.
				RouteID:                 "route-tools",
				AliasID:                 "hive-tools",
				Provider:                "openrouter",
				LiteLLMModelName:        "route-tools",
				PriceClass:              "standard",
				HealthState:             "healthy",
				Priority:                10,
				SupportsChatCompletions: true,
				SupportsTools:           true,
			},
		},
	}

	svc := NewService(repo)

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-tools",
		NeedChatCompletions: true,
		RequireToolCapable:  true,
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}
	// Must select the capable route, not the vision-only route, even though both
	// share the same provider slug.
	if result.RouteID != "route-tools" {
		t.Fatalf("expected route-tools (capable), got %q", result.RouteID)
	}
}
