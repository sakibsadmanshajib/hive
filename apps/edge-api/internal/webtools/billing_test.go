package webtools

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling/billingtest"
)

// The money path for the two web tools (issue #1695).
//
// Every assertion here is about the LEDGER, never about a constant existing. A
// test that asserts a price constant is present proves the constant is present
// and nothing about whether a call moves credits, which is the shape that let
// three days of unmetered traffic pass green in July.

// searchPricePerCall and fetchPricePerCall are what the catalog rows seeded by
// supabase/migrations/20260902_01_web_tool_call_pricing.sql charge for one
// call, at the D-046 peg of 1 USD = 1e9 credits: 0.0001 USD for a search and
// 0.0002 USD for a fetch.
const (
	searchPricePerCall = int64(100_000)
	fetchPricePerCall  = int64(200_000)
)

// stubPricer answers the catalog price lookup without a control-plane.
type stubPricer struct {
	prices map[string]AliasPrice
	err    error
	calls  int
}

func (p *stubPricer) AliasPrice(_ context.Context, aliasID string) (AliasPrice, error) {
	p.calls++
	if p.err != nil {
		return AliasPrice{}, p.err
	}
	price, ok := p.prices[aliasID]
	if !ok {
		return AliasPrice{}, errors.New("stubPricer: no such alias")
	}
	return price, nil
}

// catalogPricer answers exactly what the seeded rows say, in the column the
// migration writes: credits per MILLION calls, in output_price_credits.
func catalogPricer() *stubPricer {
	return &stubPricer{prices: map[string]AliasPrice{
		AliasWebSearch: {PriceUnit: PriceUnitCalls, CreditsPerMillion: searchPricePerCall * 1_000_000, PricingMode: "fixed"},
		AliasWebFetch:  {PriceUnit: PriceUnitCalls, CreditsPerMillion: fetchPricePerCall * 1_000_000, PricingMode: "fixed"},
	}}
}

// okFetcher is a pipeline that always returns a conforming envelope.
func okFetcher() Fetcher {
	return FetcherFunc(func(_ context.Context, target, _ string) (FetchResult, error) {
		return NewFetchResult(FetchMeta{URL: target}, []Part{{Text: "body", End: 4}})
	})
}

// billableDeps returns Deps carrying a working money path, so a test only has
// to state the part it is about.
func billableDeps(t *testing.T, acct *billingtest.Accounting, pricer Pricer) Deps {
	t.Helper()
	return Deps{
		Billing: &Billing{
			Accounting: acct.Client(t),
			Resolver:   billingtest.Billable(),
			Pricer:     pricer,
		},
	}
}

func TestWebSearchChargesTheCatalogPricePerCall(t *testing.T) {
	acct := &billingtest.Accounting{}
	deps := billableDeps(t, acct, catalogPricer())
	deps.Search = &stubSearcher{hits: okHits()}
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}

	holds := acct.Reservations()
	if len(holds) != 1 {
		t.Fatalf("holds taken = %d, want 1", len(holds))
	}
	if holds[0].EstimatedCredits != searchPricePerCall {
		t.Errorf("hold = %d credits, want %d", holds[0].EstimatedCredits, searchPricePerCall)
	}
	if holds[0].ModelAlias != AliasWebSearch {
		t.Errorf("hold alias = %q, want %q", holds[0].ModelAlias, AliasWebSearch)
	}

	charges := acct.Finalized()
	if len(charges) != 1 {
		t.Fatalf("charges settled = %d, want 1", len(charges))
	}
	if charges[0].ActualCredits != searchPricePerCall {
		t.Errorf("charge = %d credits, want %d", charges[0].ActualCredits, searchPricePerCall)
	}
	if charges[0].InputTokens != 0 || charges[0].OutputTokens != 0 {
		t.Errorf("charge reported tokens %d/%d, want a zero-token per-call charge",
			charges[0].InputTokens, charges[0].OutputTokens)
	}
	if _, released := acct.Counts(); released != 0 {
		t.Errorf("released %d holds on a delivered call, want 0", released)
	}
}

func TestWebFetchChargesTwiceTheSearchPrice(t *testing.T) {
	acct := &billingtest.Accounting{}
	deps := billableDeps(t, acct, catalogPricer())
	deps.Fetch = okFetcher()
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_fetch", "turn-1", map[string]any{"url": "https://example.com/a"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}

	charges := acct.Finalized()
	if len(charges) != 1 {
		t.Fatalf("charges settled = %d, want 1", len(charges))
	}
	if charges[0].ActualCredits != fetchPricePerCall {
		t.Errorf("charge = %d credits, want %d", charges[0].ActualCredits, fetchPricePerCall)
	}
	// The magnitude relationship, not just the absolute figure: a fetch costs
	// twice a search, and a single wrong alias lookup would break that while
	// still charging a plausible-looking number.
	if charges[0].ActualCredits != 2*searchPricePerCall {
		t.Errorf("fetch charged %d, want exactly twice the search price %d",
			charges[0].ActualCredits, 2*searchPricePerCall)
	}
}

