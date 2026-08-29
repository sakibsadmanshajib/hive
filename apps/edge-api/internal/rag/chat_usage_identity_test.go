package rag

// /v1/rag/chat is the other surface issue #1472's rule has to hold on, and
// both halves of it used to forward whatever total_tokens the upstream
// reported: the synchronous half copied it straight into ChatUsage, and the
// streaming half relayed the sanitized frame containing it.
//
// The synchronous half is covered here. The streaming half's correction is
// inference.EnforceUsageIdentityInFrame, applied in the relay loop and covered
// by TestUsageIdentityInFrame_CorrectsTheTotalAndLeavesEveryOtherMemberAlone
// in that package, since it is one shared function rather than a second copy
// of the rule written here.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// upstreamWithWrongTotal is canned200Response's shape with the live #1472
// disagreement in it: a total six times its own components, at a magnitude
// where the 1-credit floor cannot make a wrong charge look right.
const upstreamWithWrongTotal = `{"id":"upstream-123","choices":[{"message":{"role":"assistant","content":"The answer is 42 [1]."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":93}}`

// TestRAGChatHoldsTheResponseUsageToTheIdentity asserts the number the
// customer receives and the number the ledger is charged on the SAME request,
// because the two failure modes are different and either alone would pass a
// narrower test: a correction that inflated completion_tokens to meet the
// total would satisfy the identity and begin billing a class that has never
// been billed (D-055), and a correction applied only to the charge would leave
// the caller reading a total they were not billed on.
func TestRAGChatHoldsTheResponseUsageToTheIdentity(t *testing.T) {
	const inPrice, outPrice int64 = 300_000, 1_200_000
	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct, billableTenant(),
		pricedSelectRoute("route-test", inPrice, outPrice),
		fakeDispatch(http.StatusOK, upstreamWithWrongTotal, nil))

	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
	}, uuid.New()))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	var resp ChatResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, w.Body.String())
	}
	if resp.Usage == nil {
		t.Fatalf("response carried no usage block; body %s", w.Body.String())
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 5 {
		t.Errorf("components were rewritten, which would move the charge: %+v", resp.Usage)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("total_tokens = %d, want 15 (prompt 10 + completion 5): the customer received a total that disagrees with its own components",
			resp.Usage.TotalTokens)
	}

	acct.mu.Lock()
	defer acct.mu.Unlock()
	if len(acct.finalized) != 1 {
		t.Fatalf("want exactly one settlement, got %d", len(acct.finalized))
	}
	got := acct.finalized[0]
	want := (10*inPrice + 5*outPrice + 500_000) / 1_000_000
	if got.ActualCredits != want {
		t.Errorf("charged %d credits, want %d: the charge prices the components and an inflated total must not move it", got.ActualCredits, want)
	}
	if !got.TerminalUsageConfirmed {
		t.Error("a provider usage block with real token counts must settle as confirmed")
	}
}
