package routing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/catalog"
)

// The alias-price lookup (issue #1695): what an alias costs, with the unit it
// is quoted in, and no route selection.
//
// It exists for a caller that must charge against the catalog but has no route
// to select. The per-call web tools are that caller: their aliases are price
// carriers with no provider_routes row, so SelectRoute cannot answer for them
// and making it able to would mean seeding routing state nothing dispatches to.

func aliasPriceHandler(repo Repository) *Handler {
	return NewHandler(NewService(repo, &stubEntitlements{visible: true}))
}

func getAliasPrice(t *testing.T, h *Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/internal/routing/alias-price"+query, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestAliasPriceReturnsTheCatalogRowWithItsUnit(t *testing.T) {
	// A per-call row as the web tool migration seeds it: the whole price in
	// output_price_credits, input at zero, unit 'calls'.
	repo := &stubRepository{
		pricing:   catalog.FixedPricing(0, 100_000_000_000),
		priceUnit: "calls",
	}
	rr := getAliasPrice(t, aliasPriceHandler(repo), "?alias_id=hive-web-search")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}

	var got struct {
		AliasID   string                 `json:"alias_id"`
		Pricing   catalog.CatalogPricing `json:"pricing"`
		PriceUnit string                 `json:"price_unit"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", rr.Body, err)
	}
	if got.AliasID != "hive-web-search" {
		t.Errorf("alias_id = %q, want hive-web-search", got.AliasID)
	}
	if got.PriceUnit != "calls" {
		t.Errorf("price_unit = %q, want calls", got.PriceUnit)
	}
	if got.Pricing.OutputPriceCredits == nil || *got.Pricing.OutputPriceCredits != 100_000_000_000 {
		t.Fatalf("output price = %v, want 100000000000", got.Pricing.OutputPriceCredits)
	}
	// The price crossing the wire as a pointer is load bearing: a JSON null
	// decoded into a plain int64 is a silent zero, which is indistinguishable
	// from free.
	if got.Pricing.InputPriceCredits == nil || *got.Pricing.InputPriceCredits != 0 {
		t.Errorf("input price = %v, want an explicit 0", got.Pricing.InputPriceCredits)
	}
}

// No route is selected, and no route is required. An alias with zero
// candidates still answers, which is the whole reason this endpoint exists.
func TestAliasPriceDoesNotSelectARoute(t *testing.T) {
	repo := &stubRepository{pricing: catalog.FixedPricing(0, 200_000_000_000), priceUnit: "calls"}
	rr := getAliasPrice(t, aliasPriceHandler(repo), "?alias_id=hive-web-fetch")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	if repo.listCalls != 0 {
		t.Errorf("listed route candidates %d times, want 0", repo.listCalls)
	}
	if repo.loadPolicyCalls != 0 {
		t.Errorf("loaded the alias policy %d times, want 0", repo.loadPolicyCalls)
	}
}

// An alias with no model_aliases row is a 404, not a price of zero. The
// repository reports a missing row as an empty unit beside the zero pricing,
// and answering 200 with that would hand the caller something it could charge.
func TestAliasPriceIsNotFoundForAnAliasWithNoRow(t *testing.T) {
	repo := &stubRepository{unpriced: true}
	rr := getAliasPrice(t, aliasPriceHandler(repo), "?alias_id=nope")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", rr.Code, rr.Body)
	}
}

func TestAliasPriceRequiresAnAliasID(t *testing.T) {
	rr := getAliasPrice(t, aliasPriceHandler(&stubRepository{}), "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body)
	}
}

func TestAliasPriceRejectsAWrite(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/internal/routing/alias-price?alias_id=hive-web-search", nil)
	rr := httptest.NewRecorder()
	aliasPriceHandler(&stubRepository{}).ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a non-GET, body = %s", rr.Code, rr.Body)
	}
}

// A repository failure is an error, never an empty price. Serving a zero here
// is how free traffic happens.
func TestAliasPriceReportsALookupFailure(t *testing.T) {
	repo := &stubRepository{pricingErr: context.DeadlineExceeded}
	rr := getAliasPrice(t, aliasPriceHandler(repo), "?alias_id=hive-web-search")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500, body = %s", rr.Code, rr.Body)
	}
}