func TestWebToolRefusesWhenTheCatalogUnitIsNotCalls(t *testing.T) {
	acct := &billingtest.Accounting{}
	pricer := &stubPricer{prices: map[string]AliasPrice{
		// A tokens-priced row, which is what a careless catalog edit would
		// leave behind. There is no honest conversion from a per-million-token
		// rate to the price of one call, so this must refuse (D-033, D-034).
		AliasWebSearch: {PriceUnit: "tokens", CreditsPerMillion: 12, PricingMode: "fixed"},
	}}
	deps := billableDeps(t, acct, pricer)
	search := &stubSearcher{hits: okHits()}
	deps.Search = search
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rr.Code, rr.Body)
	}
	if env := decodeEnvelope(t, rr); env["code"] != CodeBillingUnavailable {
		t.Errorf("code = %v, want %q", env["code"], CodeBillingUnavailable)
	}
	if search.calls != 0 {
		t.Errorf("backend called %d times on an unpriceable call, want 0", search.calls)
	}
	if holds, _ := acct.Counts(); holds != 0 {
		t.Errorf("took %d holds on an unpriceable call, want 0", holds)
	}
}

func TestWebToolRefusesWhenTheCatalogPriceIsZero(t *testing.T) {
	acct := &billingtest.Accounting{}
	pricer := &stubPricer{prices: map[string]AliasPrice{
		AliasWebSearch: {PriceUnit: PriceUnitCalls, CreditsPerMillion: 0, PricingMode: "fixed"},
	}}
	deps := billableDeps(t, acct, pricer)
	search := &stubSearcher{hits: okHits()}
	deps.Search = search
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rr.Code, rr.Body)
	}
	if search.calls != 0 {
		t.Errorf("backend called %d times on a zero-priced alias, want 0", search.calls)
	}
}

func TestWebToolRefusesWhenThePriceLookupFails(t *testing.T) {
	acct := &billingtest.Accounting{}
	pricer := &stubPricer{err: errors.New("control-plane unreachable")}
	deps := billableDeps(t, acct, pricer)
	search := &stubSearcher{hits: okHits()}
	deps.Search = search
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rr.Code, rr.Body)
	}
	if search.calls != 0 {
		t.Errorf("backend called %d times when the price could not be read, want 0", search.calls)
	}
}

func TestWebToolRefusesWhenBillingIsNotWired(t *testing.T) {
	// Constructed directly rather than through newTestHandler, which supplies a
	// working money path so every other test can state only what it is about.
	// A deployment that forgot to wire billing must refuse, not serve free.
	search := &stubSearcher{hits: okHits()}
	mux := http.NewServeMux()
	NewHandler(Deps{Search: search}).Register(mux)

	rr := post(t, mux, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rr.Code, rr.Body)
	}
	if search.calls != 0 {
		t.Errorf("backend called %d times with no money path wired, want 0", search.calls)
	}
}

func TestWebToolRefusesOnInsufficientCredit(t *testing.T) {
	acct := &billingtest.Accounting{ReservationStatus: http.StatusConflict}
	deps := billableDeps(t, acct, catalogPricer())
	search := &stubSearcher{hits: okHits()}
	deps.Search = search
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402, body = %s", rr.Code, rr.Body)
	}
	if env := decodeEnvelope(t, rr); env["code"] != CodeInsufficientCredit {
		t.Errorf("code = %v, want %q", env["code"], CodeInsufficientCredit)
	}
	if search.calls != 0 {
		t.Errorf("backend called %d times for a tenant that cannot pay, want 0", search.calls)
	}
}

func TestWebSearchDoesNotChargeAFailedCall(t *testing.T) {
	acct := &billingtest.Accounting{}
	deps := billableDeps(t, acct, catalogPricer())
	deps.Search = &stubSearcher{err: errors.New("searxng down")}
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body = %s", rr.Code, rr.Body)
	}
	if charges := acct.Finalized(); len(charges) != 0 {
		t.Errorf("charged %d times for a failed search, want 0", len(charges))
	}
	released := acct.Released()
	if len(released) != 1 {
		t.Fatalf("released %d holds after a failed search, want 1", len(released))
	}
	if released[0].Reason == "" {
		t.Error("release carried no reason")
	}
}

