package chat_test

// Zero-content guard on the session chat surface (issue #1526).
//
// PR #1499 stopped a reasoning burn (a turn that spends the caller's whole
// completion ceiling on hidden reasoning and returns nothing the customer can
// read) from being billed on the API-key streaming path. This relay settles
// through inference.ChatSettlementCredits instead of settleStream, so it kept
// charging full catalog price for a blank answer on the surface the product
// demo actually runs on.
//
// Every case here asserts the TERMINAL STATE of the hold, not the status code:
// a burn releases and a served answer finalizes, and the release carries the
// reason that tells a burn apart from a provider that died or a customer who
// hung up.

import (
	"fmt"
	"io"
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

// scriptedUpstream streams the exact frames a test hands it, so a test can
// write the reasoning-burn shape the fixed usageUpstream cannot produce (it
// always emits one content delta and always finishes on "stop").
func scriptedUpstream(t *testing.T, frames ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, f := range frames {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// burnFrames is the reasoning-burn signature: no visible delta anywhere, one
// terminal frame finishing on "length" with a confident usage block, then the
// upstream's own end of stream. The prompt and completion counts price at 163
// credits, so a charge for this cannot be mistaken for a rounding difference
// against the zero it should settle at.
var burnFrames = []string{
	`{"model":"route-burn","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
	`{"model":"route-burn","choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811,"completion_tokens_details":{"reasoning_tokens":700}}}`,
	"[DONE]",
}

func burnHandler(t *testing.T, acct *fakeAccounting, upstreamURL string) http.Handler {
	t.Helper()
	return chat.NewDispatch(chat.Deps{
		Routing:    pricedRouting(t, "route-burn", 56_000, 224_000),
		Accounting: inference.NewAccountingClient(acct.server(t).URL),
		Billing: stubBilling{state: metering.TenantBillingState{
			AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
		}},
		LiteLLMURL: upstreamURL,
		Env:        "test",
	})
}

func burnRequest(t *testing.T) *http.Request {
	t.Helper()
	return sessionChatRequest(t, uuid.New(), uuid.New(),
		`{"model":"hive-free","messages":[{"role":"user","content":"hi"}]}`)
}

// A completed stream that carried no assistant-visible text is not charged:
// the customer received nothing they can read, so the hold goes back whole.
func TestSessionChatDoesNotBillAReasoningBurn(t *testing.T) {
	acct := &fakeAccounting{}
	handler := burnHandler(t, acct, scriptedUpstream(t, burnFrames...).URL)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, burnRequest(t))
	require.Equal(t, http.StatusOK, rec.Code)

	reservations, finalized, released := acct.calls()
	require.Len(t, reservations, 1, "the request still takes exactly one hold")
	require.Empty(t, finalized, "a stream that delivered no visible text must not be charged")
	require.Len(t, released, 1, "the hold must be handed back exactly once")
	require.Equal(t, "zero_content", released[0].Reason,
		"a burn must be distinguishable in the ledger from an upstream fault or a customer hanging up")
}

// The same stream with one visible token in it bills normally. Without this the
// guard above could be satisfied by never charging anything at all.
func TestSessionChatStillBillsAStreamThatDeliveredText(t *testing.T) {
	acct := &fakeAccounting{}
	handler := burnHandler(t, acct, scriptedUpstream(t,
		`{"model":"route-burn","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`{"model":"route-burn","choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811}}`,
		"[DONE]",
	).URL)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, burnRequest(t))
	require.Equal(t, http.StatusOK, rec.Code)

	_, finalized, released := acct.calls()
	require.Len(t, finalized, 1, "a delivered answer is charged")
	require.Empty(t, released)
	require.Equal(t, expectedCredits(111, 700, 56_000, 224_000), finalized[0].ActualCredits)
}

// A tool-call-only turn truncated at the ceiling delivered real work with no
// text in it, and bills. This is the case a finish_reason alone cannot separate
// from a burn.
func TestSessionChatStillBillsATruncatedToolCallTurn(t *testing.T) {
	acct := &fakeAccounting{}
	handler := burnHandler(t, acct, scriptedUpstream(t,
		`{"model":"route-burn","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{"}}]}}]}`,
		`{"model":"route-burn","choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811}}`,
		"[DONE]",
	).URL)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, burnRequest(t))
	require.Equal(t, http.StatusOK, rec.Code)

	_, finalized, released := acct.calls()
	require.Len(t, finalized, 1, "a tool-call turn is a delivered response and bills")
	require.Empty(t, released)
}

// An empty stream that never carried a finish_reason at all proves nothing:
// the relay may have been cut off before the frame that would have said so, and
// that frame could have carried the whole answer. It bills (D-034, fail closed).
func TestSessionChatBillsAnEmptyStreamWithNoFinishReason(t *testing.T) {
	acct := &fakeAccounting{}
	handler := burnHandler(t, acct, scriptedUpstream(t,
		`{"model":"route-burn","choices":[{"index":0,"delta":{"role":"assistant"}}],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811}}`,
		"[DONE]",
	).URL)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, burnRequest(t))
	require.Equal(t, http.StatusOK, rec.Code)

	_, finalized, _ := acct.calls()
	require.Len(t, finalized, 1, "an unfinished stream bills; only a finished one can be proven empty")
}

// An empty answer the upstream called COMPLETE is not a burn. finish_reason
// "stop" means the model chose to end there, genuine empty answers exist, and
// the sync guard has always declined to second-guess that verdict. It bills.
func TestSessionChatStillBillsAnEmptyAnswerThatFinishedOnStop(t *testing.T) {
	acct := &fakeAccounting{}
	handler := burnHandler(t, acct, scriptedUpstream(t,
		`{"model":"route-burn","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"model":"route-burn","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":111,"completion_tokens":700,"total_tokens":811}}`,
		"[DONE]",
	).URL)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, burnRequest(t))
	require.Equal(t, http.StatusOK, rec.Code)

	_, finalized, released := acct.calls()
	require.Len(t, finalized, 1, "an upstream that called its own response complete is charged")
	require.Empty(t, released)
}

