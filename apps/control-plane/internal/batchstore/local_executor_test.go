package batchstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/batchstore/executor"
	"github.com/hivegpt/hive/apps/control-plane/internal/filestore"
	"github.com/hivegpt/hive/apps/control-plane/internal/routing"
)

// stubRoutingProvider always returns the configured provider + model.
type stubRoutingProvider struct {
	provider string
	model    string
}

func (s *stubRoutingProvider) SelectRoute(ctx context.Context, in routing.SelectionInput) (routing.SelectionResult, error) {
	return routing.SelectionResult{
		AliasID:          in.AliasID,
		RouteID:          "route-1",
		LiteLLMModelName: s.model,
		Provider:         s.provider,
	}, nil
}

// stubExecuteQueue captures local-executor enqueues.
type stubExecuteQueue struct {
	mu       sync.Mutex
	payloads []BatchExecutePayload
}

func (q *stubExecuteQueue) EnqueueExecute(ctx context.Context, payload BatchExecutePayload) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.payloads = append(q.payloads, payload)
	return nil
}

// stubPollQueue captures upstream-poll enqueues.
type stubPollQueue struct {
	mu       sync.Mutex
	payloads []BatchPollPayload
}

func (q *stubPollQueue) Enqueue(ctx context.Context, payload BatchPollPayload) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.payloads = append(q.payloads, payload)
	return nil
}

// stubSubmitterFiles implements BatchFileStore.
type stubSubmitterFiles struct {
	mu      sync.Mutex
	file    *filestore.File
	updates []map[string]interface{}
	statuses []string
}

func (s *stubSubmitterFiles) GetFile(ctx context.Context, id, accountID string) (*filestore.File, error) {
	if s.file == nil {
		return nil, fmt.Errorf("not found")
	}
	return s.file, nil
}

func (s *stubSubmitterFiles) UpdateBatchStatus(ctx context.Context, batchID, status string, updates map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = append(s.statuses, status)
	cp := map[string]interface{}{}
	for k, v := range updates {
		cp[k] = v
	}
	s.updates = append(s.updates, cp)
	return nil
}

// stubInputStorage implements BatchInputStorage with empty bytes.
type stubInputStorage struct{}

func (stubInputStorage) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader([]byte{})), nil
}

// stubReleaser implements BatchReservationReleaser as a no-op.
type stubReleaser struct{}

func (stubReleaser) ReleaseReservation(ctx context.Context, in accounting.ReleaseReservationInput) (accounting.Reservation, error) {
	return accounting.Reservation{}, nil
}

// Test 1: submitter chooses local executor for openrouter route by default.
func TestSubmitter_RoutesOpenRouterToLocalExecutor(t *testing.T) {
	files := &stubSubmitterFiles{file: &filestore.File{ID: "f1", StoragePath: "batches/b1/input.jsonl"}}
	rt := &stubRoutingProvider{provider: "openrouter", model: "openrouter/free"}
	pq := &stubPollQueue{}
	eq := &stubExecuteQueue{}
	s := NewSubmitter(files, rt, stubInputStorage{}, pq, stubReleaser{}, "http://litellm:4000", "key", "hive-files").
		WithLocalExecutor(eq, "auto")
	batch := filestore.Batch{
		ID:               "b1",
		AccountID:        uuid.New().String(),
		InputFileID:      "f1",
		ModelAlias:       "alias-1",
		Endpoint:         "/v1/chat/completions",
		EstimatedCredits: 1000,
	}
	out, err := s.SubmitBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if out.Status != "in_progress" {
		t.Fatalf("status=%q want in_progress", out.Status)
	}
	if len(eq.payloads) != 1 || eq.payloads[0].BatchID != "b1" {
		t.Fatalf("expected 1 execute enqueue, got %+v", eq.payloads)
	}
	if len(pq.payloads) != 0 {
		t.Fatalf("upstream poll queue should be empty, got %+v", pq.payloads)
	}
	foundKind := false
	for _, u := range files.updates {
		if u["executor_kind"] == "local" {
			foundKind = true
		}
	}
	if !foundKind {
		t.Fatalf("expected status update with executor_kind=local, got %+v", files.updates)
	}
}

