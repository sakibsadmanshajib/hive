package anthropic_test

import (
	"encoding/json"
	"errors"
	"io"
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
	// http.MaxBytesReader accepts exactly MaxRequestBodyBytes and rejects the
	// (n+1)th byte, so the real boundary is n and n+1, not n-1 and n+1.
	// Using n-1 here would leave a one-byte hole: an off-by-one that started
	// rejecting exactly at the limit (rather than one past it) would pass
	// every test in this file if none of them ever sent exactly n bytes.
	chat := &fakeChat{respond: respondStatus(http.StatusUnauthorized,
		`{"error":{"message":"Incorrect API key provided.","type":"invalid_request_error","code":"invalid_api_key"}}`)}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	req := newAuthedRequest(t, messagesBodyOfSize(t, apierr.MaxRequestBodyBytes))
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
	// json.Unmarshal, not json.NewDecoder(...).Decode(...): Decode reads only
	// the first JSON value and silently ignores anything written after it,
	// so it cannot catch a handler that (wrongly) writes a second body after
	// the 413. Unmarshal requires the whole buffer to be exactly one value.
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
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

// TestHandler_ContentLengthOverLimit_RejectedWithoutReading proves the
// declared-oversize fast path fires before any body bytes are read: the
// actual body here is small, only the Content-Length header lies, and the
// downstream chain must never be called.
func TestHandler_ContentLengthOverLimit_RejectedWithoutReading(t *testing.T) {
	chat := &fakeChat{}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	req := newAuthedRequest(t, `{"model":"hive-fast","messages":[{"role":"user","content":"hi"}],"max_tokens":8}`)
	req.ContentLength = apierr.MaxRequestBodyBytes + 1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: want 413 got %d: %s", rec.Code, rec.Body.String())
	}
	if chat.calls != 0 {
		t.Fatalf("downstream calls: want 0 (rejected on declared Content-Length alone) got %d", chat.calls)
	}
}

// messagesBodyWithContentOfSize returns a valid /v1/messages body whose
// message content string is padded to make the whole body exactly n bytes.
// Unlike messagesBodyOfSize's "_pad" field (which json.Unmarshal silently
// drops, since MessagesRequest has no such field, so it never survives
// translation), padding through content survives ToOAIRequest unchanged
// (OAIMessageContent.MarshalJSON emits a plain string for un-blocked
// content), which is what TestHandler_TranslatedBodyGrowthDoesNotTripTheDownstreamCap
// needs to reproduce real translation growth pushing a body over the cap.
func messagesBodyWithContentOfSize(t *testing.T, n int) string {
	t.Helper()
	const prefix = `{"model":"hive-fast","messages":[{"role":"user","content":"`
	const suffix = `"}],"max_tokens":8}`
	padLen := n - len(prefix) - len(suffix)
	if padLen < 0 {
		t.Fatalf("target size %d too small for prefix/suffix overhead", n)
	}
	return prefix + strings.Repeat("x", padLen) + suffix
}

// dispatchLikeChat reproduces the real downstream chat handlers' own
// body-size enforcement (http.MaxBytesReader unless the request carries
// apierr.IsTrustedBody), instead of accepting any size unconditionally like
// fakeChat does. A fakeChat that skipped the cap unconditionally would never
// have caught PR #1273 review finding 2: the translated body silently
// outgrowing the cap the client's own body already cleared.
type dispatchLikeChat struct {
	calls      int
	bodyLen    int
	sawTrusted bool
}

