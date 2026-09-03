package agenttask_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
)

// frame is one decoded server-sent event: its name and its data line.
type frame struct {
	event string
	data  string
}

// readFrames reads SSE frames off an open response body until it has n of
// them or the deadline passes. Heartbeat comment lines are skipped, which is
// what makes them heartbeats rather than events.
//
// It reads line by line rather than waiting for the body to end, because
// "arrives without the client asking again" is the property under test and a
// reader that waits for EOF cannot tell it from "arrives all at once at the
// end", which is the defect.
func readFrames(t *testing.T, body *bufio.Reader, n int, within time.Duration) []frame {
	t.Helper()
	// out is written by the reader goroutine and read by both branches of the
	// select below, including the timeout branch, which runs while that
	// goroutine is still going. Guarded rather than raced: a harness that only
	// fails under -race is a worse report of a real defect than the defect.
	var mu sync.Mutex
	out := make([]frame, 0, n)
	snapshot := func() []frame {
		mu.Lock()
		defer mu.Unlock()
		return append([]frame(nil), out...)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(out)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var current frame
		for count() < n {
			line, err := body.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, ":"):
				// Heartbeat.
			case strings.HasPrefix(line, "event: "):
				current.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				current.data = strings.TrimPrefix(line, "data: ")
			case line == "":
				if current.event != "" {
					mu.Lock()
					out = append(out, current)
					mu.Unlock()
					current = frame{}
				}
			}
		}
	}()
	select {
	case <-done:
		// The body ended. Fewer frames than asked for is a real failure and
		// has to be reported as one: reading the partial list would turn a
		// stream that hung up early into an index panic, which is a worse
		// report of the same defect.
		got := snapshot()
		if len(got) < n {
			t.Fatalf("the stream ended after %d of %d frames: %v", len(got), n, got)
		}
		return got
	case <-time.After(within):
		got := snapshot()
		t.Fatalf("timed out after %s with %d of %d frames: %v", within, len(got), n, got)
		return nil
	}
}

// newStreamServer stands the internal mux up on a real listener, because a
// ResponseRecorder buffers: only a real connection can show that a frame
// reached the client before the handler returned.
func newStreamServer(t *testing.T, repo agenttask.Repository) *httptest.Server {
	t.Helper()
	svc := agenttask.NewService(repo, &fakeEngine{sessionRef: "session-stream"}, agenttask.WithTaskCredentials(newFakeCredentials()))
	h := agenttask.NewHandler(svc).WithStreamTick(5 * time.Millisecond)
	srv := httptest.NewServer(h.InternalMux())
	t.Cleanup(srv.Close)
	return srv
}

func streamURL(srv *httptest.Server, task agenttask.Task) string {
	return srv.URL + "/internal/agent-tasks/" + task.TenantID.String() + "/" +
		task.UserID.String() + "/" + task.ID.String() + "/events/stream"
}

func appendStep(t *testing.T, repo *fakeRepository, task agenttask.Task, id, preview string) {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"text_preview": preview})
	err := repo.AppendEvents(context.Background(), task, []agenttask.TaskEvent{{
		SourceEventID: id,
		Kind:          agenttask.EventToolCall,
		Payload:       payload,
	}})
	if err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
}

// A step written while the connection is open reaches the client on that same
// connection. This is the acceptance criterion #1622 names and the one PR
// #1709 explicitly did not deliver: the feed there is a cursor the browser
// re-asks every three seconds, so a step could only arrive when the client
// went looking for it.
func TestHandler_EventStream_DeliversAStepWrittenWhileTheClientIsListening(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-stream")
	srv := newStreamServer(t, repo)

	resp, err := http.Get(streamURL(srv, task))
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

	reader := bufio.NewReader(resp.Body)
	// The opening frame is the run's current status, so a client that
	// connects mid-run knows where it is without a second request.
	first := readFrames(t, reader, 1, 2*time.Second)
	if first[0].event != "status" {
		t.Fatalf("first frame is %q, want status", first[0].event)
	}

	// Nothing else is requested from here on. Every frame below arrives
	// because the server pushed it.
	appendStep(t, repo, task, "ev-1", "Using bash: ls")
	appendStep(t, repo, task, "ev-2", "Used bash: ls")

	steps := readFrames(t, reader, 2, 2*time.Second)
	for i, want := range []string{"Using bash: ls", "Used bash: ls"} {
		if steps[i].event != "step" {
			t.Fatalf("frame %d is %q, want step", i, steps[i].event)
		}
		if !strings.Contains(steps[i].data, want) {
			t.Errorf("frame %d data %q does not carry %q", i, steps[i].data, want)
		}
	}
}

