package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeInference is a configurable test double for InferencePort.
type fakeInference struct {
	mu sync.Mutex

	// handler returns (body, usage, statusCode, err) per call. May be nil; if
	// nil, returns a successful empty response.
	handler func(ctx context.Context, attempt int, model string, body json.RawMessage) (json.RawMessage, *Usage, int, error)

	calls int

	// concurrent tracks instantaneous in-flight count for concurrency tests.
	concurrent     atomic.Int32
	maxConcurrent  atomic.Int32
	customIDCounts map[string]int
}

func (f *fakeInference) ChatCompletion(ctx context.Context, model string, body json.RawMessage) (json.RawMessage, *Usage, int, error) {
	f.concurrent.Add(1)
	defer f.concurrent.Add(-1)
	for {
		cur := f.concurrent.Load()
		mx := f.maxConcurrent.Load()
		if cur <= mx {
			break
		}
		if f.maxConcurrent.CompareAndSwap(mx, cur) {
			break
		}
	}

	f.mu.Lock()
	f.calls++
	attempt := 0
	// Track per-customID attempts via embedded JSON marker if present.
	var probe struct {
		ID string `json:"_test_id"`
	}
	_ = json.Unmarshal(body, &probe)
	if f.customIDCounts == nil {
		f.customIDCounts = map[string]int{}
	}
	if probe.ID != "" {
		f.customIDCounts[probe.ID]++
		attempt = f.customIDCounts[probe.ID]
	} else {
		attempt = f.calls
	}
	f.mu.Unlock()

	if f.handler != nil {
		return f.handler(ctx, attempt, model, body)
	}
	return json.RawMessage(`{"id":"chat_x","choices":[]}`), &Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}, 200, nil
}

func mustBody(t *testing.T, model string, extra string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"_extra":   extra,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// Test 4: bounded concurrency — given Concurrency=4 and 100 lines,
// instrumentation observes max 4 in-flight calls at any sample point.
func TestDispatcher_BoundedConcurrency(t *testing.T) {
	infer := &fakeInference{
		handler: func(ctx context.Context, attempt int, model string, body json.RawMessage) (json.RawMessage, *Usage, int, error) {
			// Hold each call long enough that all 4 workers race against the cap.
			time.Sleep(10 * time.Millisecond)
			return json.RawMessage(`{}`), &Usage{TotalTokens: 1}, 200, nil
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 4, MaxRetries: 3, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	in := make(chan InputLine)
	out := make(chan DispatchResult, 100)
	go func() {
		for i := 0; i < 100; i++ {
			in <- InputLine{
				CustomID:     fmt.Sprintf("req-%d", i),
				Method:       "POST",
				URL:          "/v1/chat/completions",
				Body:         mustBody(t, "alias-1", fmt.Sprintf("p%d", i)),
				Alias:        "alias-1",
				LiteLLMModel: "openrouter/gpt-4o-mini",
			}
		}
		close(in)
	}()

	disp.Pool(context.Background(), in, out)
	close(out)

	count := 0
	for range out {
		count++
	}
	if count != 100 {
		t.Fatalf("results=%d want 100", count)
	}
	if peak := disp.PeakInFlight(); peak > 4 {
		t.Fatalf("peak inflight %d exceeded cap 4", peak)
	}
	if peak := infer.maxConcurrent.Load(); peak > 4 {
		t.Fatalf("inference max concurrent %d exceeded cap 4", peak)
	}
}

// Test 5: retry policy — fake handler returns HTTP 503 twice then 200 → line
// settles success on attempt 3; returns HTTP 400 → line fails immediately on
// attempt 1 with no retry.
func TestDispatcher_Retry503ThenSuccess(t *testing.T) {
	var attempts atomic.Int32
	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			n := attempts.Add(1)
			if n < 3 {
				return nil, nil, 503, errors.New("upstream unavailable")
			}
			return json.RawMessage(`{"ok":true}`), &Usage{TotalTokens: 7}, 200, nil
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 3, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "alias-1", ""),
		Alias:        "alias-1",
		LiteLLMModel: "openrouter/gpt-4o-mini",
	})
	if res.Error != nil {
		t.Fatalf("expected success after retries, got error %+v", res.Error)
	}
	if res.Output == nil || res.Output.Response.StatusCode != 200 {
		t.Fatalf("expected 200 output, got %+v", res.Output)
	}
	if res.Attempts != 3 {
		t.Fatalf("attempts=%d want 3", res.Attempts)
	}
	if res.ConsumedCredits != 7 {
		t.Fatalf("credits=%d want 7", res.ConsumedCredits)
	}
}

