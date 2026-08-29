package httpx

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stallingBody reports a read that never delivers the declared bytes.
type stallingBody struct{ reads int }

func (s *stallingBody) Read(p []byte) (int, error) {
	s.reads++
	if s.reads > 1 {
		return 0, io.EOF
	}
	p[0] = 'x'
	return 1, nil
}

func (s *stallingBody) Close() error { return nil }

// failingBody returns a read error that is neither EOF nor an oversize
// refusal, standing in for a client that stalls or drops mid-body.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func (failingBody) Close() error { return nil }

// deadlineWriter records every SetReadDeadline call so a test can assert the
// deadline was armed, and whether it was cleared.
type deadlineWriter struct {
	http.ResponseWriter
	calls []time.Time
}

func (d *deadlineWriter) SetReadDeadline(t time.Time) error {
	d.calls = append(d.calls, t)
	return nil
}

func TestReadBodyRejectsDeclaredOversizeWithoutReading(t *testing.T) {
	body := &stallingBody{}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	r.ContentLength = 11 << 20

	got, err := ReadBody(httptest.NewRecorder(), r, 10<<20)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
	if !TooLarge(err) {
		t.Fatalf("TooLarge(%v) = false, want true so the caller answers 413", err)
	}
	if got != nil {
		t.Fatalf("body = %q, want nil", got)
	}
	if body.reads != 0 {
		t.Fatalf("body was read %d times, want 0: the point of the pre-check is that the body never lands in memory", body.reads)
	}
}

func TestReadBodyReadsBodyWithinLimit(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))

	got, err := ReadBody(httptest.NewRecorder(), r, 10<<20)
	if err != nil {
		t.Fatalf("ReadBody: %v", err)
	}
	if string(got) != `{"model":"m"}` {
		t.Fatalf("body = %q", got)
	}
}

// A body of exactly max bytes is accepted, declared or not. This pins the
// comparison as ContentLength > max rather than >=: an off-by-one that
// started refusing exactly at the limit would reject legitimate traffic the
// documented cap admits, and every oversize test in this package would still
// pass. The limit here is small so the case is exact rather than approximate.
func TestReadBodyAcceptsExactlyTheLimit(t *testing.T) {
	for _, declared := range []int64{10, -1} {
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("a", 10)))
		r.ContentLength = declared

		got, err := ReadBody(httptest.NewRecorder(), r, 10)
		if err != nil {
			t.Fatalf("ContentLength %d: ReadBody: %v", declared, err)
		}
		if len(got) != 10 {
			t.Fatalf("ContentLength %d: len(body) = %d, want 10", declared, len(got))
		}
	}
}

// An unknown length (chunked encoding, ContentLength -1) must not be rejected
// by the pre-check: it is exactly the case the pre-check cannot see, and
// treating -1 as oversize would break every chunked client.
func TestReadBodyAllowsUnknownLength(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("abc"))
	r.ContentLength = -1

	got, err := ReadBody(httptest.NewRecorder(), r, 10<<20)
	if err != nil {
		t.Fatalf("ReadBody: %v", err)
	}
	if string(got) != "abc" {
		t.Fatalf("body = %q", got)
	}
}

// A body longer than the limit with no declared length is REFUSED, not
// truncated. Truncating is what issue #1250 removed from every other body
// read in this server: the truncated bytes fail the caller's json.Unmarshal
// and the client is told its valid body was malformed, with no mention of
// size. http.MaxBytesReader errors instead, and TooLarge routes that to the
// same honest 413 as the declared-oversize case.
func TestReadBodyRefusesUndeclaredOversize(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(strings.Repeat("a", 100)))
	r.ContentLength = -1

	got, err := ReadBody(httptest.NewRecorder(), r, 10)
	if err == nil {
		t.Fatalf("ReadBody returned %d bytes and no error, want an oversize refusal", len(got))
	}
	var maxBytes *http.MaxBytesError
	if !errors.As(err, &maxBytes) {
		t.Fatalf("err = %v, want *http.MaxBytesError", err)
	}
	if !TooLarge(err) {
		t.Fatalf("TooLarge(%v) = false, want true so the caller answers 413 rather than a lying 400", err)
	}
}

