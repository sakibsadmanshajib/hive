package routing

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

func TestSelectRouteHandlerReturnsRouteAndFallbacks(t *testing.T) {
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
		},
	}

	handler := NewHandler(NewService(repo, &stubEntitlements{visible: true}))
	body := bytes.NewBufferString(`{
		"alias_id":"hive-fast",
		"need_responses":true,
		"need_chat_completions":true,
		"allowed_aliases":["hive-fast"],
		"allowed_providers":["groq","openrouter"]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/internal/routing/select", body)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var result SelectionResult
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if result.AliasID != "hive-fast" {
		t.Fatalf("expected alias hive-fast, got %q", result.AliasID)
	}
	if result.RouteID != "route-groq-fast" {
		t.Fatalf("expected route-groq-fast, got %q", result.RouteID)
	}
	if len(result.FallbackRouteIDs) != 1 || result.FallbackRouteIDs[0] != "route-openrouter-fast-fallback" {
		t.Fatalf("expected fallback route-openrouter-fast-fallback, got %v", result.FallbackRouteIDs)
	}
}

func TestSelectRouteHandlerRejectsMissingAliasID(t *testing.T) {
	handler := NewHandler(NewService(&stubRepository{}, &stubEntitlements{visible: true}))
	req := httptest.NewRequest(http.MethodPost, "/internal/routing/select", bytes.NewBufferString(`{"need_responses":true}`))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSelectRouteHandlerRejectsIneligibleRoute(t *testing.T) {
	repo := &stubRepository{
		policy: catalog.AliasPolicySnapshot{
			AliasID:                 "hive-fast",
			PolicyMode:              "latency",
			AllowPriceClassWidening: false,
			FallbackOrder:           []string{"route-groq-fast"},
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

	handler := NewHandler(NewService(repo, &stubEntitlements{visible: true}))
	body := bytes.NewBufferString(`{
		"alias_id":"hive-fast",
		"need_responses":true,
		"allowed_aliases":["hive-fast"],
		"allowed_providers":["openrouter"]
	}`)

	req := httptest.NewRequest(http.MethodPost, "/internal/routing/select", body)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSelectRouteHandlerRefusesUnentitledTenantWith403 pins the status for the
// new refusal. A 5xx would misreport an admin policy decision as an outage, and
// a 404 would be indistinguishable from an unknown model.
func TestSelectRouteHandlerRefusesUnentitledTenantWith403(t *testing.T) {
	repo := entitlementTestRepo()
	handler := NewHandler(NewService(repo, &stubEntitlements{visible: false}))
	body := bytes.NewBufferString(`{
		"alias_id":"hive-fast",
		"tenant_id":"3f6c1d9e-2b7a-4c53-9f21-8a4d6e0b7c11",
		"need_chat_completions":true
	}`)

	req := httptest.NewRequest(http.MethodPost, "/internal/routing/select", body)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an unentitled tenant, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestSelectRouteHandlerForwardsTenantToEntitlementCheck asserts the wire
// tenant_id reaches the entitlement check rather than being decoded and dropped.
func TestSelectRouteHandlerForwardsTenantToEntitlementCheck(t *testing.T) {
	repo := entitlementTestRepo()
	entitlements := &stubEntitlements{visible: true}
	handler := NewHandler(NewService(repo, entitlements))
	body := bytes.NewBufferString(`{
		"alias_id":"hive-fast",
		"tenant_id":"3f6c1d9e-2b7a-4c53-9f21-8a4d6e0b7c11",
		"need_chat_completions":true
	}`)

	req := httptest.NewRequest(http.MethodPost, "/internal/routing/select", body)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if entitlements.calls != 1 {
		t.Fatalf("expected the decoded tenant to drive one entitlement lookup, got %d", entitlements.calls)
	}
}

func TestSelectRouteHandlerRejectsMalformedTenantID(t *testing.T) {
	repo := entitlementTestRepo()
	handler := NewHandler(NewService(repo, &stubEntitlements{visible: true}))
	body := bytes.NewBufferString(`{"alias_id":"hive-fast","tenant_id":"not-a-uuid"}`)

	req := httptest.NewRequest(http.MethodPost, "/internal/routing/select", body)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed tenant_id, got %d: %s", rr.Code, rr.Body.String())
	}
}
