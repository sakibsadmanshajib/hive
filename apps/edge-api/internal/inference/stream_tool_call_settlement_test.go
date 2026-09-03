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
// zero and only the prompt was charged. That is the modal shape of agent
// traffic, which PR #920 routed onto this path.
//
// D-055's fail-closed clause names this shape by name ("no served request bills
// zero ... including tool-call-only turns with no usage block"), and D-034 is
// the standing rule that delivered work is charged for. Nothing here widens
// which token classes are billed: completion tokens were always billed, and the
// estimate of them simply could not see the one output shape carrying no text.

// toolCallLongArguments is the argument fragment the fixture below streams. Long
// enough that its estimated token count, and so the credit difference the fix
// makes, is a figure rather than a rounding artefact. Deliberately ordinary
// prose-shaped JSON with no repeated-character runs, so runCollapsible has
// nothing to collapse and the estimate is the plain byte-length one.
var toolCallLongArguments = `{"query":"` + strings.Repeat("summarise the quarterly revenue report for the north region, ", 40) + `"}`

const toolCallLongFunctionName = "search_documents"

// toolCallOnlyLongArgsSSEServer streams a single tool-call-only chunk finishing on
// tool_calls, then [DONE], and never sends a usage frame. That last part is the
// precondition for the whole defect: with a usage block the provider's own
// completion count covers the tool call, and settlement is confirmed.
func toolCallOnlyLongArgsSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		toolCalls, _ := json.Marshal([]map[string]any{{
			"index": 0,
			"id":    "call_abc123",
			"type":  "function",
			"function": map[string]string{
				"name":      toolCallLongFunctionName,
				"arguments": toolCallLongArguments,
			},
		}})
		finish := "tool_calls"
		chunk := ChatCompletionChunk{
			ID:      "chunk-1",
			Object:  "chat.completion.chunk",
			Created: 1700000000,
			Model:   "route",
			Choices: []ChunkChoice{{
				Index:        0,
				Delta:        ChunkDelta{ToolCalls: toolCalls},
				FinishReason: &finish,
			}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintln(w, "data: "+string(b))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// toolCallLongRequestBody is the caller's request. It carries a real user
// message so promptText has something to estimate, and no max_tokens, which is
// the shape of ordinary SDK traffic and the one #1198 showed the capture bound
// has to cover anyway.
var toolCallLongRequestBody = []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"` +
	strings.Repeat("find the north region revenue figures for the last four quarters. ", 20) +
	`"}],"tools":[{"type":"function","function":{"name":"search_documents"}}],"stream":true}`)

// TestExecuteStreaming_ToolCallOnlyTurn_ChargesForTheToolCallItProduced is the
// money guard for issue #928 defect 1.
//
// It pins the exact figure rather than "more than nothing", because the failure
// mode is a specific undercharge and not an absence of billing: before the fix
// the turn DID settle (the #1215 forwarded-chunk fallback and the #1198 priced
// capture between them make sure of that), it just settled with the completion
// half of the capture priced at zero tokens. An assertion of "credits >= 1"
// passes straight through that, exactly as assertPricedCapture's own comment
// warns about the #1198 defect.
func TestExecuteStreaming_ToolCallOnlyTurn_ChargesForTheToolCallItProduced(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMockWithHold(rec, DefaultHoldText)
	defer acctSrv.Close()

	litellmSrv := toolCallOnlyLongArgsSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(toolCallLongRequestBody)))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	_ = orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, toolCallLongRequestBody,
		"gpt-4o", "gpt-4o", NeedFlags{NeedChatCompletions: true, NeedStreaming: true},
		DefaultHoldText, false, nil, orch.litellm.ChatCompletionStream)

	// The route the mock resolves to, rebuilt here so the expected figure comes
	// from the same catalog price the settlement used.
	route := SelectRouteResult{AliasID: "gpt-4o", Pricing: catalogHiveFast, PriceUnit: PriceUnitTokens}
	promptTokens := estimateCompletionTokens(promptText(EndpointChatCompletions, toolCallLongRequestBody))
	toolTokens := estimateCompletionTokens(toolCallLongFunctionName + toolCallLongArguments)
	if toolTokens == 0 {
		t.Fatal("fixture produced no estimable tool-call text: the assertion below would be vacuous")
	}
	want := CreditsForTokens(route, promptTokens, 0, 0, toolTokens)
	promptOnly := CreditsForTokens(route, promptTokens, 0, 0, 0)
	if want == promptOnly {
		t.Fatalf("fixture is too small to discriminate: prompt-only and prompt-plus-tool-call both price at %d credits", want)
	}

	if rec.has("/internal/accounting/reservations/release") {
		t.Error("a tool-call-only turn is delivered work: it must never release the hold in full (D-034)")
	}
	fbody, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation for a delivered tool-call turn; calls seen: %+v", rec.calls)
	}
	got := finalizeInt64(t, fbody, "actual_credits")
	if got != want {
		t.Errorf("actual_credits = %d, want %d (prompt %d tokens + tool-call output %d tokens at the alias price). Priced at %d, the prompt alone, before the fix (#928 defect 1)",
			got, want, promptTokens, toolTokens, promptOnly)
	}
	if got > DefaultHoldText {
		t.Errorf("actual_credits = %d exceeds the %d credit hold: a capture may never exceed what was authorized", got, DefaultHoldText)
	}
	if confirmed, _ := fbody["terminal_usage_confirmed"].(bool); confirmed {
		t.Error("terminal_usage_confirmed must be false: the upstream sent no usage block, so this is a capture and not a measurement")
	}
}

// TestAppendToolCallText_CountsTheModelOutputAndNotTheFraming pins the
// direction the estimate must err in. A streamed tool call repeats its id, type
// and index on every fragment, so counting raw delta bytes would bill roughly
// twenty times the envelope for the payload -- an OVER-count on a figure that
// gets charged, which is the one direction estimateCompletionTokens refuses to
// err in (see bytesPerToken).
func TestAppendToolCallText_CountsTheModelOutputAndNotTheFraming(t *testing.T) {
	raw := json.RawMessage(`[{"index":0,"id":"call_abc123","type":"function","function":{"name":"get","arguments":"{\"a\":1}"}}]`)
	var b strings.Builder
	appendToolCallText(&b, raw, nil)
	if got, want := b.String(), `get{"a":1}`; got != want {
		t.Errorf("tool-call text = %q, want %q: only the name and the arguments are model output", got, want)
	}

	var legacy strings.Builder
	appendToolCallText(&legacy, nil, json.RawMessage(`{"name":"get","arguments":"{\"a\":1}"}`))
	if got, want := legacy.String(), `get{"a":1}`; got != want {
		t.Errorf("legacy function_call text = %q, want %q", got, want)
	}

	var unparseable strings.Builder
	appendToolCallText(&unparseable, json.RawMessage(`not json`), nil)
	if unparseable.Len() != 0 {
		t.Errorf("an undecodable delta contributed %q: a shape this gateway cannot read is not one whose token count it knows", unparseable.String())
	}
}
