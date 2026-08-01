package routing

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

type stubRepository struct {
	policy          catalog.AliasPolicySnapshot
	policyErr       error
	candidates      []RouteCandidate
	candidatesErr   error
	listCalls       int
	loadPolicyCalls int

	pricing            catalog.CatalogPricing
	pricingByRoute     map[string]catalog.CatalogPricing
	pricingErr         error
	pricingCalls       int
	sawPricingRouteIDs []string
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

func (s *stubRepository) LoadRoutePricing(_ context.Context, routeID string) (catalog.CatalogPricing, error) {
	s.pricingCalls++
	s.sawPricingRouteIDs = append(s.sawPricingRouteIDs, routeID)
	if s.pricingErr != nil {
		return catalog.CatalogPricing{}, s.pricingErr
	}
	if p, ok := s.pricingByRoute[routeID]; ok {
		return p, nil
	}

	return s.pricing, nil
}

// stubEntitlements stands in for catalog.Service, the production
// TenantEntitlements implementation.
type stubEntitlements struct {
	visible bool
	err     error
	calls   int
	sawIDs  []string
}

func (s *stubEntitlements) IsAliasVisibleToTenant(_ context.Context, _ uuid.UUID, aliasID string) (bool, error) {
	s.calls++
	s.sawIDs = append(s.sawIDs, aliasID)
	if s.err != nil {
		return false, s.err
	}
	return s.visible, nil
}

func entitlementTestRepo() *stubRepository {
	return &stubRepository{
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
				SupportsChatCompletions: true,
				SupportsStreaming:       true,
			},
		},
	}
}

// TestSelectRouteAllowsAliasVisibleToTenant is the entitled happy path: a
// tenant-scoped request for a model the tenant may see resolves normally.
func TestSelectRouteAllowsAliasVisibleToTenant(t *testing.T) {
	repo := entitlementTestRepo()
	entitlements := &stubEntitlements{visible: true}
	svc := NewService(repo, entitlements)
	tenantID := uuid.New()

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		TenantID:            tenantID,
		NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error for entitled alias: %v", err)
	}
	if result.RouteID != "route-groq-fast" {
		t.Fatalf("expected route-groq-fast, got %q", result.RouteID)
	}
	if entitlements.calls != 1 {
		t.Fatalf("expected exactly one entitlement lookup, got %d", entitlements.calls)
	}
	if len(entitlements.sawIDs) != 1 || entitlements.sawIDs[0] != "hive-fast" {
		t.Fatalf("expected entitlement lookup for hive-fast, got %v", entitlements.sawIDs)
	}
}

// TestSelectRouteDeniesAliasHiddenFromTenant is the defect this change fixes:
// an alias the admin hid from the tenant must not resolve to a route.
func TestSelectRouteDeniesAliasHiddenFromTenant(t *testing.T) {
	repo := entitlementTestRepo()
	svc := NewService(repo, &stubEntitlements{visible: false})

	_, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		TenantID:            uuid.New(),
		NeedChatCompletions: true,
	})
	if err == nil {
		t.Fatal("expected an unentitled alias to be refused")
	}
	if !errors.Is(err, ErrModelNotEntitled) {
		t.Fatalf("expected ErrModelNotEntitled, got %v", err)
	}
	if repo.listCalls != 0 {
		t.Fatalf("expected candidate list to be skipped for an unentitled alias, got %d calls", repo.listCalls)
	}
}

// TestSelectRouteFailsClosedWhenEntitlementsUnwired asserts a tenant-scoped
// request is refused, not silently allowed, when no entitlement source is
// configured. An implicit allow-all default is how the original gap shipped.
func TestSelectRouteFailsClosedWhenEntitlementsUnwired(t *testing.T) {
	repo := entitlementTestRepo()
	svc := NewService(repo, nil)

	_, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		TenantID:            uuid.New(),
		NeedChatCompletions: true,
	})
	if !errors.Is(err, ErrModelNotEntitled) {
		t.Fatalf("expected ErrModelNotEntitled when entitlements are unwired, got %v", err)
	}
}

// TestSelectRouteSurfacesEntitlementLookupFailure asserts a lookup error is not
// mistaken for a verdict: it must propagate rather than admit the request.
func TestSelectRouteSurfacesEntitlementLookupFailure(t *testing.T) {
	repo := entitlementTestRepo()
	svc := NewService(repo, &stubEntitlements{err: errors.New("db down")})

	_, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		TenantID:            uuid.New(),
		NeedChatCompletions: true,
	})
	if err == nil {
		t.Fatal("expected entitlement lookup failure to be surfaced")
	}
	if repo.listCalls != 0 {
		t.Fatalf("expected no candidate listing after a failed entitlement lookup, got %d", repo.listCalls)
	}
}