func TestDispatcher_4xxNoRetry(t *testing.T) {
	var attempts atomic.Int32
	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			attempts.Add(1)
			return nil, nil, 400, errors.New("bad request")
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 3, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "alias-1", ""),
		Alias:        "alias-1",
		LiteLLMModel: "openrouter/gpt-4o-mini",
	})
	if res.Output != nil {
		t.Fatalf("expected error, got success")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts=%d want 1 (no retry on 4xx)", attempts.Load())
	}
	if res.Error.Error.Code != "invalid_request" {
		t.Fatalf("code=%q want invalid_request", res.Error.Error.Code)
	}
	if res.ConsumedCredits != 0 {
		t.Fatalf("credits=%d want 0", res.ConsumedCredits)
	}
}

// Test 6: per-line context deadline — handler that exceeds LineTimeout is
// canceled; line written to errors.jsonl with code=timeout (or upstream_error).
func TestDispatcher_LineTimeout(t *testing.T) {
	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			select {
			case <-ctx.Done():
				return nil, nil, 0, ctx.Err()
			case <-time.After(2 * time.Second):
				return json.RawMessage(`{}`), &Usage{TotalTokens: 1}, 200, nil
			}
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 1, LineTimeout: 50 * time.Millisecond}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	// LineTimeout=50ms is below floor=5s, so Validate clamps. Force directly.
	disp.cfg.LineTimeout = 50 * time.Millisecond

	start := time.Now()
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "alias-1", ""),
		Alias:        "alias-1",
		LiteLLMModel: "openrouter/gpt-4o-mini",
	})
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Fatalf("dispatch did not honor timeout, elapsed=%s", elapsed)
	}
	if res.Output != nil {
		t.Fatalf("expected error on timeout, got success")
	}
}

// Test 7: provider-name sanitization — fake handler returns error message
// containing "openrouter upstream rejected"; ErrorLine.message must NOT contain
// "openrouter".
func TestDispatcher_ProviderNameSanitized(t *testing.T) {
	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			return nil, nil, 502, errors.New("openrouter upstream rejected the request via litellm gateway")
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 2, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "alias-1", ""),
		Alias:        "alias-1",
		LiteLLMModel: "openrouter/gpt-4o-mini",
	})
	if res.Error == nil {
		t.Fatalf("expected error result")
	}
	msg := res.Error.Error.Message
	for _, banned := range []string{"openrouter", "groq", "litellm"} {
		if containsCI(msg, banned) {
			t.Fatalf("sanitized message still contains %q: %q", banned, msg)
		}
	}
}

