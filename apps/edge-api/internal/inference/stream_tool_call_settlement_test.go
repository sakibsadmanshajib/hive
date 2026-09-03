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

// --- PR #1762 second review: the byte-prefix rule missed two cumulative shapes ---
//
// The rule this replaces treated "cumulative" as "byte-prefix-extending", so a
// cumulative provider whose fragments are not byte prefixes of one another
// missed both prefix arms, hit the append arm, and reproduced the full
// quadratic: 87.25x truth at two hundred fragments. The fixtures below are the
// three wire shapes, all carrying the IDENTICAL final arguments, because a
// customer must not be charged differently for the same work because their
// provider chose a different framing.

// toolCallArgumentsObject builds the final arguments object for a call with n
// fields, in forward key order. Ordinary prose-shaped JSON with no
// repeated-character runs, so runCollapsible has nothing to collapse and the
// estimate is the plain byte-length one.
func toolCallArgumentsObject(n int, reversed bool) string {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < n; i++ {
		field := i
		if reversed {
			field = n - 1 - i
		}
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%q:%q`, fmt.Sprintf("field%d", field), fmt.Sprintf("reading %d", field))
	}
	b.WriteByte('}')
	return b.String()
}

// incrementalFragments is OpenAI's own contract: every fragment is the next
// piece of one partial JSON object, and none of them parses on its own.
func incrementalFragments(n int) []string {
	final := []rune(toolCallArgumentsObject(n, false))
	frags := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		frags = append(frags, string(final[len(final)*(i-1)/n:len(final)*i/n]))
	}
	return frags
}

// cumulativePrefixFragments is the raw cumulative shape: a provider resending
// its whole buffer, so every fragment is a byte prefix of the next. This is the
// only cumulative shape the rule under review caught.
func cumulativePrefixFragments(n int) []string {
	final := []rune(toolCallArgumentsObject(n, false))
	frags := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		frags = append(frags, string(final[:len(final)*i/n]))
	}
	return frags
}

// cumulativeRepairedFragments is a provider that closes each prefix into valid
// JSON before sending it, so no fragment is a byte prefix of the next. 87.25x
// at two hundred fragments under the rule this replaces.
func cumulativeRepairedFragments(n int) []string {
	frags := make([]string, 0, n)
	for k := 1; k <= n; k++ {
		frags = append(frags, toolCallArgumentsObject(k, false))
	}
	return frags
}

// cumulativeMapOrderFragments is the same, re-marshalled in map order, so
// consecutive fragments do not even share a first key. It settles at the same
// byte count as the forward-order object, which is what the assertions compare.
func cumulativeMapOrderFragments(n int) []string {
	frags := make([]string, 0, n)
	for k := 1; k <= n; k++ {
		frags = append(frags, toolCallArgumentsObject(k, true))
	}
	return frags
}

// TestFragmentStream_SettlesAtTheFinalArgumentsWhateverTheFraming is the
// classifier's own guard: every shape, at every fragment count, settles for the
// final arguments the model produced, once.
//
// The ratio is asserted in TOKENS rather than bytes because tokens are what
// gets billed, and 1.0 exactly rather than "close to 1.0" because anything
// above it is an overcharge and the fixtures carry a known final value.
func TestFragmentStream_SettlesAtTheFinalArgumentsWhateverTheFraming(t *testing.T) {
	shapes := map[string]func(int) []string{
		"incremental partial JSON": incrementalFragments,
		"cumulative byte prefixes": cumulativePrefixFragments,
		"cumulative repaired JSON": cumulativeRepairedFragments,
		"cumulative map order":     cumulativeMapOrderFragments,
	}
	for _, n := range []int{10, 60, 200} {
		for name, shape := range shapes {
			t.Run(fmt.Sprintf("%s/%d fragments", name, n), func(t *testing.T) {
				frags := shape(n)
				var f fragmentStream
				var concatenated strings.Builder
				for _, frag := range frags {
					f.add(frag)
					concatenated.WriteString(frag)
				}

				want := estimateCompletionTokens(toolCallArgumentsObject(n, false))
				got := estimateCompletionTokens(f.settle())
				if got != want {
					t.Errorf("settled at %d completion tokens, want %d (%.2fx): a turn settles for the final arguments the model produced, once, whatever framing the provider used",
						got, want, float64(got)/float64(want))
				}

				// The fixture has to be able to fail. A shape whose plain
				// concatenation already equals the truth discriminates nothing,
				// which is exactly how the rule under review passed its own
				// cumulative test while charging 87.25x on the shape next to it.
				if quadratic := estimateCompletionTokens(concatenated.String()); n > 1 && strings.HasPrefix(name, "cumulative") && quadratic <= want {
					t.Fatalf("fixture cannot discriminate: concatenating %d fragments estimates %d against a truthful %d", n, quadratic, want)
				}
			})
		}
	}
}

// TestFragmentStream_TwoHundredCumulativeFragmentsNeverExceedTheCeiling states
// the ceiling as a number rather than as a property.
//
// Whatever the classification decided, the settled value is one of the two
// readings the fragments witness and never longer than their concatenation. For
// the shapes below that puts the worst case at 1.00x, against the 87.25x the
// byte-prefix rule charged on the middle one.
func TestFragmentStream_TwoHundredCumulativeFragmentsNeverExceedTheCeiling(t *testing.T) {
	const n = 200
	for name, shape := range map[string]func(int) []string{
		"cumulative byte prefixes": cumulativePrefixFragments,
		"cumulative repaired JSON": cumulativeRepairedFragments,
		"cumulative map order":     cumulativeMapOrderFragments,
	} {
		var f fragmentStream
		for _, frag := range shape(n) {
			f.add(frag)
		}
		if !f.cumulative {
			t.Errorf("%s: never latched cumulative, so the settled value is the quadratic concatenation", name)
		}
		if got, ceiling := len(f.settle()), f.joinedLen; got > ceiling {
			t.Errorf("%s: settled %d bytes against a ceiling of %d", name, got, ceiling)
		}
		if got, want := len(f.settle()), len(toolCallArgumentsObject(n, false)); got != want {
			t.Errorf("%s: settled %d bytes, want %d (%.2fx)", name, got, want, float64(got)/float64(want))
		}
	}
}

// TestFragmentStream_ClassifiesTheStreamNotTheFragment covers the decision
// itself, including the shapes a relay fixture cannot conveniently produce.
//
// The error directions are asymmetric on purpose: a false cumulative verdict
// UNDER-counts, which is the direction this whole estimate already errs in,
// while a false incremental verdict is the overcharge.
func TestFragmentStream_ClassifiesTheStreamNotTheFragment(t *testing.T) {
	cases := []struct {
		name      string
		fragments []string
		want      string
	}{
		{"single fragment is its own value", []string{`{"a":1}`}, `{"a":1}`},
		{"no fragments at all", nil, ""},
		{"empty fragments contribute nothing", []string{`{"a"`, "", `:1}`, ""}, `{"a":1}`},
		{"incremental partial pieces concatenate", []string{`{"a"`, `:1,"b"`, `:2}`}, `{"a":1,"b":2}`},
		{"cumulative byte prefixes keep the last", []string{`{"a"`, `{"a":1`, `{"a":1}`}, `{"a":1}`},
		{"cumulative repaired JSON keeps the last", []string{`{"a":1}`, `{"a":1,"b":2}`}, `{"a":1,"b":2}`},
		{"cumulative map order keeps the last", []string{`{"a":1}`, `{"b":2,"a":1}`}, `{"b":2,"a":1}`},
		{"an identical repeat is one value, not two", []string{"get_weather", "get_weather", "get_weather"}, "get_weather"},
		{"a name streamed in pieces still concatenates", []string{"get_", "weather"}, "get_weather"},
		{"the latch is sticky once anything shows the shape", []string{`{"a":1}`, `{"a":1,"b":2}`, `{"c"`}, `{"c"`},
		{"a bare valid-JSON token is not a complete object", []string{`1`, `2`}, `12`},
		{"a quoted string fragment is not a complete object", []string{`"a`, `"b`}, `"a"b`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f fragmentStream
			for _, frag := range tc.fragments {
				f.add(frag)
			}
			if got := f.settle(); got != tc.want {
				t.Errorf("settled %q, want %q", got, tc.want)
			}
		})
	}
}

// toolCallFragmentSSEServer streams one tool call as the given fragments, with
// the function name repeated on every frame the way providers actually send it.
func toolCallFragmentSSEServer(fragments []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i, fragment := range fragments {
			fmt.Fprintln(w, toolCallOnlyChunkLine(fmt.Sprintf("t%d", i+1), toolCallOnlyFunctionName, fragment))
		}
		fmt.Fprintln(w, `data: {"id":"tf","object":"chat.completion.chunk","created":1700000000,"model":"route","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// TestExecuteStreaming_RepairedCumulativeFragments_BillTheToolCallOnce is the
// money guard for the shape the byte-prefix rule missed, end to end through the
// relay and the accounting mock rather than against the classifier alone.
//
// Two hundred fragments, each a COMPLETE valid JSON object rather than a byte
// prefix of the next, is the fixture that charged 87.25x truth (second review on
// PR #1762). Nothing downstream bounds it: clampUsageToCeiling returns early on
// ceiling <= 0 and ordinary SDK traffic sends no max_tokens, so only the
// reservation hold stood in the way.
func TestExecuteStreaming_RepairedCumulativeFragments_BillTheToolCallOnce(t *testing.T) {
	const fragments = 200
	frags := cumulativeRepairedFragments(fragments)

	rec := &accountingRecorder{}
	acctSrv := newAccountingMockWithHold(rec, DefaultHoldText)
	defer acctSrv.Close()

	litellmSrv := toolCallFragmentSSEServer(frags)
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
		estimateCompletionTokens(toolCallOnlyFunctionName+frags[fragments-1]))

	var concatenated strings.Builder
	for _, fragment := range frags {
		concatenated.WriteString(toolCallOnlyFunctionName)
		concatenated.WriteString(fragment)
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
	if actual := finalizeInt64(t, fbody, "actual_credits"); actual != truthful {
		t.Errorf("actual_credits = %d, want %d. Concatenating the %d repaired-JSON fragments charges %d, which is %.1fx the truthful figure (#928, second review on PR #1762)",
			actual, truthful, fragments, quadratic, float64(quadratic)/float64(truthful))
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("a tool-call-only turn is delivered work: it must never release the hold in full (D-034)")
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