// The pass that observes a terminal status still drains that task's events
// before it closes.
//
// This is the server side of the ordering PR #1709's review found the hard
// way. The flush writes a run's last steps immediately before the terminal
// transition, so a stream that returned the moment it read `succeeded` would
// hang up on frames that were already in the table, and the client would end
// a run having never been told what it did. Reading the status first and the
// events second is what closes it, and it has to live here now that the
// client no longer issues the two requests itself.
func TestHandler_EventStream_DrainsTheFinalStepsBeforeItEnds(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-stream")
	srv := newStreamServer(t, repo)

	resp, err := http.Get(streamURL(srv, task))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	readFrames(t, reader, 1, 2*time.Second) // the opening status

	// The order the flush guarantees: the steps land, then the terminal
	// status is published.
	appendStep(t, repo, task, "ev-final", "Used bash: go test")
	if _, err := repo.Transition(context.Background(), task.TenantID, task.UserID, task.ID,
		agenttask.StatusSucceeded, "", "summary", ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	got := readFrames(t, reader, 3, 2*time.Second)
	kinds := []string{got[0].event, got[1].event, got[2].event}
	// The step must appear, and it must appear before the stream says the run
	// is over. A stream that ends first loses it.
	if kinds[0] != "status" || kinds[1] != "step" || kinds[2] != "end" {
		t.Fatalf("frames %v, want status, step, end", kinds)
	}
	if !strings.Contains(got[1].data, "go test") {
		t.Errorf("the final step's data is %q", got[1].data)
	}
	if !strings.Contains(got[2].data, "succeeded") {
		t.Errorf("the end frame is %q, want the terminal status in it", got[2].data)
	}
}

// A run that has already finished streams its whole history and closes,
// rather than holding a connection open on a question that is settled.
func TestHandler_EventStream_ReplaysAFinishedRunAndCloses(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-stream")
	appendStep(t, repo, task, "ev-1", "Using bash: ls")
	if _, err := repo.Transition(context.Background(), task.TenantID, task.UserID, task.ID,
		agenttask.StatusSucceeded, "", "summary", ""); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	srv := newStreamServer(t, repo)

	resp, err := http.Get(streamURL(srv, task))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	got := readFrames(t, reader, 3, 2*time.Second)
	if got[0].event != "status" || got[1].event != "step" || got[2].event != "end" {
		t.Fatalf("frames %v %v %v, want status, step, end", got[0].event, got[1].event, got[2].event)
	}
	// Closed, not merely quiet: the body ends.
	rest := make([]byte, 1)
	if _, err := reader.Read(rest); err == nil {
		t.Errorf("the stream stayed open after the run had ended")
	}
}

// after_seq is honoured, so a client reconnecting after a dropped connection
// resumes rather than replaying every step it has already rendered.
func TestHandler_EventStream_ResumesFromTheCursor(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-stream")
	appendStep(t, repo, task, "ev-1", "already seen")
	appendStep(t, repo, task, "ev-2", "not yet seen")
	srv := newStreamServer(t, repo)

	resp, err := http.Get(streamURL(srv, task) + "?after_seq=1")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	got := readFrames(t, reader, 2, 2*time.Second)
	if got[1].event != "step" {
		t.Fatalf("second frame is %q, want step", got[1].event)
	}
	if strings.Contains(got[1].data, "already seen") {
		t.Errorf("the stream replayed a step behind the cursor: %q", got[1].data)
	}
	if !strings.Contains(got[1].data, "not yet seen") {
		t.Errorf("the stream skipped the step after the cursor: %q", got[1].data)
	}
}

// A task belonging to someone else is a 404 with a JSON body, the same as
// every other read on this surface. The refusal has to be an HTTP status
// rather than a frame: once the 200 and the event-stream header are written
// there is no status left to send, and a client would render a refusal as an
// empty run.
func TestHandler_EventStream_RefusesBeforeItOpensTheStream(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-stream")
	srv := newStreamServer(t, repo)

	other := srv.URL + "/internal/agent-tasks/" + uuid.New().String() + "/" +
		task.UserID.String() + "/" + task.ID.String() + "/events/stream"
	resp, err := http.Get(other)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type %q, want application/json", ct)
	}
}