// Test 8: output-body sanitization -- issue #1235. The local batch executor
// writes InferencePort's raw response body straight into output.jsonl's
// response.body, which reaches the customer verbatim (a batch output file is
// customer-retrievable output, same as a sync/stream response). A raw
// upstream body carries the exact identity leaks PR #1222 closed on the
// sync/stream boundaries: an OpenRouter "gen-*" id, a "provider" field
// naming the actual upstream (observed live: "DigitalOcean"), a
// "system_fingerprint", usage.cost/cost_details, and a "model" field that
// echoes LiteLLM's internal route name rather than the customer's alias.
// None of that may reach output.jsonl. Reproduces the live shape captured
// against a real LiteLLM route-deepseek-v4-pro response (2026-08-28).
func TestDispatcher_OutputBodySanitized_StripsIdentityLeaks(t *testing.T) {
	// The full, untrimmed real capture, including choices[].provider_specific_fields
	// and choices[].message.provider_specific_fields (issue #1280, fixed in
	// this same PR): a trimmed fixture that omitted a known-leaked shape
	// would make this test pass partly because the leak was left out of
	// the input (PR #1253 review finding).
	rawUpstreamBody := `{"id":"gen-1787946282-BraVtgcskggFgHSaafrV","created":1787946282,"model":"route-deepseek-v4-pro","object":"chat.completion","choices":[{"finish_reason":"stop","index":0,"message":{"content":"Hi!","role":"assistant","provider_specific_fields":{"reasoning":null,"refusal":null}},"provider_specific_fields":{"native_finish_reason":"stop"}}],"usage":{"completion_tokens":3,"prompt_tokens":9,"total_tokens":12,"cost":2.376e-05,"is_byok":false,"cost_details":{"upstream_inference_cost":2.376e-05}},"provider":"DigitalOcean"}`

	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			return json.RawMessage(rawUpstreamBody), &Usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12}, 200, nil
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 1, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "customer-alias-1", ""),
		Alias:        "customer-alias-1",
		LiteLLMModel: "openrouter/deepseek/deepseek-v4-pro-0813",
	})
	if res.Output == nil || res.Output.Response == nil {
		t.Fatalf("expected success output, got %+v", res)
	}
	body := string(res.Output.Response.Body)

	for _, leak := range []string{
		"gen-1787946282-BraVtgcskggFgHSaafrV", // raw upstream id, OpenRouter shape
		"DigitalOcean",                        // actual provider name
		"\"provider\"",                        // provider key at all
		"system_fingerprint",
		"\"cost\"",
		"cost_details",
		"is_byok",
		"route-deepseek-v4-pro", // internal LiteLLM route name, not the customer alias
		"provider_specific_fields",
		"native_finish_reason",
		"\"reasoning\"",
		"\"refusal\"",
	} {
		if strings.Contains(body, leak) {
			t.Fatalf("output.jsonl body leaked %q:\n%s", leak, body)
		}
	}

	var decoded struct {
		ID    string `json:"id"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(res.Output.Response.Body, &decoded); err != nil {
		t.Fatalf("sanitized body is not valid JSON: %v\n%s", err, body)
	}
	if !strings.HasPrefix(decoded.ID, "chatcmpl-") {
		t.Fatalf("id=%q want gateway-minted chatcmpl- prefix", decoded.ID)
	}
	if decoded.Model != "customer-alias-1" {
		t.Fatalf("model=%q want customer alias %q", decoded.Model, "customer-alias-1")
	}
}

// Test 8b: a truncated upstream response (InferencePort.ChatCompletion
// detecting an over-cap body, issue #1255 finding #2) must never be
// recorded as a successful line -- it settles as a failed line with an
// honest reason via the existing errors.jsonl shape, not a second
// convention.
func TestDispatcher_TruncatedResponseIsNeverASuccess(t *testing.T) {
	var calls atomic.Int32
	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			calls.Add(1)
			// Mirrors LiteLLMInferenceClient.ChatCompletion's truncation
			// shape exactly: nil body, status 0, an error wrapping
			// ErrTruncatedUpstreamResponse.
			return nil, nil, 0, fmt.Errorf("%w: exceeded 4194304 byte limit", ErrTruncatedUpstreamResponse)
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 3, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "customer-alias-1", ""),
		Alias:        "customer-alias-1",
		LiteLLMModel: "openrouter/deepseek/deepseek-v4-pro-0813",
	})
	if res.Output != nil {
		t.Fatalf("truncated response recorded as a success: %+v", res.Output)
	}
	if res.Error == nil {
		t.Fatalf("expected a failed line, got neither output nor error")
	}
	if res.Error.Error.Code != "response_too_large" {
		t.Fatalf("code=%q want response_too_large", res.Error.Error.Code)
	}
	if res.ConsumedCredits != 0 {
		t.Fatalf("credits=%d want 0 for a failed line", res.ConsumedCredits)
	}
	// Deterministic failure: MaxRetries=3 but a truncation is terminal on
	// the first attempt, unlike a transient failure (PR #1253 review:
	// retrying a byte-cap truncation only re-pays the upstream for the
	// same guaranteed failure).
	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream called %d times, want exactly 1 (truncation must not retry)", got)
	}
}

// Test 8c: the inverse of Test 8b -- an unparseable 2xx body (not a
// truncation) IS worth retrying, since it may be a transient upstream
// encoding quirk rather than a deterministic failure. Confirms the retry
// policies were actually swapped, not just the truncation side fixed.
func TestDispatcher_UnparseableUpstreamBody_RetriesThenFails(t *testing.T) {
	var calls atomic.Int32
	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			calls.Add(1)
			// 200 status with a body that is not valid JSON at all --
			// VariablePriceFrame's json.Unmarshal fails, ok=false, on
			// every attempt in this test.
			return json.RawMessage(`not valid json`), &Usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12}, 200, nil
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 3, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "customer-alias-1", ""),
		Alias:        "customer-alias-1",
		LiteLLMModel: "openrouter/deepseek/deepseek-v4-pro-0813",
	})
	if res.Output != nil {
		t.Fatalf("unparseable 200 body recorded as success: %+v", res.Output)
	}
	if res.Error == nil {
		t.Fatalf("expected a failed line, got neither output nor error")
	}
	if res.Error.Error.Code != "upstream_error" {
		t.Fatalf("code=%q want upstream_error", res.Error.Error.Code)
	}
	if res.ConsumedCredits != 0 {
		t.Fatalf("credits=%d want 0 for a failed line", res.ConsumedCredits)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("upstream called %d times, want exactly 3 (unparseable 2xx must retry up to MaxRetries)", got)
	}
}

// Test 8d: issue #1253 review H1 -- a single unparseable 2xx body, when the
// NEXT attempt succeeds, settles as a normal success. Proves the sanitize
// ok=false branch composes correctly with retry rather than poisoning a
// later good attempt.
func TestDispatcher_UnparseableUpstreamBody_ThenSuccessOnRetry(t *testing.T) {
	var calls atomic.Int32
	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			n := calls.Add(1)
			if n < 2 {
				return json.RawMessage(`not valid json`), nil, 200, nil
			}
			return json.RawMessage(`{"id":"gen-x","model":"route-x","choices":[]}`), &Usage{TotalTokens: 5}, 200, nil
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 3, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "customer-alias-1", ""),
		Alias:        "customer-alias-1",
		LiteLLMModel: "openrouter/deepseek/deepseek-v4-pro-0813",
	})
	if res.Error != nil {
		t.Fatalf("expected eventual success, got error %+v", res.Error)
	}
	if res.Output == nil {
		t.Fatalf("expected a successful output")
	}
	if strings.Contains(string(res.Output.Response.Body), "gen-x") || strings.Contains(string(res.Output.Response.Body), "route-x") {
		t.Fatalf("recovered success still leaked upstream id/model: %s", res.Output.Response.Body)
	}
}

// Test 7b: SanitizeMessage on direct strings.
func TestSanitizeMessage(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"openrouter upstream rejected", "upstream upstream rejected"},
		{"GROQ HTTP 502", "upstream HTTP 502"},
		{"LiteLLM gateway error", "upstream gateway error"},
		{"clean message", "clean message"},
		{"", "upstream error"},
	}
	for _, c := range cases {
		got := SanitizeMessage(c.in)
		if got != c.want {
			t.Fatalf("SanitizeMessage(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestConfig_Validate_ClampAndDefaults(t *testing.T) {
	c := Config{Concurrency: 0, MaxRetries: 0, LineTimeout: 0}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Concurrency != ConcurrencyDefault {
		t.Fatalf("concurrency=%d", c.Concurrency)
	}
	if c.MaxRetries != MaxRetriesDefault {
		t.Fatalf("retries=%d", c.MaxRetries)
	}
	if c.LineTimeout != LineTimeoutDefault {
		t.Fatalf("timeout=%s", c.LineTimeout)
	}
	if c.Kind != KindAuto {
		t.Fatalf("kind=%q", c.Kind)
	}

	c = Config{Concurrency: 999, MaxRetries: 999, LineTimeout: 24 * time.Hour}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Concurrency != ConcurrencyCeiling {
		t.Fatalf("concurrency clamp failed: %d", c.Concurrency)
	}
	if c.MaxRetries != MaxRetriesCeiling {
		t.Fatalf("retries clamp failed: %d", c.MaxRetries)
	}
	if c.LineTimeout != LineTimeoutCeiling {
		t.Fatalf("timeout clamp failed: %s", c.LineTimeout)
	}

	c = Config{Kind: "weird"}
	if err := c.Validate(); err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

func containsCI(haystack, needle string) bool {
	if needle == "" {
		return false
	}
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			c1 := haystack[i+j]
			c2 := needle[j]
			if 'A' <= c1 && c1 <= 'Z' {
				c1 += 'a' - 'A'
			}
			if 'A' <= c2 && c2 <= 'Z' {
				c2 += 'a' - 'A'
			}
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// billingSpy records every conversion of usage into credits, so a test can
// assert not merely that a failed line settled at zero but that the dispatcher
// never got as far as pricing it.
type billingSpy struct{ calls atomic.Int32 }

func (b *billingSpy) Credits(usage *Usage) int64 {
	b.calls.Add(1)
	return 999_999
}

// TestDispatcher_ErrorCarryingFrameIsNeverStoredOrBilled is the money-path
// guard for PR #1303's change to packages/sanitize.
//
// That PR gives the SSE relays a way to turn an upstream error frame into a
// gateway-owned error the customer can render, instead of dropping it. The
// dangerous way to have built that would have been to change
// VariablePriceFrame itself to return (replacement, true), because this
// caller reads ok == false as "do not store this line": a replacement with
// ok == true would land a synthetic Hive error object in output.jsonl as a
// completed 2xx result AND charge d.credits.Credits(usage) for it at
// dispatcher.go's success branch. The replacement is therefore a separate,
// opt-in call (sanitize.ReplaceErrorFrame) that only the relays make.
//
// This test fails if that contract is ever relaxed back. It goes red on all
// three of: an Output line appearing, a non-zero charge, and the pricing
// function being called at all.
func TestDispatcher_ErrorCarryingFrameIsNeverStoredOrBilled(t *testing.T) {
	for _, body := range []string{
		// LiteLLM's mid-stream shape, delivered on a 200.
		`{"error":{"message":"litellm.APIConnectionError: OpenAIException - You exceeded your current quota","code":"500"}}`,
		// OpenRouter's out-of-credit body, brand and top-up URL included.
		`{"id":"gen-1","error":{"message":"Insufficient credits. Add more using https://openrouter.ai/settings/credits","code":402},"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}`,
	} {
		t.Run(body[:40], func(t *testing.T) {
			spy := &billingSpy{}
			infer := &fakeInference{
				handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
					return json.RawMessage(body), &Usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12}, 200, nil
				},
			}
			disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 2, LineTimeout: 5 * time.Second}, infer, spy)
			if err != nil {
				t.Fatalf("new dispatcher: %v", err)
			}
			res := disp.Dispatch(context.Background(), InputLine{
				CustomID:     "x",
				Method:       "POST",
				URL:          "/v1/chat/completions",
				Body:         mustBody(t, "customer-alias-1", ""),
				Alias:        "customer-alias-1",
				LiteLLMModel: "openrouter/deepseek/deepseek-v4-pro-0813",
			})
			if res.Output != nil {
				t.Fatalf("error-carrying 200 body stored as a completed line: %s", res.Output.Response.Body)
			}
			if res.Error == nil {
				t.Fatalf("expected a failed line, got neither output nor error")
			}
			if res.ConsumedCredits != 0 {
				t.Fatalf("credits=%d want 0: an upstream error line must never be charged", res.ConsumedCredits)
			}
			if got := spy.calls.Load(); got != 0 {
				t.Fatalf("credit policy consulted %d times for a refused line, want 0", got)
			}
			// The customer-facing error message carries no upstream text.
			for _, leak := range []string{"openrouter", "quota", "credits", "litellm"} {
				if strings.Contains(strings.ToLower(res.Error.Error.Message), leak) {
					t.Fatalf("upstream text %q reached the batch error line: %q", leak, res.Error.Error.Message)
				}
			}
		})
	}
}

