package inference

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"net/http"
	"strings"
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

// litellmRouterExhaustionMarker is LiteLLM's own wording when its router gives
// up on a model group WITHOUT trying that group's remaining healthy
// deployments. It is the reason a load-balanced pool does not actually fail
// over, and it is why this file retries a status that is otherwise terminal.
//
// The mechanism, read out of the pinned image's source (v1.98.0) rather than
// inferred:
//
//   - litellm/router.py::should_retry_this_error re-raises immediately for
//     NotFoundError, and again for any status where litellm._should_retry() is
//     false. 404 is both. It is called from async_function_with_retries AND
//     from async_function_with_fallbacks, so neither the in-group retry nor the
//     cross-group fallback ever runs.
//   - litellm/types/router.py::RetryPolicy has no NotFoundErrorRetries field,
//     so no configuration lifts this. The only per-class knob this repo sets,
//     RateLimitErrorRetries, cannot reach it.
//   - The request therefore dies on the FIRST deployment that answers 404,
//     with the other members of the group untouched and healthy. That is what
//     took the hive-free alias down in CI run 32830060362 while three of its
//     four pool members were fine.
//
// What makes retrying here correct rather than hopeful: before raising, LiteLLM
// has already put the offending deployment into cooldown.
// router_utils/cooldown_handlers.py::_should_cooldown_deployment returns true
// on the first failure when litellm._should_retry(status) is false, so a 404
// member is excluded from selection immediately, not after allowed_fails. The
// next attempt therefore picks a DIFFERENT member. The whole retry ladder below
// finishes inside 2.9s, well inside the 5s DEFAULT_COOLDOWN_TIME_SECONDS the
// router actually applies, so the dead member cannot be re-picked mid-ladder.
//
// Scope: this matches on the message, not on a bare 404, so a genuine "that
// model does not exist" stays an immediate 404 and costs no retries.
const litellmRouterExhaustionMarker = "no fallback model group found"

// maxRetryPeekBytes bounds how much of a 404 body is read to classify it.
// Upstream error envelopes are small; anything past this is not a marker this
// function would recognise anyway.
const maxRetryPeekBytes = 8 << 10

// isRouterExhaustion404 reports whether resp is LiteLLM's router-exhaustion
// answer rather than a genuine "no such model".
//
// The body is always spliced back together before returning, so the caller sees
// a complete, unread response either way. That matters: this runs on the path
// that hands the upstream error to the customer, and a half-consumed body would
// silently truncate the error they see.
func isRouterExhaustion404(resp *http.Response) bool {
	if resp == nil || resp.StatusCode != http.StatusNotFound || resp.Body == nil {
		return false
	}

	orig := resp.Body
	head, err := io.ReadAll(io.LimitReader(orig, maxRetryPeekBytes))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{Reader: io.MultiReader(bytes.NewReader(head), orig), Closer: orig}
	if err != nil {
		return false
	}

	return strings.Contains(strings.ToLower(string(head)), litellmRouterExhaustionMarker)
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
		//
		// isRouterExhaustion404 is only consulted for statuses the table does
		// not already cover, so the common 429/5xx path never pays to read a
		// body. See its doc comment for why a 404 is worth another attempt at
		// all: for a pooled alias it means "this one member is dead", and the
		// member is already in cooldown, so the next attempt gets a live one.
		if !retryableStatuses[resp.StatusCode] && !isRouterExhaustion404(resp) {
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