// TestSelectRouteSkipsEntitlementLookupWithoutTenant covers the API-key path:
// api_keys hang off accounts, which are not tenant-scoped, so those requests
// carry no tenant and stay governed by the key policy allowlist.
func TestSelectRouteSkipsEntitlementLookupWithoutTenant(t *testing.T) {
	repo := entitlementTestRepo()
	entitlements := &stubEntitlements{visible: false}
	svc := NewService(repo, entitlements)

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("untenanted selection must not consult tenant entitlement: %v", err)
	}
	if result.RouteID != "route-groq-fast" {
		t.Fatalf("expected route-groq-fast, got %q", result.RouteID)
	}
	if entitlements.calls != 0 {
		t.Fatalf("expected no entitlement lookup without a tenant, got %d", entitlements.calls)
	}
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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

	svc := NewService(repo, &stubEntitlements{visible: true})

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

// TestSelectRouteCarriesRoutePricing is D-032's core assertion: SelectRoute
// must surface the SELECTED ROUTE's per-route credit price (from
// public.provider_routes, not public.model_aliases) on the result, keyed by
// route_id, so a caller (edge-api's metering gate) can price a request
// without a second round trip.
func TestSelectRouteCarriesRoutePricing(t *testing.T) {
	repo := entitlementTestRepo()
	cacheRead := int64(1)
	cacheWrite := int64(5)
	repo.pricing = catalog.CatalogPricing{
		InputPriceCredits:      12,
		OutputPriceCredits:     36,
		CacheReadPriceCredits:  &cacheRead,
		CacheWritePriceCredits: &cacheWrite,
	}

	svc := NewService(repo, &stubEntitlements{visible: true})

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		TenantID:            uuid.New(),
		NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}
	if result.Pricing.InputPriceCredits != 12 {
		t.Fatalf("expected input price 12, got %d", result.Pricing.InputPriceCredits)
	}
	if result.Pricing.OutputPriceCredits != 36 {
		t.Fatalf("expected output price 36, got %d", result.Pricing.OutputPriceCredits)
	}
	if result.Pricing.CacheReadPriceCredits == nil || *result.Pricing.CacheReadPriceCredits != 1 {
		t.Fatalf("expected cache read price 1, got %v", result.Pricing.CacheReadPriceCredits)
	}
	if result.Pricing.CacheWritePriceCredits == nil || *result.Pricing.CacheWritePriceCredits != 5 {
		t.Fatalf("expected cache write price 5, got %v", result.Pricing.CacheWritePriceCredits)
	}
	if repo.pricingCalls != 1 {
		t.Fatalf("expected exactly one pricing lookup, got %d", repo.pricingCalls)
	}
	if len(repo.sawPricingRouteIDs) != 1 || repo.sawPricingRouteIDs[0] != "route-groq-fast" {
		t.Fatalf("expected pricing lookup keyed by route_id route-groq-fast, got %v", repo.sawPricingRouteIDs)
	}
}

// TestSelectRouteFailsClosedForRouteWithNoPrice is D-032's fail-closed
// requirement: a route with no price row is not selectable. Under the OLD
// alias-keyed pricing this case (a missing model_aliases row) was swallowed
// into a successful zero-value price, silently billing zero. provider_routes
// price columns are NOT NULL for every route seeded today, so this is only
// reachable via a repository/query failure (a future route inserted without
// a price, a bad route_id) -- and that failure must refuse the selection,
// the same posture as the tenant entitlement check above, not fall through
// to a free route.
func TestSelectRouteFailsClosedForRouteWithNoPrice(t *testing.T) {
	repo := entitlementTestRepo()
	repo.pricingErr = errors.New("no price row for route")

	svc := NewService(repo, &stubEntitlements{visible: true})

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		NeedChatCompletions: true,
	})
	if err == nil {
		t.Fatalf("expected a route with no price to refuse selection, got result %+v", result)
	}
	if !reflect.DeepEqual(result, SelectionResult{}) {
		t.Fatalf("expected zero-value SelectionResult on failure, got %+v", result)
	}
}

