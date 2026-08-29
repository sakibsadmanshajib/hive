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

// The three frames below are the VERBATIM shape LiteLLM v1.98.0 (the pinned
// image in deploy/docker/docker-compose.yml) relays for a streaming request
// carrying stream_options.include_usage. Captured against the free pool own
// upstream on 2026-08-28:
//
//	data: {... "choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}
//	data: {... "choices":[{"index":0,"delta":{},"finish_reason":"length"}]}
//	data: {... "choices":[{"index":0,"delta":{}}],"usage":{...}}
//	data: [DONE]
//
// The terminal usage frame is what every metering-aware client reads, and the
// detail that matters is that LiteLLM sends it with ONE empty-delta choice
// rather than the empty choices array OpenAI itself uses. That is the shape
// issue #1329 turns on: a len(choices)==0 test for "is this the usage frame"
// answers no here, so the frame was dropped as an ordinary post-finish chunk
// and the caller never received a token count at all.
const (
	litellmContentFrame = `{"id":"gen-abc","object":"chat.completion.chunk","created":1787967930,"model":"upstream-route","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}`
	litellmFinishFrame  = `{"id":"gen-abc","object":"chat.completion.chunk","created":1787967930,"model":"upstream-route","choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`
	litellmUsageFrame   = `{"id":"gen-abc","created":1787967930,"model":"upstream-route","object":"chat.completion.chunk","choices":[{"index":0,"delta":{}}],"usage":{"completion_tokens":20,"prompt_tokens":15,"total_tokens":35,"completion_tokens_details":{"reasoning_tokens":14},"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"cost":0.0}}`
	// deepseekPostFinishFrame is the anomaly the 2026-08-26 parity fix exists
	// for: an extra empty role/content chunk after finish_reason, carrying no
	// usage. It must still be dropped.
	deepseekPostFinishFrame = `{"id":"gen-abc","object":"chat.completion.chunk","created":1787967930,"model":"upstream-route","choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`
)

// sseServerWithFrames streams the given raw SSE data payloads in order,
// followed by the [DONE] sentinel.
func sseServerWithFrames(frames ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, frame := range frames {
			fmt.Fprintf(w, "data: %s\n\n", frame)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
}

// sseServerWithoutDone streams the given raw SSE data payloads and then closes
// cleanly, sending no [DONE] sentinel at all: an upstream that ends the body
// rather than announcing the end.
func sseServerWithoutDone(frames ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for _, frame := range frames {
			fmt.Fprintf(w, "data: %s\n\n", frame)
			flusher.Flush()
		}
	}))
}

// relayWire runs one streaming request end to end through the real
// executeStreaming and returns the bytes the caller received.
func relayWire(t *testing.T, includeUsage bool, frames ...string) string {
	t.Helper()
	litellmSrv := sseServerWithFrames(frames...)
	defer litellmSrv.Close()
	return relayWireAgainst(t, litellmSrv, includeUsage)
}

// relayWireAgainst is relayWire against an upstream the caller built, for a
// test that needs a stream shape sseServerWithFrames does not produce.
func relayWireAgainst(t *testing.T, litellmSrv *httptest.Server, includeUsage bool) string {
	t.Helper()

	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"say hi"}],"max_tokens":40,"stream":true,"stream_options":{"include_usage":true}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	if err := orch.executeStreaming(context.Background(), w, req, EndpointChatCompletions, []byte(body),
		"gpt-4o", "gpt-4o", NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, includeUsage, nil,
		orch.litellm.ChatCompletion); err != nil {
		t.Fatalf("executeStreaming: %v", err)
	}
	return w.Body.String()
}

// positionedChunk is a relayed frame together with its position on the wire, so
// an ordering assertion can name where the frame actually landed.
type positionedChunk struct {
	position int
	chunk    ChatCompletionChunk
}

// relayedUsage returns every relayed data frame carrying a usage object, in
// wire order, plus the position and count of the [DONE] sentinel.
func relayedUsage(t *testing.T, wire string) (frames []positionedChunk, doneIndex, doneCount int) {
	t.Helper()
	doneIndex = -1
	index := 0
	for _, line := range strings.Split(wire, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			doneCount++
			if doneIndex < 0 {
				doneIndex = index
			}
			index++
			continue
		}
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("relayed frame is not valid JSON: %s", payload)
		}
		if chunk.Usage != nil {
			frames = append(frames, positionedChunk{position: index, chunk: chunk})
		}
		index++
	}
	return frames, doneIndex, doneCount
}

