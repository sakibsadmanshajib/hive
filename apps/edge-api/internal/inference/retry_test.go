package inference

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mkResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestDispatchWithRetry_SuccessFirstAttempt(t *testing.T) {
	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return mkResp(200, "ok"), nil
	}
	resp, err := dispatchWithRetry(context.Background(), "m", []byte("{}"), fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestDispatchWithRetry_RetriesOn429ThenSucceeds(t *testing.T) {
	// Speed up the test by shrinking delays just for this run.
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return mkResp(429, `{"error":"rate limited"}`), nil
		}
		return mkResp(200, "ok"), nil
	}
	resp, err := dispatchWithRetry(context.Background(), "m", []byte("{}"), fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("final status = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}

func TestDispatchWithRetry_ExhaustsAttemptsAnd429ReturnsLastResp(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return mkResp(429, "rate limited"), nil
	}
	resp, err := dispatchWithRetry(context.Background(), "m", []byte("{}"), fn)
	if err != nil {
		t.Fatalf("err = %v, want nil (last resp should surface)", err)
	}
	if resp.StatusCode != 429 {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != int32(len(retryDelays)) {
		t.Fatalf("calls = %d, want %d", got, len(retryDelays))
	}
}

func TestDispatchWithRetry_NonRetryableStatusReturnsImmediately(t *testing.T) {
	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return mkResp(400, "bad request"), nil
	}
	resp, err := dispatchWithRetry(context.Background(), "m", []byte("{}"), fn)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestDispatchWithRetry_TransportErrorExhausts(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	var calls int32
	boom := errors.New("boom")
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, boom
	}
	_, err := dispatchWithRetry(context.Background(), "m", []byte("{}"), fn)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if got := atomic.LoadInt32(&calls); got != int32(len(retryDelays)) {
		t.Fatalf("calls = %d, want %d", got, len(retryDelays))
	}
}

func TestDispatchWithRetry_ContextCancelStopsRetries(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		if atomic.LoadInt32(&calls) == 1 {
			cancel()
		}
		return mkResp(429, "rl"), nil
	}
	_, err := dispatchWithRetry(ctx, "m", []byte("{}"), fn)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	// One initial attempt; the retry backoff observes the cancel.
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1 (cancel before retry)", got)
	}
}

// routerExhaustionBody is the verbatim upstream envelope the edge received from
// LiteLLM on CI run 32830060362, when one member of the four-deployment
// route-free-pool group answered 404 and the router abandoned the request
// instead of trying the other three. Copied from the run's compose-logs
// artifact rather than paraphrased, because the whole fix keys off this text:
// if LiteLLM rewords it in a future image bump, these tests are what notices.
const routerExhaustionBody = `{"error":{"message":"litellm.NotFoundError: NotFoundError: OpenAIException - Error code: 404No fallback model group found for original model_group=route-free-pool. Fallbacks=[{'route-openrouter-embedding': ['route-openrouter-embedding-fallback']}]. Received Model Group=route-free-pool\nAvailable Model Group Fallbacks=None","type":null,"param":null,"code":"404"}}`

// TestDispatchWithRetry_FailsOverWhenOnePoolMemberIsGone is the regression
// guard for issue #1064.
//
// The free pool's entire product claim is that one exhausted or retired key
// fails over to the others. LiteLLM does not deliver that for a 404: it aborts
// on the first member that answers one. The pool's existing coverage
// (free_pool_router_test.go) asserts the four rows share a litellm_model_name,
// which stayed true the whole time this was broken -- the shape was right and
// the behaviour was wrong. This asserts the behaviour instead.
func TestDispatchWithRetry_FailsOverWhenOnePoolMemberIsGone(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		// Attempt 1 lands on the dead member. LiteLLM cools it down before
		// answering, so attempt 2 reaches a live one.
		if atomic.AddInt32(&calls, 1) == 1 {
			return mkResp(404, routerExhaustionBody), nil
		}
		return mkResp(200, `{"object":"chat.completion"}`), nil
	}

	resp, err := dispatchWithRetry(context.Background(), "route-free-pool", []byte("{}"), fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200: a pooled alias must survive one dead member", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

// TestDispatchWithRetry_GenuineModelNotFoundIsNotRetried keeps the blast radius
// of the fix above at exactly the router-exhaustion case. A customer naming a
// model that does not exist must still get a fast 404, not four attempts and
// three seconds of backoff.
func TestDispatchWithRetry_GenuineModelNotFoundIsNotRetried(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	const body = `{"error":{"message":"The model 'nonexistent-model-12345' does not exist","code":"404"}}`
	var calls int32
	fn := func(ctx context.Context, model string, body2 []byte) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return mkResp(404, body), nil
	}

	resp, err := dispatchWithRetry(context.Background(), "m", []byte("{}"), fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1: a real model-not-found must not be retried", got)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("body = %q, want it returned intact", got)
	}
}

// TestDispatchWithRetry_DeadPoolReturnsTheUpstream404Intact covers the case the
// retry cannot rescue: every member is gone. The client must still receive the
// real upstream status and a COMPLETE body, because classifying that body is
// how the edge decides what to tell the customer
// (errors.sanitizeProviderBlindMessage) and how CI names the failure. A body
// left half-consumed by the peek above would truncate both.
func TestDispatchWithRetry_DeadPoolReturnsTheUpstream404Intact(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	var calls int32
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return mkResp(404, routerExhaustionBody), nil
	}

	resp, err := dispatchWithRetry(context.Background(), "route-free-pool", []byte("{}"), fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want the real upstream 404", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&calls); got != int32(len(retryDelays)) {
		t.Fatalf("calls = %d, want %d", got, len(retryDelays))
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(got) != routerExhaustionBody {
		t.Fatalf("body = %q, want the upstream envelope returned whole", got)
	}
}

