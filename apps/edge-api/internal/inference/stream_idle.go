package inference

import (
	"io"
	"log"
	"sync"
	"time"
)

// Streaming stall bounds (issue #928 defect 3).
//
// Removing the LiteLLM client's total timeout from streaming dispatch is what
// stops a healthy long answer being cut at two minutes. Something else then has
// to stop a wedged provider holding a customer's credits open, and one bound is
// not enough, because a stream can be stuck in two different ways:
//
//  1. NO BYTES AT ALL. The connection is open and nothing is written. Bounded by
//     idleTimeoutReader below, installed on the response body at DISPATCH, not
//     at the relay. Dispatch is the right place because the relay is not the
//     first thing to read that body: dispatchWithRetry's peekBody classifies a
//     404 or an allowance wall from it, and the relay's own non-2xx branch reads
//     it with io.ReadAll. Both of those sit before the relay loop, and a 500
//     followed by silence hung there forever when the watchdog was installed at
//     the relay instead (review finding).
//
//  2. BYTES BUT NO DATA. The provider sends SSE keepalive comments and nothing
//     else. Every one of them resets a byte-level watchdog, so a provider
//     pinging every thirty seconds would hold the hold until the reservation
//     reaper reclaimed it an hour later -- and the reaper would then release a
//     hold whose request is still live, leaving a served turn with no
//     reservation behind it. Bounded by the relay's own data deadline instead:
//     see streamDataDeadlineExceeded, which each relay checks per line against
//     the last frame that actually carried something.
//
// The two are deliberately separate mechanisms rather than one clever one. The
// byte watchdog cannot classify what it reads, and the relay cannot notice a
// silence that never wakes it up.

// streamIdleTimeout is the budget both bounds use: how long a stream may go
// without a byte (bound 1) or without a data-bearing frame (bound 2) before it
// is given up on.
//
// A var, not a const, for the same reason accountingTimeout is one: the
// regression tests need to shorten it rather than sleep for two minutes.
var streamIdleTimeout = 120 * time.Second

// streamDataDeadlineExceeded reports whether the relay has gone longer than
// streamIdleTimeout without a frame that carried anything.
//
// Checked per line rather than on a timer, which is what makes it lazy and
// correct at once: the only way a keepalive-only stream can starve the customer
// is by sending keepalives, and every one of those wakes the relay loop. A
// stream that has genuinely stopped writing never reaches this test and is
// bound 1's problem instead.
func streamDataDeadlineExceeded(lastData time.Time) bool {
	return time.Since(lastData) > streamIdleTimeout
}

// chunkCarriesData reports whether a relayed chunk carried anything at all:
// assistant text, a refusal, a tool call, a finish reason, or a usage block.
//
// It is what feeds the data deadline, and it is deliberately narrower than "the
// frame parsed". A provider that emits well-formed but empty chunks as its
// keepalive is the same starvation as one that emits SSE comments, and a test
// that only excluded comments would miss it.
func chunkCarriesData(chunk ChatCompletionChunk) bool {
	if chunk.Usage != nil {
		return true
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			return true
		}
		if choice.Delta.Refusal != nil && *choice.Delta.Refusal != "" {
			return true
		}
		if rawFieldPresent(choice.Delta.ToolCalls) || rawFieldPresent(choice.Delta.FunctionCall) {
			return true
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			return true
		}
	}
	return false
}

// idleTimeoutReader wraps an upstream response body and closes it when no byte
// has been read for timeout. It is bound 1 above.
//
// The close is the mechanism, deliberately: a blocked Read on an http response
// body returns an error as soon as the body is closed, so whichever caller was
// reading gets an ordinary read failure. For the relay that means its EXISTING
// scanner.Err() abort path (issue #1255) handles the ending -- provider-blind
// error frame, operator log line, and the deferred settleStream that releases or
// captures the hold. For peekBody and the non-2xx read it means an ordinary
// short read rather than a hang. Nothing had to be taught how to end a stream.
//
// The alternative shapes were both worse. A read deadline set on the connection
// via a custom dialer would also prune healthy pooled connections whose
// background read sits idle between requests, and it behaves differently under
// HTTP/2 where one connection carries several streams. A context with a deadline
// cannot express "idle", only "total", which is the bound being removed.
type idleTimeoutReader struct {
	rc      io.ReadCloser
	timeout time.Duration
	label   string

	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
	tripped bool
}

// newIdleTimeoutReader starts the watchdog. The returned value is an
// io.ReadCloser so it can stand in for the response body itself: closing it
// stops the watchdog and closes what it wrapped, which means every existing
// `defer resp.Body.Close()` and drainAndClose call already disarms it and no
// caller needs to learn about it.
func newIdleTimeoutReader(rc io.ReadCloser, timeout time.Duration, label string) *idleTimeoutReader {
	r := &idleTimeoutReader{rc: rc, timeout: timeout, label: label}
	r.timer = time.AfterFunc(timeout, r.trip)
	return r
}

// Read resets the watchdog and then blocks on the underlying body. The reset
// happens BEFORE the read, not after it: a read that blocks for the whole window
// is exactly the case being bounded, so a reset that only ran on the way out
// would never run at all on a stalled stream.
func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if !r.stopped && !r.tripped {
		r.timer.Reset(r.timeout)
	}
	r.mu.Unlock()
	return r.rc.Read(p)
}

// Close stops the watchdog and closes the wrapped body.
func (r *idleTimeoutReader) Close() error {
	r.stop()
	return r.rc.Close()
}

func (r *idleTimeoutReader) trip() {
	r.mu.Lock()
	if r.stopped || r.tripped {
		r.mu.Unlock()
		return
	}
	r.tripped = true
	r.mu.Unlock()
	// Operator log only. The caller's side of this is the relay's own
	// provider-blind error frame, which says nothing about which upstream went
	// quiet or for how long.
	log.Printf("inference: upstream sent no bytes for %s, closing the body %s: a stalled provider must not hold a customer's credits open indefinitely (#928)",
		r.timeout, r.label)
	_ = r.rc.Close()
}

// stop ends the watch. Safe to call more than once, and after it has fired.
func (r *idleTimeoutReader) stop() {
	r.mu.Lock()
	r.stopped = true
	timer := r.timer
	r.mu.Unlock()
	timer.Stop()
}

// trippedIdle reports whether the watchdog closed the body, so a test can tell a
// stall apart from any other read failure. The relays need no such distinction,
// because every read failure ends the same way.
func (r *idleTimeoutReader) trippedIdle() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tripped
}
