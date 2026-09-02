package embeddings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/embeddings"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling/billingtest"
)

// shimAccountID stands for the account that owns OWUI_SHIM_KEY. It is the
// account issue #1696 says every chat search used to be billed to, so it is
// the account these tests assert never appears on a reservation, a charge or a
// release.
var shimAccountID = uuid.MustParse("5f1d0000-0000-4000-8000-0000000000aa")

// pricedEmbeddingRoute is a fixed-price embedding alias: 2,000,000 credits per
// million input tokens, so 1,500 prompt tokens settle at exactly 3,000 credits
// and a test can assert the magnitude rather than "something was charged".
func pricedEmbeddingRoute() inference.SelectRouteResult {
	return inference.SelectRouteResult{
		AliasID:          "hive-embedding-default",
		Provider:         "test-provider",
		LiteLLMModelName: "route-embedding",
		PriceUnit:        inference.PriceUnitTokens,
		Pricing:          inference.FixedPricing(2_000_000, 0),
	}
}

func upstreamEmbeddings(promptTokens int64) func(context.Context, string, []byte) (*http.Response, error) {
	return func(context.Context, string, []byte) (*http.Response, error) {
		body, _ := json.Marshal(map[string]any{
			"object": "list",
			"model":  "route-embedding",
			"data": []map[string]any{
				{"object": "embedding", "index": 0, "embedding": []float32{0.1, 0.2}},
			},
			"usage": map[string]any{"prompt_tokens": promptTokens, "total_tokens": promptTokens},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	}
}

func searchRequest(t *testing.T, tenantID uuid.UUID) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings",
		strings.NewReader(`{"model":"hive-embedding-default","input":["what is the weather in dhaka"]}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.WithUser(req.Context(), &auth.User{ID: uuid.New(), TenantID: tenantID, Role: "member"})
	return req.WithContext(ctx)
}

func billableTenant(accountID uuid.UUID) billingtest.Resolver {
	return billingtest.Resolver{State: metering.TenantBillingState{
		AccountID: accountID, Found: true, Deployment: metering.DeploymentHiveCloud,
	}}
}

// TestChargeLandsOnTheSearchingUserAndNotOnTheShim is issue #1696 itself. The
// embedding spend a chat web search produces must move the ledger of the user
// who searched, and must leave the shim account untouched. A test that only
// asserted "a charge happened" would have passed on the broken behaviour,
// because the broken behaviour did charge: it charged the wrong account.
func TestChargeLandsOnTheSearchingUserAndNotOnTheShim(t *testing.T) {
	searcherAccount := uuid.MustParse("11110000-0000-4000-8000-000000000001")
	searcherTenant, shimTenant := uuid.New(), uuid.New()
	acct := &billingtest.Accounting{}
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) {
			return pricedEmbeddingRoute(), nil
		},
		Dispatch:   upstreamEmbeddings(1500),
		Accounting: acct.Client(t),
		// Both accounts exist and both are billable, so nothing about this
		// fixture forces the right answer: the charge lands on the searcher
		// only because the handler read the searcher's principal.
		Billing: resolverByTenant{map[uuid.UUID]uuid.UUID{
			searcherTenant: searcherAccount,
			shimTenant:     shimAccountID,
		}},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, searchRequest(t, searcherTenant))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	holds := acct.Reservations()
	if len(holds) != 1 {
		t.Fatalf("expected exactly one hold, got %d", len(holds))
	}
	if holds[0].AccountID != searcherAccount.String() {
		t.Fatalf("hold landed on %s, want the searching user's account %s", holds[0].AccountID, searcherAccount)
	}
	if holds[0].EstimatedCredits != inference.DefaultHoldEmbeddings {
		t.Fatalf("hold size %d, want %d", holds[0].EstimatedCredits, inference.DefaultHoldEmbeddings)
	}
	charges := acct.Finalized()
	if len(charges) != 1 {
		t.Fatalf("expected exactly one charge, got %d", len(charges))
	}
	if charges[0].AccountID != searcherAccount.String() {
		t.Fatalf("charge landed on %s, want the searching user's account %s", charges[0].AccountID, searcherAccount)
	}
	if charges[0].AccountID == shimAccountID.String() {
		t.Fatalf("charge landed on the shim account, which is the defect")
	}
	// 1500 tokens at 2,000,000 credits per million is exactly 3,000 credits.
	// The magnitude is asserted, not the sign of it: a settlement that priced
	// the wrong quantity would still be "a charge".
	if charges[0].ActualCredits != 3000 {
		t.Fatalf("charged %d credits, want 3000 for 1500 prompt tokens at the alias rate", charges[0].ActualCredits)
	}
	if !charges[0].TerminalUsageConfirmed {
		t.Fatalf("a charge derived from a real usage block must be confirmed")
	}
	if charges[0].InputTokens != 1500 || charges[0].OutputTokens != 0 {
		t.Fatalf("usage row tokens %d/%d, want 1500/0", charges[0].InputTokens, charges[0].OutputTokens)
	}
	if _, released := acct.Counts(); released != 0 {
		t.Fatalf("a settled charge must not also release the hold, got %d releases", released)
	}
	for _, r := range acct.Released() {
		if r.AccountID == shimAccountID.String() {
			t.Fatalf("the shim account must not appear on any accounting call")
		}
	}
}

// TestTwoTenantsAreChargedSeparately holds the first bound in the brief: no
// customer may be charged for another customer's search. One handler, two
// searches, two accounts, and neither figure lands on the other.
func TestTwoTenantsAreChargedSeparately(t *testing.T) {
	accountA := uuid.MustParse("11110000-0000-4000-8000-00000000000a")
	accountB := uuid.MustParse("11110000-0000-4000-8000-00000000000b")
	tenantA, tenantB := uuid.New(), uuid.New()

	acct := &billingtest.Accounting{}
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) {
			return pricedEmbeddingRoute(), nil
		},
		Dispatch:   upstreamEmbeddings(1000),
		Accounting: acct.Client(t),
		Billing: resolverByTenant{map[uuid.UUID]uuid.UUID{
			tenantA: accountA,
			tenantB: accountB,
		}},
	})

	for _, tenant := range []uuid.UUID{tenantA, tenantB} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, searchRequest(t, tenant))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
	}

	charges := acct.Finalized()
	if len(charges) != 2 {
		t.Fatalf("expected two charges, got %d", len(charges))
	}
	if charges[0].AccountID != accountA.String() || charges[1].AccountID != accountB.String() {
		t.Fatalf("charges landed on %s and %s, want %s and %s",
			charges[0].AccountID, charges[1].AccountID, accountA, accountB)
	}
}

type resolverByTenant struct {
	accounts map[uuid.UUID]uuid.UUID
}

func (r resolverByTenant) ResolveState(_ context.Context, tenantID uuid.UUID) (metering.TenantBillingState, error) {
	account, ok := r.accounts[tenantID]
	if !ok {
		return metering.TenantBillingState{}, nil
	}
	return metering.TenantBillingState{
		AccountID: account, Found: true, Deployment: metering.DeploymentHiveCloud,
	}, nil
}

// TestRefusesWithoutASessionPrincipal is the fail-closed rule of the brief: a
// search whose spend cannot be attributed refuses rather than falling back to
// a platform account. There is no principal on this context at all, which is
// what a shim-key request looks like after the unwrap middleware declines to
// rewrite it, and nothing reaches the upstream.
func TestRefusesWithoutASessionPrincipal(t *testing.T) {
	acct := &billingtest.Accounting{}
	dispatched := 0
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) {
			return pricedEmbeddingRoute(), nil
		},
		Dispatch: func(context.Context, string, []byte) (*http.Response, error) {
			dispatched++
			return nil, errors.New("must not be reached")
		},
		Accounting: acct.Client(t),
		Billing:    billableTenant(uuid.New()),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings",
		strings.NewReader(`{"model":"hive-embedding-default","input":["hello"]}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rr.Code, rr.Body.String())
	}
	if dispatched != 0 {
		t.Fatalf("the upstream must not be reached, got %d dispatches", dispatched)
	}
	if holds, _ := acct.Counts(); holds != 0 {
		t.Fatalf("no hold may be taken without a principal, got %d", holds)
	}
}