// Test 2: env override "upstream" forces upstream path even for openrouter.
// The upstream path makes a real HTTP call to a non-existent URL and fails
// fast — the assertion is that submitLocal was NOT taken (no execute enqueue).
func TestSubmitter_EnvOverrideForcesUpstream(t *testing.T) {
	files := &stubSubmitterFiles{file: &filestore.File{ID: "f1", StoragePath: "batches/b1/input.jsonl"}}
	rt := &stubRoutingProvider{provider: "openrouter", model: "openrouter/free"}
	pq := &stubPollQueue{}
	eq := &stubExecuteQueue{}
	s := NewSubmitter(files, rt, stubInputStorage{}, pq, stubReleaser{}, "http://nonexistent.invalid", "key", "hive-files").
		WithLocalExecutor(eq, "upstream")
	batch := filestore.Batch{
		ID:               "b2",
		AccountID:        uuid.New().String(),
		InputFileID:      "f1",
		ModelAlias:       "alias-1",
		Endpoint:         "/v1/chat/completions",
		EstimatedCredits: 1000,
	}
	_, _ = s.SubmitBatch(context.Background(), batch)
	if len(eq.payloads) != 0 {
		t.Fatalf("upstream override but local execute enqueued: %+v", eq.payloads)
	}
}

// Test 2b: openai provider always uses upstream regardless of executorKind=auto.
func TestSubmitter_OpenAIProviderUsesUpstream(t *testing.T) {
	files := &stubSubmitterFiles{file: &filestore.File{ID: "f1", StoragePath: "batches/b1/input.jsonl"}}
	rt := &stubRoutingProvider{provider: "openai", model: "gpt-4o-mini"}
	pq := &stubPollQueue{}
	eq := &stubExecuteQueue{}
	s := NewSubmitter(files, rt, stubInputStorage{}, pq, stubReleaser{}, "http://nonexistent.invalid", "key", "hive-files").
		WithLocalExecutor(eq, "auto")
	batch := filestore.Batch{
		ID:               "b3",
		AccountID:        uuid.New().String(),
		InputFileID:      "f1",
		ModelAlias:       "alias-1",
		Endpoint:         "/v1/chat/completions",
		EstimatedCredits: 1000,
	}
	_, _ = s.SubmitBatch(context.Background(), batch)
	if len(eq.payloads) != 0 {
		t.Fatalf("openai provider but local execute enqueued: %+v", eq.payloads)
	}
}

// fakeStorageLE implements executor.StoragePort for the worker test.
type fakeStorageLE struct {
	mu        sync.Mutex
	uploads   map[string][]byte
	downloads map[string][]byte
}

func newFakeStorageLE() *fakeStorageLE {
	return &fakeStorageLE{uploads: map[string][]byte{}, downloads: map[string][]byte{}}
}

func (f *fakeStorageLE) Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads[bucket+"/"+key] = data
	return nil
}

