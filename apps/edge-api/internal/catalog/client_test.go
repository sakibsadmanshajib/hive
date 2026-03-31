package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchSnapshotDecodesModelsAndCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected GET request, got %s", r.Method)
		}
		if r.URL.Path != "/internal/catalog/snapshot" {
			t.Fatalf("expected snapshot path, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"models":[{"id":"hive-default","object":"model","created":1716935002,"owned_by":"hive"}],
			"catalog":[{"id":"hive-default","display_name":"Hive Default","summary":"Balanced default chat model.","capability_badges":["stable","chat","responses"],"pricing":{"input_price_credits":12,"output_price_credits":36,"cache_read_price_credits":2,"cache_write_price_credits":6},"lifecycle":"stable"}]
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)

	snapshot, err := client.FetchSnapshot(context.Background())
	if err != nil {
		t.Fatalf("FetchSnapshot returned error: %v", err)
	}

	if len(snapshot.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(snapshot.Models))
	}
	if snapshot.Models[0].ID != "hive-default" {
		t.Fatalf("expected hive-default, got %q", snapshot.Models[0].ID)
	}
	if len(snapshot.Catalog) != 1 {
		t.Fatalf("expected 1 catalog entry, got %d", len(snapshot.Catalog))
	}
	if snapshot.Catalog[0].Pricing.OutputPriceCredits != 36 {
		t.Fatalf("expected output price 36, got %d", snapshot.Catalog[0].Pricing.OutputPriceCredits)
	}
}

func TestFetchSnapshotReturnsErrorForNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewClient(server.URL)

	if _, err := client.FetchSnapshot(context.Background()); err == nil {
		t.Fatal("expected non-200 response to return error")
	}
}