func TestReadBodyArmsAndClearsReadDeadline(t *testing.T) {
	w := &deadlineWriter{ResponseWriter: httptest.NewRecorder()}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("abc"))

	if _, err := ReadBody(w, r, 10<<20); err != nil {
		t.Fatalf("ReadBody: %v", err)
	}
	if len(w.calls) != 2 {
		t.Fatalf("SetReadDeadline called %d times, want 2 (arm then clear)", len(w.calls))
	}
	if !w.calls[0].After(time.Now()) {
		t.Fatalf("first call did not arm a future deadline: %v", w.calls[0])
	}
	if !w.calls[1].IsZero() {
		t.Fatalf("second call = %v, want the zero time so the deadline does not survive into an SSE response", w.calls[1])
	}
}

// A FAILED read must leave the deadline armed. net/http reads the unread
// remainder of the body itself after the handler returns (transfer.go's
// (*body).Close drains up to maxPostHandlerReadBytes over a reader that does
// not memoise the earlier failure), so with the deadline cleared and
// ReadTimeout at 0 a sender that declares 300 KiB, dribbles 250 KiB and then
// stalls holds the connection and its goroutine forever: the exact unbounded
// case this file exists to remove. Nothing is streamed on an error path, so
// leaving the deadline armed cannot cut an SSE response.
func TestReadBodyKeepsDeadlineArmedOnReadError(t *testing.T) {
	w := &deadlineWriter{ResponseWriter: httptest.NewRecorder()}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", failingBody{})
	r.ContentLength = -1

	if _, err := ReadBody(w, r, 10<<20); err == nil {
		t.Fatal("ReadBody: want a read error")
	}
	if len(w.calls) != 1 {
		t.Fatalf("SetReadDeadline called %d times, want 1 (armed, never cleared)", len(w.calls))
	}
	if w.calls[0].IsZero() {
		t.Fatal("the deadline was cleared on a failed read, leaving net/http's post-handler body drain unbounded")
	}
}

// The oversize path returns before arming anything, so it must not leave a
// deadline set on the connection either.
func TestReadBodyOversizeLeavesNoDeadline(t *testing.T) {
	w := &deadlineWriter{ResponseWriter: httptest.NewRecorder()}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("abc"))
	r.ContentLength = 11 << 20

	if _, err := ReadBody(w, r, 10<<20); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err = %v, want ErrBodyTooLarge", err)
	}
	if len(w.calls) != 0 {
		t.Fatalf("SetReadDeadline called %d times, want 0", len(w.calls))
	}
}

type wrapper struct{ http.ResponseWriter }

func (w wrapper) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type nilUnwrapper struct{ http.ResponseWriter }

func (nilUnwrapper) Unwrap() http.ResponseWriter { return nil }

// A wrapper that unwraps to nil must not cost us the writer we already had.
// Handing nil to MaxBytesReader is safe (it only type asserts its writer) but
// it would discard one that may well have been the real connection.
func TestBaseWriterStopsAtANilUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	got := baseWriter(nilUnwrapper{rec})
	if got == nil {
		t.Fatal("baseWriter returned nil for a wrapper whose Unwrap returns nil")
	}
	if _, ok := got.(nilUnwrapper); !ok {
		t.Fatalf("baseWriter returned %T, want the last non-nil writer in the chain", got)
	}
}

// http.MaxBytesReader type asserts the writer it is handed and does not walk
// Unwrap, so ReadBody has to hand it the bottom of the chain or it silently
// loses the half of MaxBytesReader that marks the connection
// close-after-reply. Wrapping is what the edge-api middleware chain does to
// every request, so this walk is not hypothetical.
func TestBaseWriterWalksToTheBottomOfTheChain(t *testing.T) {
	rec := httptest.NewRecorder()
	if got := baseWriter(wrapper{wrapper{wrapper{rec}}}); got != http.ResponseWriter(rec) {
		t.Fatalf("baseWriter returned %T, want the wrapped *httptest.ResponseRecorder", got)
	}
	if got := baseWriter(rec); got != http.ResponseWriter(rec) {
		t.Fatalf("baseWriter on an unwrapped writer returned %T, want it unchanged", got)
	}
}