// A bad cursor is refused rather than silently read as zero, matching the
// cursor read this replaces. Reading it as zero would replay a whole run into
// a transcript that already holds it.
func TestHandler_EventStream_RefusesABadCursor(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-stream")
	srv := newStreamServer(t, repo)

	resp, err := http.Get(streamURL(srv, task) + "?after_seq=-4")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", resp.StatusCode)
	}
}

// failAfterFirstGetRepo answers the first Get and fails every one after it,
// which is the shape of a read that breaks while a connection is already open:
// a revoked grant, schema drift, a pool with nothing left.
type failAfterFirstGetRepo struct {
	*fakeRepository
	mu    sync.Mutex
	calls int
}

func (f *failAfterFirstGetRepo) Get(ctx context.Context, tenantID, userID, id uuid.UUID) (agenttask.Task, error) {
	f.mu.Lock()
	f.calls++
	first := f.calls == 1
	f.mu.Unlock()
	if first {
		return f.fakeRepository.Get(ctx, tenantID, userID, id)
	}
	return agenttask.Task{}, errors.New("read failed")
}

// A stream whose reads keep failing ends, rather than staying open and silent.
//
// Silence is the failure mode worth refusing here. The connection would stay
// up for the whole thirty minute ceiling and read to a person as a slow run
// rather than a broken one, and the client would never go looking. Ending
// sends it to its settle read, which fails loudly, and then to its fallback.
func TestHandler_EventStream_EndsWhenItsReadsKeepFailing(t *testing.T) {
	inner := newFakeRepository()
	task := newActiveTask(inner, agenttask.StatusRunning, "session-stream")
	repo := &failAfterFirstGetRepo{fakeRepository: inner}
	srv := newStreamServer(t, repo)

	resp, err := http.Get(streamURL(srv, task))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)

	got := readFrames(t, reader, 2, 3*time.Second)
	if got[0].event != "status" {
		t.Fatalf("first frame is %q, want status", got[0].event)
	}
	if got[1].event != "end" {
		t.Fatalf("second frame is %q, want end: the stream stayed open and silent", got[1].event)
	}
}

// One user cannot hold more open streams than the ceiling, and a closed one
// gives its slot back.
//
// Every other read on this handler is one round trip and a released goroutine.
// This one holds a goroutine and two indexed reads per tick for as long as
// somebody watches, so without a ceiling the cost is set by how many
// connections a caller chooses to open.
func TestHandler_EventStream_RefusesMoreStreamsThanOneUserMayHold(t *testing.T) {
	repo := newFakeRepository()
	task := newActiveTask(repo, agenttask.StatusRunning, "session-stream")
	srv := newStreamServer(t, repo)

	open := func() *http.Response {
		t.Helper()
		resp, err := http.Get(streamURL(srv, task))
		if err != nil {
			t.Fatalf("open stream: %v", err)
		}
		return resp
	}

	held := make([]*http.Response, 0, agenttask.MaxStreamsPerUserForTest)
	for i := 0; i < agenttask.MaxStreamsPerUserForTest; i++ {
		resp := open()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream %d refused with %d before the ceiling", i, resp.StatusCode)
		}
		// Read the opening frame so the handler is certainly past its
		// acquire before the next connection is opened.
		readFrames(t, bufio.NewReader(resp.Body), 1, 2*time.Second)
		held = append(held, resp)
	}
	t.Cleanup(func() {
		for _, resp := range held {
			resp.Body.Close()
		}
	})

	refused := open()
	defer refused.Body.Close()
	if refused.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the stream past the ceiling got %d, want 429", refused.StatusCode)
	}
	if ct := refused.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("the refusal was sent as %q rather than JSON", ct)
	}

	// A slot comes back. Without this the ceiling would be a lifetime budget
	// rather than a concurrency one, and a person who watched six runs today
	// could never watch a seventh.
	held[0].Body.Close()
	var reopened *http.Response
	for i := 0; i < 40; i++ {
		reopened = open()
		if reopened.StatusCode == http.StatusOK {
			break
		}
		reopened.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	defer reopened.Body.Close()
	if reopened.StatusCode != http.StatusOK {
		t.Fatalf("a closed stream did not give its slot back: got %d", reopened.StatusCode)
	}
}
