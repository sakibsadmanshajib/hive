package catalog

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSnapshotHandlerReturnsModelsAndCatalog(t *testing.T) {
	repo := &stubRepository{
		aliases: []ModelAlias{
			{
				AliasID:                "hive-default",
				OwnedBy:                "hive",
				DisplayName:            "Hive Default",
				Summary:                "Balanced default chat model.",
				Visibility:             "public",
				Lifecycle:              "stable",
				CapabilityBadges:       []string{"stable", "chat", "responses"},
				InputPriceCredits:      12,
				OutputPriceCredits:     36,
				CacheReadPriceCredits:  int64Ptr(2),
				CacheWritePriceCredits: int64Ptr(6),
				CreatedAt:              time.Unix(1_716_935_002, 0).UTC(),
			},
		},
	}

	handler := NewHandler(NewService(repo))
	req := httptest.NewRequest(http.MethodGet, "/internal/catalog/snapshot", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var snapshot CatalogSnapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}

	if len(snapshot.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(snapshot.Models))
	}
	if len(snapshot.Catalog) != 1 {
		t.Fatalf("expected 1 catalog entry, got %d", len(snapshot.Catalog))
	}
	if snapshot.Models[0].ID != "hive-default" {
		t.Fatalf("expected hive-default in models, got %q", snapshot.Models[0].ID)
	}
	if snapshot.Catalog[0].Pricing.OutputPriceCredits != 36 {
		t.Fatalf("expected output price 36, got %d", snapshot.Catalog[0].Pricing.OutputPriceCredits)
	}
}

func TestSnapshotHandlerReturnsServerErrorOnRepositoryFailure(t *testing.T) {
	repo := &stubRepository{err: errors.New("db unavailable")}
	handler := NewHandler(NewService(repo))
	req := httptest.NewRequest(http.MethodGet, "/internal/catalog/snapshot", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "catalog snapshot unavailable") {
		t.Fatalf("expected catalog error body, got %s", rr.Body.String())
	}
}
