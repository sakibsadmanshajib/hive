package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- issue #928 defect 1: a tool-call-only turn priced its output at nothing ---
//
// AccumulateContent ignores tool-call deltas, deliberately: they are not text
// the customer can read. Settlement then estimated a completion count from that
// same empty text whenever the upstream sent no usable usage block, so the
// entire OUTPUT half of a turn whose whole output was a tool call was worth
// zero. That is the modal shape of agent traffic, which PR #920 routed onto this
// path. D-055's fail-closed clause names the shape by name.
//
// The settled figure for the ordinary incremental wire shape is pinned by
// TestExecuteStreaming_ToolCallOnlyTurn_Billed_NotUpstreamError in
// stream_usage_missing_test.go. What is pinned HERE is the other wire shape, and
// the fragment-folding rule that keeps the two agreeing.

// cumulativeToolCallSSEServer streams the SAME tool call as the incremental
// fixture, in the OTHER wire shape: every fragment repeats the whole argument
// string built so far, rather than carrying only the next piece.
//
// Nothing on the wire enforces OpenAI's incremental contract, and this repo's
// providers are DB-managed and admin-addable, so this shape is one the gateway
// will meet and does not control. Concatenating it is quadratic: with sixty
// fragments the settlement was 31.1x the truthful figure, bounded only by the
// reservation hold, because ordinary SDK traffic sets no max_tokens for the
// ceiling bound to catch (review finding on PR #1762).
func cumulativeToolCallSSEServer(fragments int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		runes := []rune(toolCallOnlyArguments)
		for i := 1; i <= fragments; i++ {
			end := len(runes) * i / fragments
			fmt.Fprintln(w, toolCallOnlyChunkLine(fmt.Sprintf("t%d", i),
				toolCallOnlyFunctionName, string(runes[:end])))
		}
		fmt.Fprintln(w, `data: {"id":"tf","object":"chat.completion.chunk","created":1700000000,"model":"route","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// TestExecuteStreaming_CumulativeToolCallFragments_BillTheToolCallOnce is the
// money guard for the overcharge the fragment-folding rule exists to prevent.
//
// It asserts the EXACT figure, and the same figure the incremental fixture
// settles at, because the two wire shapes carry the identical tool call: a
// customer must not be charged differently for the same work because their
// provider chose a different framing.
func TestExecuteStreaming_CumulativeToolCallFragments_BillTheToolCallOnce(t *testing.T) {
	const fragments = 60

	rec := &accountingRecorder{}
	acctSrv := newAccountingMockWithHold(rec, DefaultHoldText)
	defer acctSrv.Close()

	litellmSrv := cumulativeToolCallSSEServer(fragments)
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	reqBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"What is the weather in Dhaka right now?"}],"stream":true}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(reqBody)))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, reqBody,
		"gpt-4o", "gpt-4o", NeedFlags{NeedChatCompletions: true, NeedStreaming: true},
		DefaultHoldText, false, nil, orch.litellm.ChatCompletionStream)

	promptTokens := estimateCompletionTokens(promptText(EndpointChatCompletions, reqBody))
	truthful := CreditsForTokens(routeMockPricing, promptTokens, 0, 0,
		estimateCompletionTokens(toolCallOnlyFunctionName+toolCallOnlyArguments))

	// What concatenation would have charged: every prefix counted in full, which
	// is the arithmetic series over the fragments.
	var concatenated strings.Builder
	runes := []rune(toolCallOnlyArguments)
	for i := 1; i <= fragments; i++ {
		concatenated.WriteString(toolCallOnlyFunctionName)
		concatenated.WriteString(string(runes[:len(runes)*i/fragments]))
	}
	quadratic := CreditsForTokens(routeMockPricing, promptTokens, 0, 0,
		estimateCompletionTokens(concatenated.String()))
	if quadratic <= truthful {
		t.Fatalf("fixture cannot discriminate: concatenating %d fragments prices at %d against a truthful %d", fragments, quadratic, truthful)
	}

	fbody, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("a served tool-calling turn is billable work; calls seen: %+v", rec.calls)
	}
	actual := finalizeInt64(t, fbody, "actual_credits")
	if actual != truthful {
		t.Errorf("actual_credits = %d, want %d. Concatenating the %d cumulative fragments charges %d, which is %.1fx the truthful figure (#928, PR #1762 review)",
			actual, truthful, fragments, quadratic, float64(quadratic)/float64(truthful))
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("a tool-call-only turn is delivered work: it must never release the hold in full (D-034)")
	}
}

// TestFoldToolCallFragment covers the reconciliation rule directly, including
// the shapes a relay test cannot conveniently produce.
//
// The two prefix arms are also the safe direction whenever the guess is wrong:
// treating an incremental stream as cumulative UNDER-counts, which is the
// direction this whole estimate already errs in, while the reverse is the
// overcharge.
func TestFoldToolCallFragment(t *testing.T) {
	cases := []struct {
		name        string
		accumulated string
		fragment    string
		want        string
	}{
		{"first fragment", "", `{"a"`, `{"a"`},
		{"incremental next piece", `{"a"`, `:1}`, `{"a":1}`},
		{"cumulative repeat of everything so far", `{"a"`, `{"a":1}`, `{"a":1}`},
		{"identical resend", `{"a":1}`, `{"a":1}`, `{"a":1}`},
		{"shorter resend of a prefix", `{"a":1}`, `{"a"`, `{"a":1}`},
		{"empty fragment contributes nothing", `{"a":1}`, "", `{"a":1}`},
		{"empty on empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := foldToolCallFragment(tc.accumulated, tc.fragment); got != tc.want {
				t.Errorf("foldToolCallFragment(%q, %q) = %q, want %q", tc.accumulated, tc.fragment, got, tc.want)
			}
		})
	}
}

// TestAccumulateToolCalls_CountsTheModelOutputAndNotTheFraming pins the
// direction the estimate must err in. A streamed tool call repeats its id, type
// and index on every fragment, so counting raw delta bytes would bill roughly
// twenty times the envelope for the payload -- an OVER-count on a figure that
// gets charged, which is the one direction estimateCompletionTokens refuses to
// err in (see bytesPerToken).
func TestAccumulateToolCalls_CountsTheModelOutputAndNotTheFraming(t *testing.T) {
	acc := &UsageAccumulator{}
	acc.accumulateToolCalls(json.RawMessage(`[{"index":0,"id":"call_abc123","type":"function","function":{"name":"get","arguments":"{\"a\":1}"}}]`), nil)
	if got, want := acc.ToolCallOutput(), `get{"a":1}`; got != want {
		t.Errorf("tool-call output = %q, want %q: only the name and the arguments are model output", got, want)
	}

	legacy := &UsageAccumulator{}
	legacy.accumulateToolCalls(nil, json.RawMessage(`{"name":"get","arguments":"{\"a\":1}"}`))
	if got, want := legacy.ToolCallOutput(), `get{"a":1}`; got != want {
		t.Errorf("legacy function_call output = %q, want %q", got, want)
	}

	unparseable := &UsageAccumulator{}
	unparseable.accumulateToolCalls(json.RawMessage(`not json`), nil)
	if unparseable.ToolCallOutput() != "" {
		t.Errorf("an undecodable delta contributed %q: a shape this gateway cannot read is not one whose token count it knows", unparseable.ToolCallOutput())
	}
}

// TestAccumulateToolCalls_ParallelCallsAreFoldedPerIndex proves the folding is
// per tool call and not one flat buffer. Two calls streamed interleaved must
// each reconcile against their OWN accumulated value: a shared buffer would read
// the second call's first fragment as a continuation of the first call's, and
// the prefix rule would then either drop it or double it.
func TestAccumulateToolCalls_ParallelCallsAreFoldedPerIndex(t *testing.T) {
	acc := &UsageAccumulator{}
	frame := func(index int, name, args string) json.RawMessage {
		raw, _ := json.Marshal([]map[string]any{{
			"index":    index,
			"function": map[string]string{"name": name, "arguments": args},
		}})
		return raw
	}
	acc.accumulateToolCalls(frame(0, "alpha", `{"x":`), nil)
	acc.accumulateToolCalls(frame(1, "beta", `{"y":`), nil)
	acc.accumulateToolCalls(frame(0, "", `1}`), nil)
	acc.accumulateToolCalls(frame(1, "", `2}`), nil)

	if got, want := acc.ToolCallOutput(), `alpha{"x":1}beta{"y":2}`; got != want {
		t.Errorf("tool-call output = %q, want %q: index order, each call folded against its own value", got, want)
	}
}
