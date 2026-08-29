// Package httpx holds small HTTP request helpers shared by edge-api handlers.
package httpx

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// ErrBodyTooLarge reports a request whose DECLARED Content-Length already
// exceeds the caller's limit, so the body was never read.
//
// This check is defence in depth and nothing more. It is bypassed by any
// client that sends the body with chunked transfer encoding and declares no
// length at all, so it must never be described, documented, or relied on as
// a control: it does not bound what an adversary can send. What it does buy
// is real but narrow -- for a request that does declare an oversize length,
// the high-water mark drops from "whole limit buffered in memory, then
// rejected" to approximately zero. The bound that holds for an undeclared
// body is http.MaxBytesReader below. The control that actually bounds an
// unauthenticated request is credential validation before the read, which
// five routes still do not have (issue #1299).
var ErrBodyTooLarge = errors.New("httpx: declared request body exceeds limit")

// BodyReadTimeout bounds how long one request-body read may take, start to
// finish. It is not an idle timeout: the whole body has to arrive inside it.
//
// 60s is sized off the largest body these callers accept (10 MiB), which
// needs about 1.4 Mbit/s of sustained client uplink to make the deadline --
// well under what any real API client has, and generous enough that a slow
// mobile connection uploading a large-context chat request is not cut. What
// it removes is the unbounded case: before this, a sender could open a
// connection, declare a body, dribble one byte, and hold the handler's
// buffer for as long as it liked at no cost to itself.
const BodyReadTimeout = 60 * time.Second

// TooLarge reports whether err is either oversize-body refusal ReadBody
// produces: the declared-length pre-check, or http.MaxBytesReader tripping
// mid-read on a body that declared no length. Callers answer 413 for both and
// 400 for anything else, so they never have to know which one fired.
func TooLarge(err error) bool {
	var maxBytes *http.MaxBytesError
	return errors.Is(err, ErrBodyTooLarge) || errors.As(err, &maxBytes)
}

// ReadBody reads r.Body under a bounded read deadline, capped at max by
// http.MaxBytesReader, which errors rather than silently truncating.
//
// The deadline is armed here and cleared before returning rather than set
// server-wide as http.Server.ReadTimeout, for two reasons. A server-wide
// value would cut the legitimate slow multipart upload to /v1/files that
// ReadTimeout: 0 exists to permit (see cmd/server/main.go). And it would
// stay armed for the rest of the response, where net/http's background read
// treats an expired read deadline as a connection error and cancels the
// request context, which would kill every SSE stream this gateway serves.
//
// The deadline is cleared ONLY when the read succeeded. On a failed read the
// handler answers an error and never streams, so there is no stream left to
// protect, and clearing would reintroduce the very unbounded case this is
// here to remove: net/http's own post-handler drain (transfer.go's
// (*body).Close) reads up to maxPostHandlerReadBytes from the connection
// after the handler returns, over a reader that does not memoise the earlier
// failure, so with ReadTimeout at 0 and the deadline cleared, a sender that
// declares 300 KiB, dribbles 250 KiB and then stalls holds the connection and
// its goroutine forever. Leaving the deadline armed bounds that drain.
//
// Deadline support depends on the ResponseWriter chain implementing
// Unwrap() http.ResponseWriter down to the real one. When it does not
// (httptest.ResponseRecorder, for instance) the read proceeds without a
// deadline rather than failing. TestBodyReadDeadlineReachesConnection in
// cmd/server is the guard that the shipped middleware chain does support it.
func ReadBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, error) {
	if r.ContentLength > max {
		return nil, ErrBodyTooLarge
	}
	r.Body = http.MaxBytesReader(baseWriter(w), r.Body, max)
	rc := http.NewResponseController(w)
	armed := rc.SetReadDeadline(time.Now().Add(BodyReadTimeout)) == nil
	body, err := io.ReadAll(r.Body)
	if armed && err == nil {
		_ = rc.SetReadDeadline(time.Time{})
	}
	return body, err
}

// baseWriter walks the Unwrap() http.ResponseWriter chain to the writer at
// the bottom, which for a served request is net/http's own *response.
//
// http.MaxBytesReader needs that one specifically. Its over-limit path type
// asserts the ResponseWriter it was handed against an interface holding an
// unexported net/http method, and does NOT walk Unwrap, so handing it any of
// this server's middleware wrappers silently skips the half of MaxBytesReader
// that marks the connection close-after-reply and suppresses net/http's
// post-handler body drain. The read cap still applies; what goes missing is
// the connection handling around it. http.ResponseController does this same
// walk internally but exposes no way to reach the writer, hence the loop.
//
// A wrapper whose Unwrap returns nil stops the walk and yields the last
// non-nil writer, rather than nil: handing MaxBytesReader nil is safe (it
// only ever type asserts its writer, never dereferences it) but it would
// throw away a writer that might still have been the real one.
func baseWriter(w http.ResponseWriter) http.ResponseWriter {
	for {
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return w
		}
		next := u.Unwrap()
		if next == nil {
			return w
		}
		w = next
	}
}
