package chat_test

// Zero-content guard on the UPSTREAM_ACTUAL pricing arm of session chat
// (issue #1538).
//
// The guard shipped for #1526 lives inside inference.ChatSettlementCredits,
// which is the catalog-priced arm of the branch in dispatch.go. An alias with
// no catalog price settles through inference.UpstreamActualSettlement instead,
// and that function reports Delivered on any successful cost read, so a
// reasoning burn on hive-auto (the only upstream_actual alias in the live
// catalog) went on being charged the cost the upstream reported for tokens the
// customer never saw.
//
// The invariant these cases hold, on both arms: a turn is charged if and only
// if it delivered assistant-visible text, a tool call or a refusal. Everything
// else releases its hold under reason zero_content and is charged nothing.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
	"github.com/stretchr/testify/require"
)

// variablePriceRouting resolves any alias to an upstream_actual route, which is
// hive-auto's shape (D-059): no catalog token price at all, so the charge can
// only come from the cost the upstream reported for that generation.
func variablePriceRouting(t *testing.T, litellmModel string) *inference.RoutingClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in inference.SelectRouteInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          in.AliasID,
			LiteLLMModelName: litellmModel,
			Provider:         "test-provider",
			Pricing:          inference.UpstreamActualPricing(inference.DefaultHoldText),
			PriceUnit:        inference.PriceUnitTokens,
		})
	}))
	t.Cleanup(srv.Close)
	return inference.NewRoutingClient(srv.URL)
}

// The reasoning-burn signature on a variable-price alias: no visible delta
// anywhere, one terminal frame finishing on "length", and a confident usage
// block carrying the upstream's own reported cost. The cost is what makes this
// arm different from the catalog-priced one, and it is why the burn settled as
// an ordinary success: the cost read succeeded, so the settlement came back
// delivered and confirmed.
var variablePriceBurnFrames = []string{
	`{"id":"gen-burn","model":"route-burn","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
	`{"id":"gen-burn","model":"route-burn","choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811,"cost":0.0004,"completion_tokens_details":{"reasoning_tokens":700}}}`,
	"[DONE]",
}

// 0.0004 USD at 1e9 credits per USD (D-046), with no margin factor: D-064
// retired the 1.4 multiplier on 2026-09-02 and moved margin to the purchase
// price, so a burn now costs exactly what the provider charged. Written out
// rather than taken from the production helper so this cannot pass by agreeing
// with itself, and far enough from both zero and the 100,000,000 credit hold
// that neither can be mistaken for it.
const wantVariablePriceCredits int64 = 400_000

func variablePriceHandler(t *testing.T, acct *fakeAccounting, upstreamURL string) http.Handler {
	t.Helper()
	return chat.NewDispatch(chat.Deps{
		Routing:    variablePriceRouting(t, "route-burn"),
		Accounting: inference.NewAccountingClient(acct.server(t).URL),
		Billing: stubBilling{state: metering.TenantBillingState{
			AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
		}},
		LiteLLMURL: upstreamURL,
		Env:        "test",
	})
}

func variablePriceRequest(t *testing.T) *http.Request {
	t.Helper()
	return sessionChatRequest(t, uuid.New(), uuid.New(),
		`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}]}`)
}

// A completed turn on a variable-price alias that carried no assistant-visible
// text is not charged, exactly as the same turn on a catalog-priced alias is
// not. The hold goes back under the reason that tells a burn apart from a
// provider that died or a customer who hung up, and the credits Hive absorbed
// land on the counter under this surface.
func TestSessionChatDoesNotBillAVariablePriceReasoningBurn(t *testing.T) {
	reg := prometheus.NewRegistry()
	inference.RegisterZeroContentMetrics(reg)
	before, found := absorbedCredits(t, reg, inference.ZeroContentSurfaceSessionChat)
	require.True(t, found, "the series must exist from registration, so zero reads as zero")
	ragBefore, ragFound := absorbedCredits(t, reg, inference.ZeroContentSurfaceRAGStream)
	require.True(t, ragFound)

	acct := &fakeAccounting{}
	handler := variablePriceHandler(t, acct, scriptedUpstream(t, variablePriceBurnFrames...).URL)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, variablePriceRequest(t))
	require.Equal(t, http.StatusOK, rec.Code)

	reservations, finalized, released := acct.calls()
	require.Len(t, reservations, 1, "the request still takes exactly one hold")
	require.Empty(t, finalized,
		"a variable-price turn that delivered no visible text must not be charged the cost the upstream reported")
	require.Len(t, released, 1, "the hold must be handed back exactly once")
	require.Equal(t, "zero_content", released[0].Reason,
		"a burn must be distinguishable in the ledger from an upstream fault or a customer hanging up")

	after, _ := absorbedCredits(t, reg, inference.ZeroContentSurfaceSessionChat)
	require.Equal(t, float64(wantVariablePriceCredits), after-before,
		"the counter carries the upstream's own reported cost, which is what this arm would have charged")
	ragAfter, _ := absorbedCredits(t, reg, inference.ZeroContentSurfaceRAGStream)
	require.Equal(t, ragBefore, ragAfter, "a session chat burn must not be attributed to another surface")
}

// The control, and the assertion that this change moves no delivered charge:
// the same frames with one visible token in them still settle at the cost the
// upstream reported, to the credit.
func TestSessionChatStillBillsAVariablePriceTurnThatDeliveredText(t *testing.T) {
	acct := &fakeAccounting{}
	handler := variablePriceHandler(t, acct, scriptedUpstream(t,
		`{"id":"gen-burn","model":"route-burn","choices":[{"index":0,"delta":{"content":"42"}}]}`,
		variablePriceBurnFrames[1],
		"[DONE]",
	).URL)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, variablePriceRequest(t))
	require.Equal(t, http.StatusOK, rec.Code)

	_, finalized, released := acct.calls()
	require.Len(t, finalized, 1, "a delivered answer is charged")
	require.Empty(t, released)
	require.Equal(t, wantVariablePriceCredits, finalized[0].ActualCredits,
		"the delivered charge is the upstream's reported cost at the peg, unchanged by the guard")
}
