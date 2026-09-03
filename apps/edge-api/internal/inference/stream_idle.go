package inference

import (
	"io"
	"log"
	"sync"
	"time"
)

// streamIdleTimeout is how long a streaming relay waits for the NEXT byte from
// the upstream before it gives up on the stream (issue #928 defect 3).
//
// It is a SILENCE budget, not a total one, and that distinction is the whole
// point. Removing the LiteLLM client's 120 second total timeout from the
// streaming path (see litellm_client.go) is what stops a healthy long answer
// being cut at two minutes; without something else in its place it would also
// leave a hold open for as long as a wedged provider cared to hold the
// connection, which is the unbounded-hold direction D-034 forbids. A silence
// budget bounds the second without touching the first: an answer still being
// written keeps resetting the watchdog however long it runs, and a provider
// that stops writing is finished with inside this window.
//
// A var, not a const, for the same reason accountingTimeout is one: the
// regression test needs to shorten it rather than sleep for two minutes.
var streamIdleTimeout = 120 * time.Second

// idleTimeoutReader wraps an upstream response body and closes it when no byte
// has been read for timeout.
//
// The close is the mechanism, deliberately: a blocked Read on an http response
// body returns an error as soon as the body is closed, so the relay's EXISTING
// scanner.Err() abort path (issue #1255) handles the ending -- provider-blind
// error frame to the caller, operator log line, and the deferred settleStream
// that releases or captures the hold. Nothing new had to be taught how to end a
// stream; a stall now just looks like the read failure it already is.
//
// The alternative shapes were both worse. A read deadline set on the connection
// via a custom dialer would also prune healthy pooled connections whose
// background read sits idle between requests, and it behaves differently under
// HTTP/2 where one connection carries several streams. A context with a
// deadline cannot express "idle", only "total", which is the bound being
// removed.
type idleTimeoutReader struct {
	rc      io.ReadCloser
	timeout time.Duration
	label   string

	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
	tripped bool
}

// newIdleTimeoutReader starts the watchdog. Callers must call stop when the
// relay ends, so a finished stream does not leave a timer armed against a body
// somebody else is about to close.
func newIdleTimeoutReader(rc io.ReadCloser, timeout time.Duration, label string) *idleTimeoutReader {
	r := &idleTimeoutReader{rc: rc, timeout: timeout, label: label}
	r.timer = time.AfterFunc(timeout, r.trip)
	return r
}

// Read resets the watchdog and then blocks on the underlying body. The reset
// happens BEFORE the read, not after it: a read that blocks for the whole
// window is exactly the case being bounded, so a reset that only ran on the way
// out would never run at all on a stalled stream.
func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if !r.stopped && !r.tripped {
		r.timer.Reset(r.timeout)
	}
	r.mu.Unlock()
	return r.rc.Read(p)
}

func (r *idleTimeoutReader) trip() {
	r.mu.Lock()
	if r.stopped || r.tripped {
		r.mu.Unlock()
		return
	}
	r.tripped = true
	r.mu.Unlock()
	// Operator log only. The caller's side of this is the abort branch's
	// provider-blind error frame, which says nothing about which upstream
	// went quiet or for how long.
	log.Printf("inference: upstream stream went silent for %s, closing the body %s: a stalled provider must not hold a customer's credits open indefinitely (#928)",
		r.timeout, r.label)
	_ = r.rc.Close()
}

// stop ends the watch. Safe to call more than once, and safe to call after the
// watchdog has already fired.
func (r *idleTimeoutReader) stop() {
	r.mu.Lock()
	r.stopped = true
	timer := r.timer
	r.mu.Unlock()
	timer.Stop()
}

// trippedIdle reports whether the watchdog closed the body, so a caller can
// tell a stall apart from any other read failure. Used by the tests; the relay
// itself needs no such distinction, because every read failure ends the same
// way.
func (r *idleTimeoutReader) trippedIdle() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tripped
}