// TestRefusesATenantWithNoBillingAccount keeps the request off a platform
// account when the tenant itself cannot be billed: refused, not absorbed.
func TestRefusesATenantWithNoBillingAccount(t *testing.T) {
	acct := &billingtest.Accounting{}
	dispatched := 0
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) {
			return pricedEmbeddingRoute(), nil
		},
		Dispatch: func(context.Context, string, []byte) (*http.Response, error) {
			dispatched++
			return nil, errors.New("must not be reached")
		},
		Accounting: acct.Client(t),
		Billing:    billingtest.Resolver{},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, searchRequest(t, uuid.New()))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if dispatched != 0 {
		t.Fatalf("the upstream must not be reached, got %d dispatches", dispatched)
	}
}

// TestRefusesWhenBillingIsNotWired proves an edge-api built without its
// accounting seam refuses embeddings rather than serving them free, which is
// the shape that let the gateway bill nothing for three days (D-034).
func TestRefusesWhenBillingIsNotWired(t *testing.T) {
	dispatched := 0
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) {
			return pricedEmbeddingRoute(), nil
		},
		Dispatch: func(context.Context, string, []byte) (*http.Response, error) {
			dispatched++
			return nil, errors.New("must not be reached")
		},
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, searchRequest(t, uuid.New()))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rr.Code, rr.Body.String())
	}
	if dispatched != 0 {
		t.Fatalf("the upstream must not be reached, got %d dispatches", dispatched)
	}
}