func TestWebFetchDoesNotChargeAFailedCall(t *testing.T) {
	acct := &billingtest.Accounting{}
	deps := billableDeps(t, acct, catalogPricer())
	deps.Fetch = FetcherFunc(func(context.Context, string, string) (FetchResult, error) {
		return FetchResult{}, errors.New("pipeline down")
	})
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_fetch", "turn-1", map[string]any{"url": "https://example.com/a"})
	if rr.Code == http.StatusOK {
		t.Fatalf("status = %d, want a failure, body = %s", rr.Code, rr.Body)
	}
	if charges := acct.Finalized(); len(charges) != 0 {
		t.Errorf("charged %d times for a failed fetch, want 0", len(charges))
	}
	if _, released := acct.Counts(); released != 1 {
		t.Errorf("released %d holds after a failed fetch, want 1", released)
	}
}

// A search that reached SearXNG and came back with nothing still consumed the
// query, which is what SearXNG bills for. It is a delivered call, not a
// failure, so it is charged.
func TestWebSearchChargesAnEmptyResultSet(t *testing.T) {
	acct := &billingtest.Accounting{}
	deps := billableDeps(t, acct, catalogPricer())
	deps.Search = &stubSearcher{hits: nil}
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	charges := acct.Finalized()
	if len(charges) != 1 || charges[0].ActualCredits != searchPricePerCall {
		t.Fatalf("charges = %+v, want one charge of %d credits", charges, searchPricePerCall)
	}
}

// A reservation reaches a terminal state exactly once: charged, or handed
// back, never both and never neither (#616, D-034). When the charge itself
// cannot land the hold must be released rather than stranded.
func TestWebToolHoldIsTerminalExactlyOnceWhenTheChargeFails(t *testing.T) {
	acct := &billingtest.Accounting{FinalizeStatus: http.StatusInternalServerError}
	deps := billableDeps(t, acct, catalogPricer())
	deps.Search = &stubSearcher{hits: okHits()}
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	holds, released := acct.Counts()
	if holds != 1 {
		t.Fatalf("holds = %d, want 1", holds)
	}
	if released != 1 {
		t.Errorf("released = %d after a failed charge, want the hold handed back", released)
	}
}

// The budget refusals are cheaper than the money path and must run first: a
// turn that has spent its allowance takes no hold at all.
func TestExhaustedTurnBudgetTakesNoHold(t *testing.T) {
	acct := &billingtest.Accounting{}
	deps := billableDeps(t, acct, catalogPricer())
	deps.Search = &stubSearcher{hits: okHits()}
	h := newTestHandler(t, deps)

	for i := 0; i < SearchBudgetPerTurn; i++ {
		if rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "q"}); rr.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, body = %s", i, rr.Code, rr.Body)
		}
	}
	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "q"})
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429, body = %s", rr.Code, rr.Body)
	}
	if holds, _ := acct.Counts(); holds != SearchBudgetPerTurn {
		t.Errorf("holds = %d, want %d: the refused call must not have taken one", holds, SearchBudgetPerTurn)
	}
}

// An Enterprise-posture tenant has no prepaid relationship with Hive (D-027),
// so the tool is served with no hold and no charge, by decision rather than by
// omission.
func TestEnterprisePostureServesWebToolsUnbilled(t *testing.T) {
	acct := &billingtest.Accounting{}
	deps := Deps{
		Search: &stubSearcher{hits: okHits()},
		Billing: &Billing{
			Accounting: acct.Client(t),
			Resolver:   billingtest.Enterprise(),
			Pricer:     catalogPricer(),
		},
	}
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	if holds, released := acct.Counts(); holds != 0 || released != 0 {
		t.Errorf("holds = %d, released = %d on an Enterprise tenant, want 0 and 0", holds, released)
	}
}

// A tenant whose billing position cannot be read at all is refused, never
// served: unknown is not the same as known-absent (D-034).
func TestUnreadableBillingPositionRefusesTheCall(t *testing.T) {
	acct := &billingtest.Accounting{}
	search := &stubSearcher{hits: okHits()}
	deps := Deps{
		Search: search,
		Billing: &Billing{
			Accounting: acct.Client(t),
			Resolver:   billingtest.Unreadable(),
			Pricer:     catalogPricer(),
		},
	}
	h := newTestHandler(t, deps)

	rr := post(t, h, "/v1/tools/web_search", "turn-1", map[string]any{"query": "who won"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rr.Code, rr.Body)
	}
	if search.calls != 0 {
		t.Errorf("backend called %d times for an unreadable billing position, want 0", search.calls)
	}
}
