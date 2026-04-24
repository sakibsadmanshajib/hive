package inference

import (
	"context"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// retryableStatuses are upstream response codes where a retry is worth trying:
// 429 (rate limited by upstream) and transient gateway errors.
var retryableStatuses = map[int]bool{
	http.StatusTooManyRequests:     true, // 429
	http.StatusBadGateway:          true, // 502
	http.StatusServiceUnavailable:  true, // 503
	http.StatusGatewayTimeout:      true, // 504
}

// retryDelays is the progressive backoff between attempts.
// First entry is always zero (initial attempt). Subsequent entries are the
// delay before the next attempt.
// Worst case total waiting = 300 + 800 + 1800 = 2.9s (plus jitter up to +30%).
var retryDelays = []time.Duration{
	0,
	300 * time.Millisecond,
	800 * time.Millisecond,
	1800 * time.Millisecond,
}

// dispatchWithRetry wraps a DispatchFunc with bounded retries on 429 and
// transient 5xx. The request body is reused verbatim on each attempt, so
// callers must pass a fully-materialized []byte (not a stream).
//
// Behavior:
//   - Up to len(retryDelays) total attempts (currently 4).
//   - Retries on transport error or response with retryableStatuses.
//   - Returns the final response (even if it is still an error status) or
//     the final transport error.
//   - Respects ctx cancellation — returns ctx.Err() if the context is done.
//   - Properly drains+closes intermediate response bodies to avoid leaking
//     connections from the underlying http.Client.
//
// It is safe to call before any bytes have been written to the client
// response — none of the state needed to retry lives on the response writer.
func dispatchWithRetry(ctx context.Context, litellmModel string, body []byte, dispatch dispatchFunc) (*http.Response, error) {
	var (
		lastResp *http.Response
		lastErr  error
	)

	for i, delay := range retryDelays {
		if delay > 0 {
			wait := delay + jitter(delay)
			select {
			case <-ctx.Done():
				if lastResp != nil {
					drainAndClose(lastResp)
				}
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			// Discard the previous retryable response; we're about to replace it.
			if lastResp != nil {
				drainAndClose(lastResp)
				lastResp = nil
			}
		}

		resp, err := dispatch(ctx, litellmModel, body)
		if err != nil {
			lastErr = err
			// Only retry on transport errors if we have attempts left.
			if i < len(retryDelays)-1 {
				continue
			}
			return nil, err
		}

		// Success or non-retryable status → return immediately.
		if !retryableStatuses[resp.StatusCode] {
			return resp, nil
		}

		// Retryable status. Hold onto it so we can return it if all attempts fail.
		lastResp = resp
		if i == len(retryDelays)-1 {
			return lastResp, nil
		}
	}

	// Unreachable in practice: the loop either returns or retries.
	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

// jitter returns a non-negative jitter up to ~30% of d.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	max := int64(d) * 3 / 10
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(max))
}

// drainAndClose consumes and closes a response body so connection pooling
// can reuse the underlying transport connection.
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
}