// TestStreamRelay_ForwardsLiteLLMTerminalUsageFrame is the regression guard for
// issue #1329: the terminal usage frame LiteLLM sends after finish_reason must
// reach the caller, and must arrive before [DONE]. Asserted on the relayed
// BYTES, not on the accumulator, because settlement reading the right number is
// exactly what made this defect invisible: billing stayed correct throughout
// while the caller saw no counts at all.
func TestStreamRelay_ForwardsLiteLLMTerminalUsageFrame(t *testing.T) {
	wire := relayWire(t, true, litellmContentFrame, litellmFinishFrame, litellmUsageFrame)

	frames, doneIndex, doneCount := relayedUsage(t, wire)
	if len(frames) == 0 {
		t.Fatalf("no usage-bearing frame reached the caller; wire was:\n%s", wire)
	}
	if doneCount != 1 {
		t.Errorf("expected exactly one [DONE] sentinel, got %d; wire was:\n%s", doneCount, wire)
	}

	usage := frames[len(frames)-1]
	if doneIndex >= 0 && usage.position > doneIndex {
		t.Errorf("usage frame landed after [DONE] (position %d vs %d): every client stops reading at the sentinel", usage.position, doneIndex)
	}
	if usage.chunk.Usage.PromptTokens != 15 {
		t.Errorf("prompt_tokens on the wire = %d, want 15", usage.chunk.Usage.PromptTokens)
	}
	if usage.chunk.Usage.CompletionTokens != 20 {
		t.Errorf("completion_tokens on the wire = %d, want 20", usage.chunk.Usage.CompletionTokens)
	}
	// Asserted as emptiness, not as a loop over the choices: a loop over an
	// empty slice runs zero times and passes whatever the relay did, which is
	// the shape of assertion that cannot fail.
	if len(usage.chunk.Choices) != 0 {
		t.Errorf("terminal usage frame must be relayed in the canonical usage-only shape (choices []), got %d choice(s); wire was:\n%s", len(usage.chunk.Choices), wire)
	}
}

// TestStreamRelay_StillSuppressesPostFinishContentChunk keeps the 2026-08-26
// parity fix honest: a post-finish chunk carrying NO usage is still dropped, so
// widening the usage exception does not put the DeepSeek-family trailing chunk
// back on the wire.
func TestStreamRelay_StillSuppressesPostFinishContentChunk(t *testing.T) {
	wire := relayWire(t, true, litellmContentFrame, litellmFinishFrame, deepseekPostFinishFrame, litellmUsageFrame)

	var relayed int
	for _, line := range strings.Split(wire, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data: ") && trimmed != "data: [DONE]" {
			relayed++
		}
	}
	// content frame + finish frame + usage frame. The DeepSeek trailing chunk
	// must not be among them.
	if relayed != 3 {
		t.Errorf("expected 3 relayed data frames (content, finish, usage), got %d; wire was:\n%s", relayed, wire)
	}
}

// TestStreamRelay_CallerWithoutIncludeUsage_SeesNoUsageFrame pins the other half
// of the #1226 contract: forwarding the terminal usage frame is gated on the
// request own stream_options.include_usage, never on the include_usage this
// gateway forces upstream for billing alone.
func TestStreamRelay_CallerWithoutIncludeUsage_SeesNoUsageFrame(t *testing.T) {
	wire := relayWire(t, false, litellmContentFrame, litellmFinishFrame, litellmUsageFrame)

	if strings.Contains(wire, `"usage"`) {
		t.Errorf("a caller that never asked for usage must not receive a usage frame; wire was:\n%s", wire)
	}
	if !strings.Contains(wire, "[DONE]") {
		t.Errorf("stream must still terminate with [DONE]; wire was:\n%s", wire)
	}
}

// TestStreamRelay_UpstreamWithoutSentinel_SynthesizesUsageThenOneDone pins what
// the relay does when an upstream ends its body cleanly, with no usage frame
// and no [DONE] of its own, and the caller asked for usage.
//
// The behaviour is unchanged by this branch: the code this replaces emitted the
// same synthesized frame and the same sentinel on the same condition, only in
// the opposite order and with a second sentinel behind it. It is pinned here
// rather than left implicit because moving the sentinel out of the relay loop
// made the choice explicit for the first time (CodeRabbit, PR #1334): a client
// reads the sentinel as a complete response, and whether a truncated relay
// should say that is a question worth failing a test over if anyone changes it,
// in either direction.
func TestStreamRelay_UpstreamWithoutSentinel_SynthesizesUsageThenOneDone(t *testing.T) {
	upstream := sseServerWithoutDone(litellmContentFrame, litellmFinishFrame)
	defer upstream.Close()

	wire := relayWireAgainst(t, upstream, true)

	frames, doneIndex, doneCount := relayedUsage(t, wire)
	if len(frames) != 1 {
		t.Fatalf("expected exactly one synthesized usage frame, got %d; wire was:\n%s", len(frames), wire)
	}
	if doneCount != 1 {
		t.Errorf("expected exactly one [DONE] sentinel, got %d; wire was:\n%s", doneCount, wire)
	}
	if frames[0].position > doneIndex {
		t.Errorf("synthesized usage frame landed after [DONE] (position %d vs %d)", frames[0].position, doneIndex)
	}
	if len(frames[0].chunk.Choices) != 0 {
		t.Errorf("synthesized frame must carry no choices, got %d", len(frames[0].chunk.Choices))
	}
}

// TestStreamRelay_UpstreamWithoutSentinel_NoUsageAsked_EmitsNoSentinel is the
// other half of the same behaviour, and the reason the sentinel is not emitted
// unconditionally: with nothing to synthesize there is nothing to close, and
// the relay stays silent rather than announcing a completion the upstream never
// announced.
func TestStreamRelay_UpstreamWithoutSentinel_NoUsageAsked_EmitsNoSentinel(t *testing.T) {
	upstream := sseServerWithoutDone(litellmContentFrame, litellmFinishFrame)
	defer upstream.Close()

	wire := relayWireAgainst(t, upstream, false)

	if strings.Contains(wire, "[DONE]") {
		t.Errorf("no sentinel must be emitted for an upstream that sent none; wire was:\n%s", wire)
	}
}
