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
