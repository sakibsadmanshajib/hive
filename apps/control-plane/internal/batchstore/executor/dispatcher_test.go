package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
				CustomID: fmt.Sprintf("req-%d", i),
				Method:   "POST",
				URL:      "/v1/chat/completions",
				Body:     mustBody(t, "alias-1", fmt.Sprintf("p%d", i)),
				Alias:    "alias-1",
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
		CustomID: "x",
		Method:   "POST",
		URL:      "/v1/chat/completions",
		Body:     mustBody(t, "alias-1", ""),
		Alias:    "alias-1",
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
		CustomID: "x",
		Method:   "POST",
		URL:      "/v1/chat/completions",
		Body:     mustBody(t, "alias-1", ""),
		Alias:    "alias-1",
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
		CustomID: "x",
		Method:   "POST",
		URL:      "/v1/chat/completions",
		Body:     mustBody(t, "alias-1", ""),
		Alias:    "alias-1",
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
		CustomID: "x",
		Method:   "POST",
		URL:      "/v1/chat/completions",
		Body:     mustBody(t, "alias-1", ""),
		Alias:    "alias-1",
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