// TestRefusesAnUpstreamActualAlias fails closed on the one pricing mode this
// endpoint cannot settle. CreditsForTokens returns zero for an upstream_actual
// route by design, and an embeddings response carries no content to price
// instead, so serving one would charge nothing at all while looking like a
// completed request.
func TestRefusesAnUpstreamActualAlias(t *testing.T) {
	acct := &billingtest.Accounting{}
	dispatched := 0
	route := pricedEmbeddingRoute()
	route.Pricing = inference.UpstreamActualPricing(inference.DefaultHoldEmbeddings)
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) { return route, nil },
		Dispatch: func(context.Context, string, []byte) (*http.Response, error) {
			dispatched++
			return nil, errors.New("must not be reached")
		},
		Accounting: acct.Client(t),
		Billing:    billableTenant(uuid.New()),
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, searchRequest(t, uuid.New()))

	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected a refusal, got %d: %s", rr.Code, rr.Body.String())
	}
	if dispatched != 0 {
		t.Fatalf("the upstream must not be reached, got %d dispatches", dispatched)
	}
	if holds, _ := acct.Counts(); holds != 0 {
		t.Fatalf("no hold may be taken for an alias that cannot be settled, got %d", holds)
	}
}

// TestReleasesTheHoldWhenTheUpstreamFails keeps the reservation lifecycle at
// exactly one terminal state: a failed search hands the credits back rather
// than charging the customer for nothing.
func TestReleasesTheHoldWhenTheUpstreamFails(t *testing.T) {
	acct := &billingtest.Accounting{}
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) {
			return pricedEmbeddingRoute(), nil
		},
		Dispatch: func(context.Context, string, []byte) (*http.Response, error) {
			return nil, errors.New("upstream down")
		},
		Accounting: acct.Client(t),
		Billing:    billableTenant(uuid.New()),
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, searchRequest(t, uuid.New()))

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", rr.Code, rr.Body.String())
	}
	holds, released := acct.Counts()
	if holds != 1 || released != 1 {
		t.Fatalf("expected one hold and one release, got %d and %d", holds, released)
	}
	if len(acct.Finalized()) != 0 {
		t.Fatalf("a failed search must not be charged")
	}
}

