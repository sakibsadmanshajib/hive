package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBatchStore implements BatchStore in-memory.
type fakeBatchStore struct {
	mu        sync.Mutex
	snap      BatchSnapshot
	completed *struct {
		batchID                       string
		completedLines, failedLines   int
		outputFileID, errorFileID     string
		overconsumed                  bool
		completedAt                   time.Time
	}
}

func (f *fakeBatchStore) LoadBatch(ctx context.Context, id string) (BatchSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snap.ID != id {
		return BatchSnapshot{}, fmt.Errorf("not found")
	}
	return f.snap, nil
}

func (f *fakeBatchStore) MarkCompleted(ctx context.Context, batchID string, completedLines, failedLines int, outputFileID, errorFileID string, overconsumed bool, completedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = &struct {
		batchID                       string
		completedLines, failedLines   int
		outputFileID, errorFileID     string
		overconsumed                  bool
		completedAt                   time.Time
	}{batchID, completedLines, failedLines, outputFileID, errorFileID, overconsumed, completedAt}
	return nil
}

// fakeLineStore is an in-memory LineStore.
type fakeLineStore struct {
	mu   sync.Mutex
	rows map[string]map[string]LineRow // batchID -> customID -> row
}

func newFakeLineStore() *fakeLineStore {
	return &fakeLineStore{rows: map[string]map[string]LineRow{}}
}

func (s *fakeLineStore) seed(rows []LineRow) {
	for _, r := range rows {
		if s.rows[r.BatchID] == nil {
			s.rows[r.BatchID] = map[string]LineRow{}
		}
		s.rows[r.BatchID][r.CustomID] = r
	}
}

func (s *fakeLineStore) LoadLines(ctx context.Context, batchID string) ([]LineRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LineRow, 0, len(s.rows[batchID]))
	for _, r := range s.rows[batchID] {
		out = append(out, r)
	}
	return out, nil
}

func (s *fakeLineStore) UpsertPending(ctx context.Context, batchID, customID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rows[batchID] == nil {
		s.rows[batchID] = map[string]LineRow{}
	}
	if _, ok := s.rows[batchID][customID]; !ok {
		s.rows[batchID][customID] = LineRow{BatchID: batchID, CustomID: customID, Status: "pending"}
	}
	return nil
}

func (s *fakeLineStore) MarkSucceeded(ctx context.Context, batchID, customID string, attempt int, consumedCredits int64, outputIndex int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[batchID][customID]
	r.BatchID = batchID
	r.CustomID = customID
	r.Status = "succeeded"
	r.Attempt = attempt
	r.ConsumedCredits = consumedCredits
	idx := outputIndex
	r.OutputIndex = &idx
	s.rows[batchID][customID] = r
	return nil
}

func (s *fakeLineStore) MarkFailed(ctx context.Context, batchID, customID string, attempt int, errorIndex int, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[batchID][customID]
	r.BatchID = batchID
	r.CustomID = customID
	r.Status = "failed"
	r.Attempt = attempt
	idx := errorIndex
	r.ErrorIndex = &idx
	r.LastError = lastError
	s.rows[batchID][customID] = r
	return nil
}

func (s *fakeLineStore) snapshot(batchID string) []LineRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LineRow, 0, len(s.rows[batchID]))
	for _, r := range s.rows[batchID] {
		out = append(out, r)
	}
	return out
}

// fakeFileRegistrar records Register calls and returns predictable IDs.
type fakeFileRegistrar struct {
	mu    sync.Mutex
	next  int
	calls []fakeRegisterCall
}

type fakeRegisterCall struct {
	accountID, purpose, filename, storagePath, id string
	bytes                                         int64
}

func (f *fakeFileRegistrar) Register(_ context.Context, accountID, purpose, filename, storagePath string, bytes int64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := fmt.Sprintf("file-test-%d", f.next)
	f.calls = append(f.calls, fakeRegisterCall{accountID, purpose, filename, storagePath, id, bytes})
	return id, nil
}

// fakeReservation records settlements.
type fakeReservation struct {
	mu                                          sync.Mutex
	calls                                       int
	lastActual                                  int64
	lastOverconsumed                            bool
	lastTerminalStatus                          string
}

