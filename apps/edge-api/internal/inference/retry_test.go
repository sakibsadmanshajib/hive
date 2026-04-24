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