// TestEnterprisePostureServesUnheld records the one deliberate non-billable
// verdict (D-027): a customer-hosted deployment has no prepaid relationship
// with Hive, so its embeddings are served with no hold and no charge. That is
// a decision, not the silent platform absorption #1696 is about.
func TestEnterprisePostureServesUnheld(t *testing.T) {
	acct := &billingtest.Accounting{}
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) {
			return pricedEmbeddingRoute(), nil
		},
		Dispatch:   upstreamEmbeddings(1000),
		Accounting: acct.Client(t),
		Billing:    billingtest.Enterprise(),
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, searchRequest(t, uuid.New()))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if holds, _ := acct.Counts(); holds != 0 {
		t.Fatalf("the Enterprise posture takes no hold, got %d", holds)
	}
}

// TestResponseCarriesTheHiveAlias keeps the customer-facing contract identical
// to the API-key path: the response names the Hive alias, never the route.
func TestResponseCarriesTheHiveAlias(t *testing.T) {
	acct := &billingtest.Accounting{}
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) {
			return pricedEmbeddingRoute(), nil
		},
		Dispatch:   upstreamEmbeddings(1000),
		Accounting: acct.Client(t),
		Billing:    billableTenant(uuid.New()),
	})

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, searchRequest(t, uuid.New()))

	var got inference.EmbeddingsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("response did not decode: %v (%s)", err, rr.Body.String())
	}
	if got.Model != "hive-embedding-default" {
		t.Fatalf("response model %q, want the Hive alias", got.Model)
	}
	if strings.Contains(rr.Body.String(), "test-provider") {
		t.Fatalf("the provider name must never reach the customer: %s", rr.Body.String())
	}
}

// TestTheResponseIsWrittenBeforeTheChargeIsSettled pins the ordering rather
// than leaving it to a comment. Finalize is a synchronous control-plane call
// bounded at the settlement timeout and retried once, so settling first puts up
// to two of those in front of a customer who has received nothing. Open WebUI
// embeds a batch at a time and issues several of these per search turn, so the
// latency is multiplied rather than paid once.
//
// billingtest.OnFinalize fires as the charge request is handled, which is the
// only point that can observe the two relative to each other; recording the
// call afterwards cannot tell which happened first.
//
// "written", not "on the wire": an httptest.ResponseRecorder buffers
// everything and has no socket behind it, so what this proves is that the
// handler called Write before it settled. The handler's Flush is what turns
// that into bytes leaving the process, and no in-process recorder can observe
// that.
func TestTheResponseIsWrittenBeforeTheChargeIsSettled(t *testing.T) {
	rr := httptest.NewRecorder()
	bodyLenAtCharge := -1
	acct := &billingtest.Accounting{OnFinalize: func() { bodyLenAtCharge = rr.Body.Len() }}
	h := embeddings.NewHandler(embeddings.Deps{
		SelectRoute: func(context.Context, string) (inference.SelectRouteResult, error) {
			return pricedEmbeddingRoute(), nil
		},
		Dispatch:   upstreamEmbeddings(1000),
		Accounting: acct.Client(t),
		Billing:    billableTenant(uuid.New()),
	})

	h.ServeHTTP(rr, searchRequest(t, uuid.New()))

	if len(acct.Finalized()) != 1 {
		t.Fatalf("expected the charge to settle, got %d", len(acct.Finalized()))
	}
	if bodyLenAtCharge <= 0 {
		t.Fatalf("the charge settled before the response was written (body length %d at charge time)", bodyLenAtCharge)
	}
}