// Some providers end a stream by closing the body with no [DONE] sentinel at
// all. That is still the upstream's own end of stream, so a burn delivered that
// way is a burn: the relay was not cut off, it read everything there was.
func TestSessionChatDoesNotBillABurnThatEndedWithoutTheSentinel(t *testing.T) {
	acct := &fakeAccounting{}
	handler := burnHandler(t, acct, scriptedUpstream(t, burnFrames[:len(burnFrames)-1]...).URL)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, burnRequest(t))
	require.Equal(t, http.StatusOK, rec.Code)

	_, finalized, released := acct.calls()
	require.Empty(t, finalized, "a cleanly ended stream with no visible text is still a burn")
	require.Len(t, released, 1)
	require.Equal(t, "zero_content", released[0].Reason)
}

// absorbedCredits reads one surface's series off a registry, reporting
// separately whether the series exists at all. Absent is a distinct failure
// from zero: a CounterVec emits nothing until its first increment, so a missing
// series and a quiet day are byte-identical on a dashboard unless the series is
// created at registration.
func absorbedCredits(t *testing.T, reg *prometheus.Registry, surface string) (float64, bool) {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "hive_chat_zero_content_absorbed_credits_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "surface" && label.GetValue() == surface {
					return metric.GetCounter().GetValue(), true
				}
			}
		}
	}
	return 0, false
}

// The counter's only job here is to be provable: it must be able to fire in the
// population it claims to cover. A counter keyed on a quantity a surface never
// has reads clean forever while the loss continues, which is what
// hive_usage_reasoning_tokens_unbilled_total did on the RAG path (#1472).
//
// This test asserts three things about the session chat population: the series
// exists before anything happens, it moves when a burn passes through this
// surface, and the other two surfaces do not, which is what proves the label is
// wired to the surface it names rather than to a constant.
func TestSessionChatZeroContentCounterCanFire(t *testing.T) {
	reg := prometheus.NewRegistry()
	inference.RegisterZeroContentMetrics(reg)

	before, found := absorbedCredits(t, reg, inference.ZeroContentSurfaceSessionChat)
	require.True(t, found, "the series must exist from registration, so zero reads as zero")
	ragBefore, ragFound := absorbedCredits(t, reg, inference.ZeroContentSurfaceRAGStream)
	require.True(t, ragFound)

	acct := &fakeAccounting{}
	handler := burnHandler(t, acct, scriptedUpstream(t, burnFrames...).URL)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, burnRequest(t))
	require.Equal(t, http.StatusOK, rec.Code)

	after, _ := absorbedCredits(t, reg, inference.ZeroContentSurfaceSessionChat)
	require.Greater(t, after, before,
		"a burn on this surface must be counted, in credits, where an operator can see it")
	require.Equal(t, float64(expectedCredits(111, 700, 56_000, 224_000)), after-before,
		"the figure counted is what the customer would have been charged")

	ragAfter, _ := absorbedCredits(t, reg, inference.ZeroContentSurfaceRAGStream)
	require.Equal(t, ragBefore, ragAfter, "a session chat burn must not be attributed to another surface")
}
