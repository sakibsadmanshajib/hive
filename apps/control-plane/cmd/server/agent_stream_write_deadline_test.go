package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/httpserver"
)

// The agent-task event stream survives the API server's own WriteTimeout.
//
// This test exists because every other test of that endpoint used
// httptest.NewServer, which sets no timeouts at all, and so every one of them
// passed against a handler that was being cut mid-response in production. Go
// applies WriteTimeout as one absolute deadline for the whole response rather
// than per write, so a stream under it does not slow down, it dies: an
// "i/o timeout" here and an unexpected EOF at the customer, with the ceiling,
// the heartbeat and the failure budget all unreachable code behind it.
//
// So this one stands up newAPIServer, the same constructor main uses, with
// httpserver.WriteTimeout unchanged, and holds a stream open past it.
func TestAgentEventStream_SurvivesTheAPIServersWriteTimeout(t *testing.T) {
	if testing.Short() {
		// It has to outlast a real fifteen second deadline. There is no
		// shorter version of this assertion: the number under test is the
		// production one, and a scaled-down copy would be a test of a
		// different server.
		t.Skip("holds a connection past httpserver.WriteTimeout")
	}

	repo := newStreamTestRepo()
	task := repo.seedRunningTask()
	svc := agenttask.NewService(repo, agenttask.NotConfiguredEngine{})
	handler := agenttask.NewHandler(svc)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := httpserver.New(listener.Addr().String(), handler.InternalMux())
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	url := "http://" + listener.Addr().String() + "/internal/agent-tasks/" +
		task.TenantID.String() + "/" + task.UserID.String() + "/" + task.ID.String() + "/events/stream"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(httpserver.WriteTimeout + 10*time.Second)
	firstStep := time.Time{}
	lastStep := time.Time{}
	seen := 0

	// One step every two seconds for as long as this runs, so the stream has
	// something real to carry across the deadline rather than heartbeats
	// alone. Heartbeats are written through the same path, so either would
	// prove it, but a step is what a customer is actually waiting for.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Second):
			}
			repo.appendStep(task, i)
		}
	}()

	for time.Now().Before(deadline) {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			// The failure this test exists to catch. Before the per-frame
			// deadline, this arrived as "unexpected EOF" a fraction over
			// httpserver.WriteTimeout, every time.
			t.Fatalf("the stream was cut after %s with %v, having delivered %d step(s)",
				time.Since(firstStep).Round(time.Second), readErr, seen)
		}
		if !strings.HasPrefix(line, "event: step") {
			continue
		}
		if firstStep.IsZero() {
			firstStep = time.Now()
		}
		lastStep = time.Now()
		seen++
	}

	unbroken := lastStep.Sub(firstStep)
	if unbroken <= httpserver.WriteTimeout {
		t.Fatalf("the stream carried steps for only %s, which does not clear the %s write timeout",
			unbroken.Round(time.Second), httpserver.WriteTimeout)
	}
	if seen < 5 {
		t.Fatalf("only %d step(s) arrived over %s", seen, unbroken.Round(time.Second))
	}
	t.Logf("longest unbroken stream: %s carrying %d steps, against a %s write timeout",
		unbroken.Round(time.Second), seen, httpserver.WriteTimeout)
}

// streamTestRepo is the smallest agenttask.Repository this test needs: one
// running task and an append-only event log.
type streamTestRepo struct {
	agenttask.Repository
	mu     chan struct{}
	task   agenttask.Task
	events []agenttask.TaskEvent
}

func newStreamTestRepo() *streamTestRepo {
	r := &streamTestRepo{mu: make(chan struct{}, 1)}
	r.mu <- struct{}{}
	return r
}

func (r *streamTestRepo) lock()   { <-r.mu }
func (r *streamTestRepo) unlock() { r.mu <- struct{}{} }

func (r *streamTestRepo) seedRunningTask() agenttask.Task {
	r.lock()
	defer r.unlock()
	now := time.Now()
	r.task = agenttask.Task{
		ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(),
		Pack: agenttask.PackCoding, Status: agenttask.StatusRunning,
		EngineSessionRef: "session-write-deadline", CreatedAt: now, UpdatedAt: now,
	}
	return r.task
}

func (r *streamTestRepo) appendStep(task agenttask.Task, i int) {
	payload, _ := json.Marshal(map[string]any{"text_preview": "step", "n": i})
	r.lock()
	defer r.unlock()
	r.events = append(r.events, agenttask.TaskEvent{
		Seq: int64(len(r.events) + 1), Kind: agenttask.EventToolCall,
		Payload: payload, CreatedAt: time.Now(),
	})
}

func (r *streamTestRepo) Get(_ context.Context, tenantID, userID, id uuid.UUID) (agenttask.Task, error) {
	r.lock()
	defer r.unlock()
	if r.task.ID != id || r.task.TenantID != tenantID || r.task.UserID != userID {
		return agenttask.Task{}, agenttask.ErrNotFound
	}
	return r.task, nil
}

func (r *streamTestRepo) ListEvents(_ context.Context, _, _, _ uuid.UUID, afterSeq int64, limit int) ([]agenttask.TaskEvent, error) {
	r.lock()
	defer r.unlock()
	var out []agenttask.TaskEvent
	for _, ev := range r.events {
		if ev.Seq > afterSeq && len(out) < limit {
			out = append(out, ev)
		}
	}
	return out, nil
}