func (f *fakeStorageLE) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.downloads[bucket+"/"+key]
	if !ok {
		return nil, fmt.Errorf("not found: %s/%s", bucket, key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// fakeBatchSnapLE implements executor.BatchStore.
type fakeBatchSnapLE struct {
	mu              sync.Mutex
	snap            executor.BatchSnapshot
	completedCalled bool
	completedLines  int
	failedLines     int
	overconsumed    bool
}

func (f *fakeBatchSnapLE) LoadBatch(ctx context.Context, id string) (executor.BatchSnapshot, error) {
	if f.snap.ID != id {
		return executor.BatchSnapshot{}, fmt.Errorf("not found")
	}
	return f.snap, nil
}

func (f *fakeBatchSnapLE) MarkCompleted(ctx context.Context, batchID string, completedLines, failedLines int, outputFileID, errorFileID string, overconsumed bool, completedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completedCalled = true
	f.completedLines = completedLines
	f.failedLines = failedLines
	f.overconsumed = overconsumed
	return nil
}

// fakeLineStoreLE implements executor.LineStore.
type fakeLineStoreLE struct{}

func newFakeLineStoreLE() *fakeLineStoreLE { return &fakeLineStoreLE{} }
func (f *fakeLineStoreLE) LoadLines(ctx context.Context, batchID string) ([]executor.LineRow, error) {
	return nil, nil
}
func (f *fakeLineStoreLE) UpsertPending(ctx context.Context, batchID, customID string) error {
	return nil
}
func (f *fakeLineStoreLE) MarkSucceeded(ctx context.Context, batchID, customID string, attempt int, consumedCredits int64, outputIndex int) error {
	return nil
}
func (f *fakeLineStoreLE) MarkFailed(ctx context.Context, batchID, customID string, attempt int, errorIndex int, lastError string) error {
	return nil
}

// fakeReservationLE implements executor.ReservationPort.
type fakeReservationLE struct {
	calls int
}

func (f *fakeReservationLE) Settle(ctx context.Context, batchID, accountID, reservationID string, actualCredits int64, overconsumed bool, terminalStatus string) error {
	f.calls++
	return nil
}

// fakeFileRegistrarLE implements executor.FileRegistrar with synthetic IDs.
type fakeFileRegistrarLE struct {
	mu   sync.Mutex
	next int
}

func (f *fakeFileRegistrarLE) Register(_ context.Context, _, _, _, _ string, _ int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	return fmt.Sprintf("file-le-%d", f.next), nil
}

// fakeInfer implements executor.InferencePort.
type fakeInfer struct {
	response json.RawMessage
	usage    *executor.Usage
	status   int
	err      error
}

func (f *fakeInfer) ChatCompletion(ctx context.Context, model string, body json.RawMessage) (json.RawMessage, *executor.Usage, int, error) {
	return f.response, f.usage, f.status, f.err
}

// Test 3: worker handles batch:execute task by calling executor.Run.
func TestBatchWorker_HandlesBatchExecuteTask(t *testing.T) {
	storeFs := newFakeStorageLE()
	storeFs.downloads["hive-files/batches/bx/input.jsonl"] = []byte(
		`{"custom_id":"a","method":"POST","url":"/v1/chat/completions","body":{"model":"alias-1","messages":[]}}` + "\n",
	)
	infer := &fakeInfer{
		response: json.RawMessage(`{"ok":true,"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`),
		usage:    &executor.Usage{TotalTokens: 3},
		status:   200,
	}
	disp, err := executor.NewDispatcher(executor.Config{Concurrency: 1, MaxRetries: 1, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatal(err)
	}
	bs := &fakeBatchSnapLE{snap: executor.BatchSnapshot{
		ID: "bx", AccountID: uuid.New().String(), InputFilePath: "batches/bx/input.jsonl",
		ReservationID: uuid.New().String(), ModelAlias: "alias-1", ReservedCredits: 1000,
	}}
	ls := newFakeLineStoreLE()
	rp := &fakeReservationLE{}
	fr := &fakeFileRegistrarLE{}
	ex, err := executor.NewExecutor(executor.Config{Concurrency: 1, MaxRetries: 1, LineTimeout: 5 * time.Second}, bs, ls, storeFs, fr, "hive-files", disp, rp)
	if err != nil {
		t.Fatal(err)
	}

	worker := &BatchWorker{}
	worker.WithLocalExecutor(ex)

	payload, _ := json.Marshal(BatchExecutePayload{BatchID: "bx", AccountID: bs.snap.AccountID})
	task := asynq.NewTask(TypeBatchExecute, payload)
	if err := worker.HandleBatchExecute(context.Background(), task); err != nil {
		t.Fatalf("handle execute: %v", err)
	}
	if !bs.completedCalled {
		t.Fatalf("expected MarkCompleted to be called")
	}
	if bs.completedLines != 1 {
		t.Fatalf("completed=%d want 1", bs.completedLines)
	}
}

var _ = strings.ToLower
