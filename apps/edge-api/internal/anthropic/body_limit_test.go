package anthropic_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/anthropic"
	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// messagesBodyOfSize returns a valid /v1/messages body padded to exactly n
// bytes via a trailing "_pad" string field. Built by direct string
// concatenation, not json.Marshal, for byte-exact control at the
// MaxRequestBodyBytes-1 / MaxRequestBodyBytes+1 boundary (issue #1250).
func messagesBodyOfSize(t *testing.T, n int) string {
	t.Helper()
	const prefix = `{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":8,"_pad":"`
	const suffix = `"}`
	padLen := n - len(prefix) - len(suffix)
	if padLen < 0 {
		t.Fatalf("target size %d too small for prefix/suffix overhead", n)
	}
	return prefix + strings.Repeat("x", padLen) + suffix
}

// TestHandler_MessagesBodyOneByteUnderLimitIsDelegated proves a body one byte
// under the cap parses and reaches the delegated chat-completions chain,
// rather than being rejected -- the widened 10 MiB cap (unified with
// /v1/chat/completions, /v1/embeddings, /v1/responses) must actually admit a
// body between the old 4 MiB and the new 10 MiB.
func TestHandler_MessagesBodyOneByteUnderLimitIsDelegated(t *testing.T) {
	chat := &fakeChat{respond: respondStatus(http.StatusUnauthorized,
		`{"error":{"message":"Incorrect API key provided.","type":"invalid_request_error","code":"invalid_api_key"}}`)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	req := newAuthedRequest(t, messagesBodyOfSize(t, apierr.MaxRequestBodyBytes-1))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if chat.calls != 1 {
		t.Fatalf("downstream calls: want 1 (body accepted and delegated) got %d, body=%s", chat.calls, rec.Body.String())
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401 (delegated) got %d: %s", rec.Code, rec.Body.String())
	}
}

// TestHandler_MessagesBodyOneByteOverLimitIsHonest413 is the core regression
// this issue is about: before the fix, a body one byte over the cap silently
// truncated, failed json.Unmarshal, and came back as a lying "invalid JSON
// body" with no mention of size. It must now be an honest 413 in Anthropic's
// own request_too_large error type, naming the limit, before the request
// ever reaches the delegated chain.
func TestHandler_MessagesBodyOneByteOverLimitIsHonest413(t *testing.T) {
	chat := &fakeChat{}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	req := newAuthedRequest(t, messagesBodyOfSize(t, apierr.MaxRequestBodyBytes+1))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if chat.calls != 0 {
		t.Fatalf("downstream calls: want 0 (rejected before delegation) got %d", chat.calls)
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: want 413 got %d: %s", rec.Code, rec.Body.String())
	}

	var got anthropicErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if got.Type != "error" {
		t.Errorf(`top-level type: want "error" got %q`, got.Type)
	}
	if got.Error.Type != "request_too_large" {
		t.Errorf("error.type: want request_too_large got %q", got.Error.Type)
	}
	if !strings.Contains(got.Error.Message, "MiB") {
		t.Errorf("error.message must name the limit, got %q", got.Error.Message)
	}
	if strings.Contains(strings.ToLower(got.Error.Message), "json") {
		t.Errorf("error.message must not misreport an oversized body as invalid JSON, got %q", got.Error.Message)
	}
}
