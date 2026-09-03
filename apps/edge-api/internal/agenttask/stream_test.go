package agenttask

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// streamingClient is a TaskClient whose StreamEvents hands back a pipe the
// test writes into, so a frame can be produced after the response has already
// started. A fake that returned a finished buffer could not tell a relay that
// streams apart from one that buffers, which is the only thing under test.
type streamingClient struct {
	*fakeClient
	pipe   io.ReadCloser
	err    error
	opened chan struct{}
}

func (s *streamingClient) StreamEvents(_ context.Context, _, _, _ uuid.UUID, _ int64) (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	close(s.opened)
	return s.pipe, nil
}

// serveStream runs the handler on a real listener. A ResponseRecorder buffers
// by construction, so it cannot distinguish the fix from the defect.
func serveStream(t *testing.T, h *Handler, tenantID uuid.UUID) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.routeTaskByID(w, r.WithContext(userCtx(tenantID)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A frame written by control-plane after the response has begun reaches the
// customer without a second request. This is the relay's whole job, and a
// buffering relay passes every other assertion about this endpoint.
func TestHandleEventStream_RelaysAFrameWrittenAfterTheResponseBegan(t *testing.T) {
	reader, writer := io.Pipe()
	// Closed before the test server's own cleanup runs, which is what makes a
	// failing run report the failure instead of hanging: httptest.Server.Close
	// waits for outstanding handlers, and the handler is blocked reading this
	// pipe until somebody closes the far end.
	defer writer.Close()
	client := &streamingClient{fakeClient: newFakeClient(), pipe: reader, opened: make(chan struct{})}
	srv := serveStream(t, NewHandler(client), uuid.New())

	resp, err := http.Get(srv.URL + "/v1/agent/tasks/" + uuid.New().String() + "/events/stream")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type %q, want text/event-stream", ct)
	}

	<-client.opened
	go func() {
		_, _ = io.WriteString(writer, "event: step\ndata: {\"seq\":1}\n\n")
	}()

	line := make(chan string, 1)
	go func() {
		br := bufio.NewReader(resp.Body)
		for {
			text, readErr := br.ReadString('\n')
			if strings.HasPrefix(text, "data: ") {
				line <- strings.TrimSpace(text)
				return
			}
			if readErr != nil {
				return
			}
		}
	}()

	select {
	case got := <-line:
		if !strings.Contains(got, `"seq":1`) {
			t.Errorf("relayed %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Error("the frame never reached the customer, so the relay is buffering rather than streaming")
	}
}

// A refusal from control-plane is still an HTTP status. Once the 200 and the
// event-stream header are written there is nothing left to refuse with, and
// the browser would render the refusal as a run that did nothing.
func TestHandleEventStream_RefusesBeforeTheStreamOpens(t *testing.T) {
	client := &streamingClient{fakeClient: newFakeClient(), err: ErrNotFound, opened: make(chan struct{})}
	srv := serveStream(t, NewHandler(client), uuid.New())

	resp, err := http.Get(srv.URL + "/v1/agent/tasks/" + uuid.New().String() + "/events/stream")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("a refusal was sent as an event stream (Content-Type %q)", ct)
	}
}

// A bad cursor is refused rather than read as zero, matching the cursor read
// beside it. Silently reading it as zero would replay a whole run into a
// transcript that already holds it.
func TestHandleEventStream_RefusesABadCursor(t *testing.T) {
	client := &streamingClient{fakeClient: newFakeClient(), opened: make(chan struct{})}
	srv := serveStream(t, NewHandler(client), uuid.New())

	resp, err := http.Get(srv.URL + "/v1/agent/tasks/" + uuid.New().String() + "/events/stream?after_seq=-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

// The route exists. extractTaskPath refused every three-segment path before
// this change, so without the admission the stream is a 400 no client can
// reach and every test above would be testing a handler nothing routes to.
func TestExtractTaskPath_AdmitsTheStreamSuffix(t *testing.T) {
	id := uuid.New()
	got, suffix, ok := extractTaskPath("/v1/agent/tasks/" + id.String() + "/events/stream")
	if !ok || got != id || suffix != "events/stream" {
		t.Fatalf("extractTaskPath = %v, %q, %v", got, suffix, ok)
	}
	// And nothing else three segments long.
	if _, _, ok := extractTaskPath("/v1/agent/tasks/" + id.String() + "/events/anything"); ok {
		t.Error("an unserved three-segment path was admitted")
	}
	if _, _, ok := extractTaskPath("/v1/agent/tasks/" + id.String() + "/files/stream"); ok {
		t.Error("an unserved three-segment path was admitted")
	}
}

// A saturated stream ceiling reaches the customer as a 429, not a 500.
//
// The two mean different things and want different responses: a 500 says
// something is broken, and an operator reading it cannot tell breakage from a
// deployment that is simply busy. The front end also treats them differently,
// because only one of them is worth dropping back to the cursor read for.
func TestHandleEventStream_SaturationIsA429NotA500(t *testing.T) {
	client := &streamingClient{
		fakeClient: newFakeClient(),
		err:        ErrTooManyStreams,
		opened:     make(chan struct{}),
	}
	srv := serveStream(t, NewHandler(client), uuid.New())

	resp, err := http.Get(srv.URL + "/v1/agent/tasks/" + uuid.New().String() + "/events/stream")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d, want 429", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// The sentence has to tell the person what to do. "Request failed" is a
	// status code with extra steps.
	if !strings.Contains(string(body), "close a tab") {
		t.Errorf("the refusal does not say what to do about it: %s", body)
	}
}

// control-plane's 429 becomes that sentinel rather than the generic failure,
// which is what keeps the two apart all the way to the customer.
func TestStatusErr_MapsSaturationToItsOwnSentinel(t *testing.T) {
	if err := statusErr(http.StatusTooManyRequests); !errors.Is(err, ErrTooManyStreams) {
		t.Fatalf("statusErr(429) = %v, want ErrTooManyStreams", err)
	}
	if err := statusErr(http.StatusInternalServerError); errors.Is(err, ErrTooManyStreams) {
		t.Fatal("a 500 was mapped to saturation")
	}
}