// TestDispatcher_DeclaredButEmptyErrorIsStoredNormally is the other side of
// the same change: relaxing the empty-error cases must actually take effect
// here, or a provider that declares "error":null on every chunk has every
// batch line refused and retried to exhaustion for no reason.
func TestDispatcher_DeclaredButEmptyErrorIsStoredNormally(t *testing.T) {
	infer := &fakeInference{
		handler: func(ctx context.Context, _ int, _ string, _ json.RawMessage) (json.RawMessage, *Usage, int, error) {
			return json.RawMessage(`{"id":"gen-1","error":null,"choices":[{"index":0,"message":{"content":"hi"}}],"usage":{"prompt_tokens":9,"completion_tokens":3,"total_tokens":12}}`),
				&Usage{PromptTokens: 9, CompletionTokens: 3, TotalTokens: 12}, 200, nil
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 1, MaxRetries: 2, LineTimeout: 5 * time.Second}, infer, DefaultCreditPolicy{})
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	res := disp.Dispatch(context.Background(), InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         mustBody(t, "customer-alias-1", ""),
		Alias:        "customer-alias-1",
		LiteLLMModel: "openrouter/deepseek/deepseek-v4-pro-0813",
	})
	if res.Error != nil {
		t.Fatalf("healthy line with a declared-but-null error was refused: %+v", res.Error)
	}
	if res.Output == nil {
		t.Fatalf("expected a stored output line")
	}
	if strings.Contains(string(res.Output.Response.Body), `"error"`) {
		t.Fatalf("the error key must not reach the customer's output.jsonl: %s", res.Output.Response.Body)
	}
}