// TestIsRouterExhaustion404LeavesTheBodyReadable pins the splice itself for
// bodies larger than the peek window, which is the case where a naive
// implementation silently drops everything past maxRetryPeekBytes.
func TestIsRouterExhaustion404LeavesTheBodyReadable(t *testing.T) {
	big := strings.Repeat("x", maxRetryPeekBytes*2)
	resp := mkResp(404, big)

	if isRouterExhaustion404(resp) {
		t.Fatal("a body with no marker must not be classified as router exhaustion")
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if len(got) != len(big) {
		t.Fatalf("body length = %d, want %d: the peek truncated the response", len(got), len(big))
	}
}


// groqTopKRefusal is the real body LiteLLM returned on 2026-08-28 when an
// Anthropic Messages request carrying top_k drew the free pool's Groq member,
// trimmed to the part that matters. The customer saw "hive-free is not
// available." for a field the pool's other members would have served.
const groqTopKRefusal = `{"error":{"message":"litellm.BadRequestError: GroqException - {\"error\":{\"message\":\"property 'top_k' is unsupported\",\"type\":\"invalid_request_error\"}}","type":null,"param":null,"code":"400"}}`

// TestDispatchWithRetry_StripsRefusedPassthroughField is the behavioural
// assertion for the whole point of the strip: a request naming a passthrough
// field must survive an upstream that refuses it, and the retry must actually
// go out WITHOUT that field.
func TestDispatchWithRetry_StripsRefusedPassthroughField(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	calls := 0
	secondBody := ""
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		calls++
		if calls == 1 {
			return mkResp(400, groqTopKRefusal), nil
		}
		secondBody = string(body)
		return mkResp(200, `{"object":"chat.completion"}`), nil
	}

	resp, err := dispatchWithRetry(context.Background(), "route-free-pool",
		[]byte(`{"model":"hive-free","top_k":40,"temperature":0.5}`), fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if strings.Contains(secondBody, "top_k") {
		t.Errorf("retry body still carries top_k: %s", secondBody)
	}
	if !strings.Contains(secondBody, `"temperature":0.5`) {
		t.Errorf("retry body lost temperature: %s", secondBody)
	}
	if !strings.Contains(secondBody, `"model":"hive-free"`) {
		t.Errorf("retry body lost model: %s", secondBody)
	}
}

// TestDispatchWithRetry_DoesNotStripCallerOwnedFields keeps the blast radius
// tight. A 400 blaming a field that is part of the OpenAI surface proper is
// the answer to what the caller asked for; silently dropping that field would
// change the request and return a 200 for something nobody asked for.
func TestDispatchWithRetry_DoesNotStripCallerOwnedFields(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	calls := 0
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		calls++
		return mkResp(400, `{"error":{"message":"property 'temperature' is unsupported"}}`), nil
	}

	resp, err := dispatchWithRetry(context.Background(), "route-free-pool",
		[]byte(`{"model":"hive-free","temperature":0.5}`), fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1: a caller-owned field must not trigger a retry", calls)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(got), "temperature") {
		t.Errorf("body was consumed by the classifier: %q", got)
	}
}

// TestDispatchWithRetry_StripsEachFieldAtMostOnce bounds the loop. An upstream
// that keeps naming the same field after it is gone must not spend the whole
// retry ladder on it, and must still return a real response.
func TestDispatchWithRetry_StripsEachFieldAtMostOnce(t *testing.T) {
	origDelays := retryDelays
	retryDelays = []time.Duration{0, 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	t.Cleanup(func() { retryDelays = origDelays })

	calls := 0
	fn := func(ctx context.Context, model string, body []byte) (*http.Response, error) {
		calls++
		return mkResp(400, groqTopKRefusal), nil
	}

	resp, err := dispatchWithRetry(context.Background(), "route-free-pool",
		[]byte(`{"model":"hive-free","top_k":40}`), fn)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if resp == nil {
		t.Fatal("resp = nil: the caller must always get a real response")
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2: one strip, then the refusal stands", calls)
	}
}

func TestRefusedPassthroughField(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"groq wording", 400, groqTopKRefusal, "top_k"},
		{"openai wording", 400, `{"error":{"message":"Unrecognized request argument supplied: top_k"}}`, "top_k"},
		{"gemini wording", 400, `{"error":{"message":"Invalid JSON payload received. Unknown name \"top_k\""}}`, "top_k"},
		{"anthropic wording", 400, `{"error":{"message":"thinking: Extra inputs are not permitted"}}`, "thinking"},
		{"caller-owned field", 400, `{"error":{"message":"property 'temperature' is unsupported"}}`, ""},
		{"unrelated 400", 400, `{"error":{"message":"messages: at least one message is required"}}`, ""},
		{"not a 400", 422, groqTopKRefusal, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := refusedPassthroughField(mkResp(tc.status, tc.body)); got != tc.want {
				t.Errorf("refusedPassthroughField = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStripTopLevelField(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		field   string
		wantOK  bool
		wantOut string
	}{
		{"present", `{"a":1,"top_k":40}`, "top_k", true, `{"a":1}`},
		{"absent", `{"a":1}`, "top_k", false, `{"a":1}`},
		{"not json", `"not json"`, "top_k", false, `"not json"`},
		{"nested only", `{"a":{"top_k":40}}`, "top_k", false, `{"a":{"top_k":40}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, ok := stripTopLevelField([]byte(tc.body), tc.field)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if string(out) != tc.wantOut {
				t.Errorf("out = %s, want %s", out, tc.wantOut)
			}
		})
	}
}