// TestSelectRoutePriceIsRouteStableAcrossFallback is D-032's motivating case:
// hive-fast routes to both OpenRouter llama-3.1-8b ($0.05/$0.08 per M) and
// Groq llama-3.3-70b ($0.59/$0.79 per M) under one alias. Pricing must now
// be a property of whichever ROUTE was actually selected, not a single
// alias-wide number: whichever route becomes primary must bill its OWN
// price, and swapping priority (so the other route becomes primary) must
// change which price comes back. This replaces the old
// TestSelectRoutePriceIsAliasStableAcrossFallback, which asserted the
// opposite (one shared alias price regardless of route) -- that was the bug
// D-032 fixes, not a behavior to preserve.
func TestSelectRoutePriceIsRouteStableAcrossFallback(t *testing.T) {
	candidates := []RouteCandidate{
		{
			RouteID:                 "route-groq-fast",
			AliasID:                 "hive-fast",
			Provider:                "groq",
			LiteLLMModelName:        "route-groq-fast",
			PriceClass:              "standard",
			HealthState:             "healthy",
			Priority:                10,
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
			SupportsChatCompletions: true,
		},
	}
	pricingByRoute := map[string]catalog.CatalogPricing{
		"route-groq-fast":               {InputPriceCredits: 82600, OutputPriceCredits: 110600},
		"route-openrouter-fast-fallback": {InputPriceCredits: 7000, OutputPriceCredits: 11200},
	}

	// Groq primary (lower priority number wins): result must carry Groq's
	// own price, not OpenRouter's and not a blended/shared number.
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-fast",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
			FallbackOrder:           []string{"route-groq-fast", "route-openrouter-fast-fallback"},
		},
		candidates:     candidates,
		pricingByRoute: pricingByRoute,
	}
	svc := NewService(repo, &stubEntitlements{visible: true})

	result, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}
	if result.RouteID != "route-groq-fast" {
		t.Fatalf("expected primary route-groq-fast, got %q", result.RouteID)
	}
	if result.Pricing.InputPriceCredits != 82600 || result.Pricing.OutputPriceCredits != 110600 {
		t.Fatalf("expected Groq's own price 82600/110600, got %+v", result.Pricing)
	}

	// Reverse the policy's fallback order so OpenRouter becomes primary: the
	// returned price must change to OpenRouter's, proving it is route-keyed,
	// not cached once per alias.
	repo2 := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-fast",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
			FallbackOrder:           []string{"route-openrouter-fast-fallback", "route-groq-fast"},
		},
		candidates:     candidates,
		pricingByRoute: pricingByRoute,
	}
	svc2 := NewService(repo2, &stubEntitlements{visible: true})

	result2, err := svc2.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		NeedChatCompletions: true,
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error on swapped priority: %v", err)
	}
	if result2.RouteID != "route-openrouter-fast-fallback" {
		t.Fatalf("expected primary route-openrouter-fast-fallback, got %q", result2.RouteID)
	}
	if result2.Pricing.InputPriceCredits != 7000 || result2.Pricing.OutputPriceCredits != 11200 {
		t.Fatalf("expected OpenRouter's own price 7000/11200 after priority swap, got %+v", result2.Pricing)
	}
}

// TestSelectRoutePropagatesPricingLookupFailure matches the repo's existing
// convention (TestSelectRoutePropagatesAliasLookupErrors): a genuine
// infrastructure failure resolving a route's price (DB unreachable, etc.)
// surfaces to the caller rather than being silently swallowed into a wrong
// zero price. Complements TestSelectRouteFailsClosedForRouteWithNoPrice,
// which covers the missing-price-row case specifically.
func TestSelectRoutePropagatesPricingLookupFailure(t *testing.T) {
	repo := entitlementTestRepo()
	repo.pricingErr = errors.New("pricing db down")

	svc := NewService(repo, &stubEntitlements{visible: true})

	_, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		NeedChatCompletions: true,
	})
	if err == nil {
		t.Fatal("expected pricing lookup failure to be surfaced")
	}
}

// TestSelectRouteSkipsPricingLookupOnEarlyRefusal asserts the pricing
// lookup is not a wasted query on a path that was always going to fail: an
// unentitled alias must be refused before any pricing read, exactly as it
// already skips ListRouteCandidates (TestSelectRouteDeniesAliasHiddenFromTenant).
func TestSelectRouteSkipsPricingLookupOnEarlyRefusal(t *testing.T) {
	repo := entitlementTestRepo()
	svc := NewService(repo, &stubEntitlements{visible: false})

	_, err := svc.SelectRoute(context.Background(), SelectionInput{
		AliasID:             "hive-fast",
		TenantID:            uuid.New(),
		NeedChatCompletions: true,
	})
	if err == nil {
		t.Fatal("expected an unentitled alias to be refused")
	}
	if repo.pricingCalls != 0 {
		t.Fatalf("expected pricing lookup to be skipped for an unentitled alias, got %d calls", repo.pricingCalls)
	}
}
