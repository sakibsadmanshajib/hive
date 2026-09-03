// streamproofserver serves the real agent-task event stream, on the real API
// server construction, for a visual proof capture.
//
// Why this exists. The bug this endpoint shipped with was not in the handler;
// it was in the socket underneath it. cmd/server sets a fifteen second
// WriteTimeout, Go applies that as one absolute deadline for the whole
// response, and every stream was being cut mid-frame. Nothing caught it
// because no layer of the proof touched a real listener: the handler tests
// used httptest.NewServer, which sets no timeouts, and the browser capture
// talked to a Node stand-in.
//
// So this binary is the missing layer. It builds httpserver.New, the same
// constructor cmd/server calls, with httpserver.WriteTimeout unchanged, around
// the same agenttask.Handler, and serves the same internal mux. A capture that
// drives a browser through it is driving it through the socket that had the
// bug.
//
// What is NOT real here, stated plainly: the repository. There is no Postgres
// and no sandbox, so the run is an in-memory event log that appends a step on
// a timer. That is deliberate and it is not where the defect lived. A step
// still has to cross the handler, the ResponseController deadline, the
// listener and the network to be rendered.
//
// Mirrors apps/agent-engine/cmd/streamproof, which exists for the same reason
// on the other side of the launcher.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/agenttask"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/httpserver"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8099", "listen address")
	every := flag.Duration("step-every", 2*time.Second, "how often the run takes a step")
	steps := flag.Int("steps", 12, "how many steps the run takes before it succeeds")
	flag.Parse()

	repo := newProofRepo()
	task := repo.seed()
	svc := agenttask.NewService(repo, agenttask.NotConfiguredEngine{})
	handler := agenttask.NewHandler(svc)

	// The identifiers a capture needs to build the URL, on stdout as JSON so a
	// harness reads them rather than being told them out of band and drifting.
	out, _ := json.Marshal(map[string]string{
		"tenant_id": task.TenantID.String(),
		"user_id":   task.UserID.String(),
		"task_id":   task.ID.String(),
		"path": fmt.Sprintf("/internal/agent-tasks/%s/%s/%s/events/stream",
			task.TenantID, task.UserID, task.ID),
	})
	fmt.Println(string(out))
	_ = os.Stdout.Sync()

	go repo.run(*every, *steps)

	srv := httpserver.New(*addr, handler.InternalMux())
	log.Printf("stream proof server on %s, write timeout %s, %d steps every %s",
		*addr, httpserver.WriteTimeout, *steps, *every)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("ListenAndServe: %v", err)
	}
}

// proofRepo is the smallest agenttask.Repository this binary needs: one
// running task and an append-only event log.
type proofRepo struct {
	agenttask.Repository

	mu     sync.Mutex
	task   agenttask.Task
	events []agenttask.TaskEvent
}

func newProofRepo() *proofRepo { return &proofRepo{} }

func (r *proofRepo) seed() agenttask.Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	r.task = agenttask.Task{
		ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(),
		Pack:             agenttask.PackKnowledgeWork,
		Instructions:     "Create a file named sixcap.txt containing the text HIVE-COWORK-OK, then show its contents",
		Status:           agenttask.StatusQueued,
		EngineSessionRef: "session-stream-proof",
		CreatedAt:        now, UpdatedAt: now,
	}
	return r.task
}

// run walks the task through queued, running, its steps, and succeeded.
func (r *proofRepo) run(every time.Duration, steps int) {
	time.Sleep(every)
	r.setStatus(agenttask.StatusRunning)

	// Alternating call and result, so the transcript renders the same open and
	// close pair a real run produces rather than a flat list.
	for i := 0; i < steps; i++ {
		time.Sleep(every)
		call := i%2 == 0
		kind := agenttask.EventToolResult
		preview := fmt.Sprintf("step %d finished", i/2+1)
		if call {
			kind = agenttask.EventToolCall
			preview = fmt.Sprintf("step %d running", i/2+1)
		}
		payload, _ := json.Marshal(map[string]any{
			"tool_name":    "bash",
			"tool_call_id": fmt.Sprintf("c%d", i/2),
			"preview":      preview,
		})
		r.append(agenttask.TaskEvent{Kind: kind, Payload: payload})
	}

	time.Sleep(every)
	r.setStatus(agenttask.StatusSucceeded)
}

func (r *proofRepo) setStatus(status agenttask.Status) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.task.Status = status
	r.task.UpdatedAt = time.Now()
	if status == agenttask.StatusSucceeded {
		r.task.ResultSummaryRef = "sixcap.txt now holds HIVE-COWORK-OK"
	}
}

func (r *proofRepo) append(ev agenttask.TaskEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ev.Seq = int64(len(r.events) + 1)
	ev.CreatedAt = time.Now()
	r.events = append(r.events, ev)
}

func (r *proofRepo) Get(_ context.Context, tenantID, userID, id uuid.UUID) (agenttask.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task.ID != id || r.task.TenantID != tenantID || r.task.UserID != userID {
		return agenttask.Task{}, agenttask.ErrNotFound
	}
	return r.task, nil
}

func (r *proofRepo) ListEvents(_ context.Context, _, _, _ uuid.UUID, afterSeq int64, limit int) ([]agenttask.TaskEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []agenttask.TaskEvent
	for _, ev := range r.events {
		if ev.Seq > afterSeq && len(out) < limit {
			out = append(out, ev)
		}
	}
	return out, nil
}
