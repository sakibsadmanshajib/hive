package routing

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hivegpt/hive/apps/control-plane/internal/catalog"
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

	handler := NewHandler(NewService(repo))
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
	handler := NewHandler(NewService(&stubRepository{}))
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

	handler := NewHandler(NewService(repo))
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