func (d *dispatchLikeChat) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.calls++
	d.sawTrusted = apierr.IsTrustedBody(r.Context())
	if !d.sawTrusted {
		r.Body = http.MaxBytesReader(w, r.Body, apierr.MaxRequestBodyBytes)
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		w.Header().Set("Content-Type", "application/json")
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_, _ = w.Write([]byte(`{"error":{"message":"` + apierr.RequestTooLargeMessage() + `","type":"invalid_request_error"}}`))
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	d.bodyLen = len(raw)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","model":"hive-fast",` +
		`"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],` +
		`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
}

// TestHandler_TranslatedBodyGrowthDoesNotTripTheDownstreamCap is the
// regression for PR #1273 review finding 2. /v1/messages reads the client
// body at up to MaxRequestBodyBytes, then re-marshals a *translated* body
// (Stream and StreamOptions are always added, unconditionally, before
// marshalling) and hands it to the delegated chain, which used to apply the
// SAME cap to the translated bytes. A client body one byte under the limit,
// already accepted at the inbound check, would then get refused downstream
// with a "10 MiB" error for a body that never exceeded anything. The
// delegated sub-request must carry apierr.WithTrustedBody so the downstream
// cap is skipped for this server-constructed, already-validated body.
func TestHandler_TranslatedBodyGrowthDoesNotTripTheDownstreamCap(t *testing.T) {
	chat := &dispatchLikeChat{}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	body := messagesBodyWithContentOfSize(t, apierr.MaxRequestBodyBytes-1)
	req := newAuthedRequest(t, body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if chat.calls != 1 {
		t.Fatalf("downstream calls: want 1 (client body was within the limit) got %d, body=%s", chat.calls, rec.Body.String())
	}
	if !chat.sawTrusted {
		t.Fatal("delegated sub-request must carry apierr.WithTrustedBody so the downstream cap is skipped")
	}
	if chat.bodyLen <= apierr.MaxRequestBodyBytes {
		t.Fatalf("test setup did not actually exercise translation growth: translated body was %d bytes, want > %d (MaxRequestBodyBytes)", chat.bodyLen, apierr.MaxRequestBodyBytes)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 (client body never exceeded the limit) got %d: %s", rec.Code, rec.Body.String())
	}
}

// toolUseBodyUnderLimit returns a valid /v1/messages body carrying one
// assistant tool_use content block whose "input" is a large JSON array of
// quoted, comma-separated single-character strings, sized so the whole
// client body lands safely under MaxRequestBodyBytes. translate_request.go's
// tool_use conversion (`args := string(bl.Input)`) copies that raw JSON text
// byte-for-byte into a Go string, which the surrounding json.Marshal(oaiReq)
// then re-encodes as a JSON string value: every `"` in the array becomes
// `\"`, so each `"a",` element (4 raw bytes) grows to `\"a\",` (6 bytes) on
// translation, a 1.5x expansion on the array portion alone. This is the
// PR #1273 review's third-pass finding: real translation growth on a
// tool-heavy request is not the small, fixed Stream/StreamOptions delta
// alone, it can be a large fraction of the tool arguments' own size.
func toolUseBodyUnderLimit(t *testing.T, targetClientBytes int) string {
	t.Helper()
	const prefix = `{"model":"hive-fast","max_tokens":8,"messages":[{"role":"assistant","content":[` +
		`{"type":"tool_use","id":"toolu_1","name":"search","input":[`
	const suffix = `"a"]}]}]}`
	const elem = `"a",`
	budget := targetClientBytes - len(prefix) - len(suffix)
	if budget < len(elem) {
		t.Fatalf("target size %d too small for prefix/suffix/element overhead", targetClientBytes)
	}
	count := budget / len(elem)
	return prefix + strings.Repeat(elem, count) + suffix
}

// TestHandler_TranslatedBodyGrowth_ToolUseArgumentQuoteEscaping_DoesNotTripCap
// is the tool-heavy variant of the review finding above: growth here comes
// from JSON-string quote-escaping doubling the tool_use input's size on
// re-marshal, not from the small fixed Stream/StreamOptions delta, and the
// overflow window it opens is proportionally much larger.
func TestHandler_TranslatedBodyGrowth_ToolUseArgumentQuoteEscaping_DoesNotTripCap(t *testing.T) {
	chat := &dispatchLikeChat{}
	h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
	// A comfortable margin under the cap (not byte-exact like the boundary
	// tests above): this test's job is to prove the quote-escaping growth
	// mechanism, not to nail the exact byte boundary a second time.
	clientBody := toolUseBodyUnderLimit(t, apierr.MaxRequestBodyBytes-200_000)
	req := newAuthedRequest(t, clientBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if len(clientBody) >= apierr.MaxRequestBodyBytes {
		t.Fatalf("test setup bug: client body itself already at/over the cap (%d bytes)", len(clientBody))
	}
	if chat.calls != 1 {
		t.Fatalf("downstream calls: want 1 (client body was within the limit) got %d, body=%s", chat.calls, rec.Body.String())
	}
	if !chat.sawTrusted {
		t.Fatal("delegated sub-request must carry apierr.WithTrustedBody so the downstream cap is skipped")
	}
	if chat.bodyLen <= apierr.MaxRequestBodyBytes {
		t.Fatalf("test setup did not exercise quote-escaping growth: translated body was %d bytes (client body was %d), want > %d (MaxRequestBodyBytes)",
			chat.bodyLen, len(clientBody), apierr.MaxRequestBodyBytes)
	}
	// The growth here must be a large fraction of the input array's size
	// (quote-escaping, not the ~50-90 byte Stream/StreamOptions delta), so
	// require at least 100 KiB of growth to distinguish this test from the
	// small-delta case already covered above.
	if growth := chat.bodyLen - len(clientBody); growth < 100_000 {
		t.Fatalf("growth was only %d bytes, expected quote-escaping to grow the tool_use array by a large fraction of its size", growth)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200 (client body never exceeded the limit) got %d: %s", rec.Code, rec.Body.String())
	}
}

// refusingBody fails the test if anything reads it.
type refusingBody struct{ t *testing.T }

func (b *refusingBody) Read([]byte) (int, error) {
	b.t.Fatal("request body was read despite a declared Content-Length over the limit")
	return 0, nil
}

func (b *refusingBody) Close() error { return nil }

// TestMessagesDeclaredOversizeBodyRejectedBeforeRead covers both Anthropic
// routes, not /v1/messages alone. Per declared byte /v1/messages is the most
// expensive pre-auth read in edge-api, so a declared-oversize request has to
// cost approximately nothing: refused on Content-Length, body never read,
// nothing delegated downstream.
//
// Both subtests send an AUTHENTICATED request. An anonymous one to
// /v1/messages/count_tokens is refused 401 before the read is ever reached,
// so that subtest would pin nothing about body-size behaviour at all and
// would stay green with the size check deleted outright (review finding F2
// on PR #1301).
func TestMessagesDeclaredOversizeBodyRejectedBeforeRead(t *testing.T) {
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		t.Run(path, func(t *testing.T) {
			chat := &fakeChat{}
			h := anthropic.NewHandler(anthropic.Deps{OpenAIChat: chat})
			req := newAuthedRequest(t, "")
			req.URL.Path = path
			req.RequestURI = path
			req.Body = &refusingBody{t: t}
			req.ContentLength = apierr.MaxRequestBodyBytes + 1
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
			}
			if chat.calls != 0 {
				t.Fatalf("downstream calls = %d, want 0", chat.calls)
			}
			var got anthropicErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("response is not JSON: %v body=%s", err, rec.Body.String())
			}
			if got.Error.Type != "request_too_large" {
				t.Errorf("error.type: want request_too_large got %q", got.Error.Type)
			}
		})
	}
}