func (f *fakeReservation) Settle(ctx context.Context, batchID, accountID, reservationID string, actualCredits int64, overconsumed bool, terminalStatus string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastActual = actualCredits
	f.lastOverconsumed = overconsumed
	f.lastTerminalStatus = terminalStatus
	return nil
}

// makeBatchInput builds n lines: indices in errorIndices return 4xx; others succeed.
func makeBatchInput(n int) []byte {
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		body := fmt.Sprintf(`{"model":"alias-1","messages":[{"role":"user","content":"line %d"}]}`, i)
		line := fmt.Sprintf(`{"custom_id":"req-%d","method":"POST","url":"/v1/chat/completions","body":%s}`, i, body)
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.Bytes()
}

// Test 1: 100-line input, 95 success / 5 error → output.jsonl has 95 lines,
// errors.jsonl has 5; batch row status=completed; completed_lines=95,
// failed_lines=5; Storage upload called twice.
func TestExecutor_MixedSuccessFailure(t *testing.T) {
	const total = 100
	storeFs := newFakeStorage()
	storeFs.downloads["hive-files/batches/b1/input.jsonl"] = makeBatchInput(total)

	failIDs := map[string]bool{"req-3": true, "req-17": true, "req-42": true, "req-66": true, "req-91": true}
	infer := &fakeInference{
		handler: func(ctx context.Context, attempt int, model string, body json.RawMessage) (json.RawMessage, *Usage, int, error) {
			var probe struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.Unmarshal(body, &probe)
			content := ""
			if len(probe.Messages) > 0 {
				content = probe.Messages[0].Content
			}
			// Map content "line N" back to "req-N".
			id := strings.Replace(content, "line ", "req-", 1)
			if failIDs[id] {
				return nil, nil, 400, errors.New("bad request")
			}
			return json.RawMessage(`{"id":"chatcmpl_x","choices":[]}`), &Usage{TotalTokens: 10}, 200, nil
		},
	}
	disp, err := NewDispatcher(Config{Concurrency: 4, MaxRetries: 2, LineTimeout: 5 * time.Second}, infer, nil)
	if err != nil {
		t.Fatal(err)
	}
	bs := &fakeBatchStore{snap: BatchSnapshot{
		ID: "b1", AccountID: "acct", InputFileID: "f1", InputFilePath: "batches/b1/input.jsonl",
		ReservationID: "res1", Endpoint: "/v1/chat/completions", ModelAlias: "alias-1",
		LiteLLMModel:    "openrouter/gpt-4o-mini",
		ReservedCredits: 100000,
	}}
	ls := newFakeLineStore()
	rp := &fakeReservation{}
	fr := &fakeFileRegistrar{}
	ex, err := NewExecutor(Config{Concurrency: 4, MaxRetries: 2, LineTimeout: 5 * time.Second}, bs, ls, storeFs, fr, "hive-files", disp, rp)
	if err != nil {
		t.Fatal(err)
	}
	if err := ex.Run(context.Background(), "b1"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if bs.completed == nil {
		t.Fatalf("MarkCompleted not called")
	}
	if bs.completed.completedLines != 95 || bs.completed.failedLines != 5 {
		t.Fatalf("counts=%d/%d want 95/5", bs.completed.completedLines, bs.completed.failedLines)
	}
	// Output + errors files must both be registered (so MarkCompleted gets real file IDs).
	if len(fr.calls) != 2 {
		t.Fatalf("file registrar calls=%d want 2", len(fr.calls))
	}
	if bs.completed.outputFileID != fr.calls[0].id || bs.completed.errorFileID != fr.calls[1].id {
		t.Fatalf("MarkCompleted got file IDs %q/%q want %q/%q", bs.completed.outputFileID, bs.completed.errorFileID, fr.calls[0].id, fr.calls[1].id)
	}
	out := storeFs.uploads["hive-files/batches/b1/output.jsonl"]
	errs := storeFs.uploads["hive-files/batches/b1/errors.jsonl"]
	if out == nil {
		t.Fatalf("output.jsonl not uploaded")
	}
	if errs == nil {
		t.Fatalf("errors.jsonl not uploaded")
	}
	if got := bytes.Count(out, []byte("\n")); got != 95 {
		t.Fatalf("output line count=%d want 95", got)
	}
	if got := bytes.Count(errs, []byte("\n")); got != 5 {
		t.Fatalf("errors line count=%d want 5", got)
	}
	if rp.calls != 1 {
		t.Fatalf("reservation settle calls=%d want 1", rp.calls)
	}
	// 95 succeeded × 10 tokens = 950 credits.
	if rp.lastActual != 950 {
		t.Fatalf("actual=%d want 950", rp.lastActual)
	}
}

// Test 3: empty input file → batch row status=completed, completed_lines=0,
// failed_lines=0, output.jsonl uploaded as empty, errors.jsonl skipped.
func TestExecutor_EmptyInput(t *testing.T) {
	storeFs := newFakeStorage()
	storeFs.downloads["hive-files/batches/b2/input.jsonl"] = []byte("")

	infer := &fakeInference{}
	disp, _ := NewDispatcher(Config{Concurrency: 1, MaxRetries: 1, LineTimeout: 1 * time.Second}, infer, nil)
	bs := &fakeBatchStore{snap: BatchSnapshot{
		ID: "b2", AccountID: "acct", InputFilePath: "batches/b2/input.jsonl",
		ReservationID: "res2", ModelAlias: "alias-1", LiteLLMModel: "openrouter/gpt-4o-mini",
		ReservedCredits: 100,
	}}
	ls := newFakeLineStore()
	rp := &fakeReservation{}
	fr := &fakeFileRegistrar{}
	ex, _ := NewExecutor(Config{Concurrency: 1, MaxRetries: 1, LineTimeout: 1 * time.Second}, bs, ls, storeFs, fr, "hive-files", disp, rp)
	if err := ex.Run(context.Background(), "b2"); err != nil {
		t.Fatal(err)
	}
	if bs.completed.completedLines != 0 || bs.completed.failedLines != 0 {
		t.Fatalf("counts not zero")
	}
	if _, ok := storeFs.uploads["hive-files/batches/b2/errors.jsonl"]; ok {
		t.Fatalf("errors.jsonl should not be uploaded for empty input")
	}
	// Empty input still registers the (empty) output file but skips errors.
	if len(fr.calls) != 1 {
		t.Fatalf("registrar calls=%d want 1 (output only)", len(fr.calls))
	}
	if bs.completed.errorFileID != "" {
		t.Fatalf("errorFileID=%q want empty when errors.jsonl skipped", bs.completed.errorFileID)
	}
	out, ok := storeFs.uploads["hive-files/batches/b2/output.jsonl"]
	if !ok {
		t.Fatalf("output.jsonl missing")
	}
	if len(out) != 0 {
		t.Fatalf("expected empty output, got %d bytes", len(out))
	}
}

// Test 4: all-error input (10/10 fail) → status=completed semantically (per
// OpenAI), failed_lines=10, completed_lines=0, settlement actual=0.
func TestExecutor_AllErrors(t *testing.T) {
	storeFs := newFakeStorage()
	storeFs.downloads["hive-files/batches/b3/input.jsonl"] = makeBatchInput(10)

	infer := &fakeInference{
		handler: func(ctx context.Context, attempt int, model string, body json.RawMessage) (json.RawMessage, *Usage, int, error) {
			return nil, nil, 422, errors.New("invalid")
		},
	}
	disp, _ := NewDispatcher(Config{Concurrency: 2, MaxRetries: 1, LineTimeout: 1 * time.Second}, infer, nil)
	bs := &fakeBatchStore{snap: BatchSnapshot{
		ID: "b3", AccountID: "acct", InputFilePath: "batches/b3/input.jsonl",
		ReservationID: "res3", ModelAlias: "alias-1", LiteLLMModel: "openrouter/gpt-4o-mini",
		ReservedCredits: 5000,
	}}
	ls := newFakeLineStore()
	rp := &fakeReservation{}
	fr := &fakeFileRegistrar{}
	ex, _ := NewExecutor(Config{Concurrency: 2, MaxRetries: 1, LineTimeout: 1 * time.Second}, bs, ls, storeFs, fr, "hive-files", disp, rp)
	if err := ex.Run(context.Background(), "b3"); err != nil {
		t.Fatal(err)
	}
	if bs.completed.completedLines != 0 || bs.completed.failedLines != 10 {
		t.Fatalf("counts=%d/%d want 0/10", bs.completed.completedLines, bs.completed.failedLines)
	}
	if rp.lastActual != 0 {
		t.Fatalf("actual=%d want 0", rp.lastActual)
	}
}

// Test 2: mid-run resume — first run processes 6/10 (others skipped via prior
// batch_lines rows), counts and uploads reflect resume semantics; no
// duplicate-charge.
func TestExecutor_RestartResume(t *testing.T) {
	storeFs := newFakeStorage()
	storeFs.downloads["hive-files/batches/b4/input.jsonl"] = makeBatchInput(10)

	// Pre-populate 6 succeeded + 0 failed lines (simulating prior crash).
	ls := newFakeLineStore()
	priorRows := make([]LineRow, 0, 6)
	for i := 0; i < 6; i++ {
		priorRows = append(priorRows, LineRow{
			BatchID: "b4", CustomID: fmt.Sprintf("req-%d", i),
			Status: "succeeded", Attempt: 1, ConsumedCredits: 10,
		})
	}
	ls.seed(priorRows)

	dispatched := 0
	var dispatchedMu sync.Mutex
	infer := &fakeInference{
		handler: func(ctx context.Context, attempt int, model string, body json.RawMessage) (json.RawMessage, *Usage, int, error) {
			dispatchedMu.Lock()
			dispatched++
			dispatchedMu.Unlock()
			return json.RawMessage(`{"ok":true}`), &Usage{TotalTokens: 10}, 200, nil
		},
	}
	disp, _ := NewDispatcher(Config{Concurrency: 2, MaxRetries: 1, LineTimeout: 1 * time.Second}, infer, nil)
	bs := &fakeBatchStore{snap: BatchSnapshot{
		ID: "b4", AccountID: "acct", InputFilePath: "batches/b4/input.jsonl",
		ReservationID: "res4", ModelAlias: "alias-1", LiteLLMModel: "openrouter/gpt-4o-mini",
		ReservedCredits: 100000,
	}}
	rp := &fakeReservation{}
	fr := &fakeFileRegistrar{}
	ex, _ := NewExecutor(Config{Concurrency: 2, MaxRetries: 1, LineTimeout: 1 * time.Second}, bs, ls, storeFs, fr, "hive-files", disp, rp)
	if err := ex.Run(context.Background(), "b4"); err != nil {
		t.Fatal(err)
	}
	if dispatched != 4 {
		t.Fatalf("dispatched=%d want 4 (resume should skip 6)", dispatched)
	}
	if bs.completed.completedLines != 10 {
		t.Fatalf("completed=%d want 10 (6 prior + 4 new)", bs.completed.completedLines)
	}
	// Output.jsonl must contain 10 lines (no duplicates by custom_id).
	out := storeFs.uploads["hive-files/batches/b4/output.jsonl"]
	seen := map[string]int{}
	for _, ln := range bytes.Split(bytes.TrimRight(out, "\n"), []byte("\n")) {
		var v struct {
			CustomID string `json:"custom_id"`
		}
		if err := json.Unmarshal(ln, &v); err != nil {
			t.Fatalf("decode line: %v", err)
		}
		seen[v.CustomID]++
	}
	if len(seen) != 10 {
		t.Fatalf("unique customIDs=%d want 10", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("custom_id %s appeared %d times — resume duplicated work", id, n)
		}
	}
	// 6 prior + 4 new × 10 tokens = 100 credits.
	if rp.lastActual != 100 {
		t.Fatalf("actual=%d want 100 (no duplicate-charge)", rp.lastActual)
	}
}

// readAllStorage convenience helper (silence unused imports).
var _ = io.ReadAll
