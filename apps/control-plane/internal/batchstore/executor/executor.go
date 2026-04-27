package executor

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/big"
	"strings"
	"time"
)

// BatchSnapshot is the slice of public.batches the executor needs to do its
// work. Concrete implementations live in the batchstore package.
type BatchSnapshot struct {
	ID            string
	AccountID     string
	InputFileID   string
	InputFilePath string
	ReservationID string
	Endpoint      string
	ModelAlias    string
	ReservedCredits int64
}

// LineRow is the persisted state of a single batch line. Mirrors public.batch_lines.
type LineRow struct {
	BatchID         string
	CustomID        string
	Status          string // "pending" | "in_progress" | "succeeded" | "failed"
	Attempt         int
	ConsumedCredits int64
	OutputIndex     *int
	ErrorIndex      *int
	LastError       string
}

// BatchStore is the persistence port the executor depends on. Production
// implementations wrap pgx queries against public.batches.
type BatchStore interface {
	// LoadBatch returns the batch snapshot needed for execution.
	LoadBatch(ctx context.Context, batchID string) (BatchSnapshot, error)
	// MarkCompleted writes terminal status, line counts, output/error file
	// IDs, and overconsumed flag in a single transaction.
	MarkCompleted(ctx context.Context, batchID string, completedLines, failedLines int, outputFileID, errorFileID string, overconsumed bool, completedAt time.Time) error
}

// LineStore persists per-line state for restart-safe resume.
type LineStore interface {
	// LoadLines returns previously-persisted line rows for the batch.
	LoadLines(ctx context.Context, batchID string) ([]LineRow, error)
	// UpsertPending creates or no-ops a pending row before dispatch.
	UpsertPending(ctx context.Context, batchID, customID string) error
	// MarkSucceeded transitions a line row to succeeded and records consumed credits + output index.
	MarkSucceeded(ctx context.Context, batchID, customID string, attempt int, consumedCredits int64, outputIndex int) error
	// MarkFailed transitions a line row to failed with sanitized last_error and error_index.
	MarkFailed(ctx context.Context, batchID, customID string, attempt int, errorIndex int, lastError string) error
}

// ReservationPort releases the reserved credits for a batch by setting the
// final actual_credits. Implementation reuses accounting.Service.FinalizeReservation
// when actual > 0, or ReleaseReservation when actual == 0.
type ReservationPort interface {
	Settle(ctx context.Context, batchID, accountID, reservationID string, actualCredits int64, overconsumed bool, terminalStatus string) error
}

// FileRegistrar persists a public.files row for an output/error artifact the
// executor uploaded to object storage and returns the generated file ID.
// MarkCompleted writes that ID into batches.output_file_id / error_file_id
// (FK -> public.files.id), so this port must be invoked before MarkCompleted
// — otherwise the FK references a non-existent row.
type FileRegistrar interface {
	Register(ctx context.Context, accountID, purpose, filename, storagePath string, bytes int64) (string, error)
}

// Executor is the local batch executor entry point. Constructed once at
// startup and shared by the asynq worker handling the batch:execute task.
type Executor struct {
	cfg          Config
	batches      BatchStore
	lines        LineStore
	storage      StoragePort
	files        FileRegistrar
	bucket       string
	dispatcher   *Dispatcher
	reservations ReservationPort

	// outputKey/errorKey templates expose hooks for tests to predict storage paths.
	outputKey func(batchID string) string
	errorKey  func(batchID string) string
}

// NewExecutor wires the executor with its ports.
func NewExecutor(cfg Config, batches BatchStore, lines LineStore, storage StoragePort, files FileRegistrar, bucket string, dispatcher *Dispatcher, reservations ReservationPort) (*Executor, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if batches == nil || lines == nil || storage == nil || files == nil || dispatcher == nil || reservations == nil {
		return nil, fmt.Errorf("executor: all ports are required")
	}
	if strings.TrimSpace(bucket) == "" {
		return nil, fmt.Errorf("executor: bucket is required")
	}
	return &Executor{
		cfg:          cfg,
		batches:      batches,
		lines:        lines,
		storage:      storage,
		files:        files,
		bucket:       bucket,
		dispatcher:   dispatcher,
		reservations: reservations,
		outputKey:    func(id string) string { return "batches/" + id + "/output.jsonl" },
		errorKey:     func(id string) string { return "batches/" + id + "/errors.jsonl" },
	}, nil
}

// Run executes a single batch end-to-end. It is safe to invoke twice for the
// same batchID — the second run resumes from existing batch_lines rows.
func (e *Executor) Run(ctx context.Context, batchID string) error {
	batch, err := e.batches.LoadBatch(ctx, batchID)
	if err != nil {
		return fmt.Errorf("executor: load batch %s: %w", batchID, err)
	}

	prior, err := e.lines.LoadLines(ctx, batchID)
	if err != nil {
		return fmt.Errorf("executor: load batch_lines %s: %w", batchID, err)
	}
	priorByID := make(map[string]LineRow, len(prior))
	for _, r := range prior {
		priorByID[r.CustomID] = r
	}

	reader, err := e.storage.Download(ctx, e.bucket, batch.InputFilePath)
	if err != nil {
		return fmt.Errorf("executor: download input %s: %w", batch.InputFilePath, err)
	}
	defer reader.Close()

	scanCh := make(chan ScanResult, e.cfg.Concurrency*2)
	go ScanLines(ctx, reader, scanCh)

	in := make(chan InputLine, e.cfg.Concurrency*2)
	out := make(chan DispatchResult, e.cfg.Concurrency*2)
	output := &JSONLWriter{}
	errs := &JSONLWriter{}

	completed := 0
	failed := 0

	// Pre-seed counts from prior persisted rows so resume produces correct totals.
	// Output/error JSONL writers do NOT replay successful prior lines (those
	// are already uploaded as part of a previous run that crashed before
	// MarkCompleted; the partial files are overwritten on this run with the
	// remaining lines plus the prior content held in memory). Concretely: we
	// re-emit prior succeeded/failed lines into the in-memory writer so the
	// final upload contains every line.
	for _, r := range prior {
		switch r.Status {
		case "succeeded":
			completed++
			// Re-emit a placeholder row to preserve ordering — the actual
			// upstream response is gone (transient memory). We cannot reissue
			// the call without double-charging. Document in test 2: the
			// re-upload contains a sentinel response body marked
			// {"resumed":true}; restart-safety contract is that the line is
			// NOT re-charged and NOT re-dispatched.
			_, _ = output.Append(map[string]any{
				"id":        "batch_req_resumed_" + r.CustomID,
				"custom_id": r.CustomID,
				"response":  map[string]any{"status_code": 200, "request_id": "resumed", "body": map[string]any{"resumed": true}},
				"error":     nil,
			})
		case "failed":
			failed++
			_, _ = errs.Append(map[string]any{
				"id":        "batch_req_resumed_" + r.CustomID,
				"custom_id": r.CustomID,
				"response":  nil,
				"error":     map[string]any{"code": "resumed", "message": SanitizeMessage(r.LastError)},
			})
		}
	}

	// Producer: read scan results, skip already-settled lines, push to dispatcher.
	scanDone := make(chan struct{})
	scanErrs := make([]ScanResult, 0)
	go func() {
		defer close(in)
		for sr := range scanCh {
			if sr.Err != nil {
				scanErrs = append(scanErrs, sr)
				continue
			}
			line := sr.Line
			if prior, ok := priorByID[line.CustomID]; ok {
				if prior.Status == "succeeded" || prior.Status == "failed" {
					continue
				}
			}
			if err := e.lines.UpsertPending(ctx, batchID, line.CustomID); err != nil {
				log.Printf("executor: upsert pending %s/%s: %v", batchID, line.CustomID, err)
			}
			out := *line
			out.Alias = batch.ModelAlias
			select {
			case <-ctx.Done():
				return
			case in <- out:
			}
		}
		close(scanDone)
	}()

	// Worker pool runs in its own goroutine so we can drain results synchronously.
	poolDone := make(chan struct{})
	go func() {
		e.dispatcher.Pool(ctx, in, out)
		close(out)
		close(poolDone)
	}()

	// Consumer: collect results, persist line rows, append to writers.
	totalConsumed := big.NewInt(0)
	for res := range out {
		if res.Output != nil {
			idx, _ := output.Append(res.Output)
			if err := e.lines.MarkSucceeded(ctx, batchID, res.CustomID, res.Attempts, res.ConsumedCredits, idx); err != nil {
				log.Printf("executor: mark succeeded %s/%s: %v", batchID, res.CustomID, err)
			}
			completed++
			totalConsumed.Add(totalConsumed, big.NewInt(res.ConsumedCredits))
		} else if res.Error != nil {
			idx, _ := errs.Append(res.Error)
			if err := e.lines.MarkFailed(ctx, batchID, res.CustomID, res.Attempts, idx, res.Error.Error.Message); err != nil {
				log.Printf("executor: mark failed %s/%s: %v", batchID, res.CustomID, err)
			}
			failed++
		}
	}

	// Append malformed-JSON scan errors as error lines (no dispatch happened).
	for _, sr := range scanErrs {
		idx, _ := errs.Append(map[string]any{
			"id":        "batch_req_invalid",
			"custom_id": "",
			"response":  nil,
			"error":     map[string]any{"code": "invalid_json", "message": SanitizeMessage(sr.Err.Error())},
		})
		failed++
		_ = idx
	}

	// Add prior consumed credits from re-emitted succeeded lines. They are
	// already accounted for in batch_lines.consumed_credits — sum from DB.
	for _, r := range prior {
		if r.Status == "succeeded" {
			totalConsumed.Add(totalConsumed, big.NewInt(r.ConsumedCredits))
		}
	}

	// Finalize uploads.
	outputKey := e.outputKey(batchID)
	errorKey := e.errorKey(batchID)
	_, outputBytes, err := output.Finalize(ctx, e.storage, e.bucket, outputKey, false)
	if err != nil {
		return fmt.Errorf("executor: upload output: %w", err)
	}
	uploadedErrs, errorBytes, err := errs.Finalize(ctx, e.storage, e.bucket, errorKey, true)
	if err != nil {
		return fmt.Errorf("executor: upload errors: %w", err)
	}

	// Register output/error artifacts in public.files so the FK targets in
	// batches.output_file_id / error_file_id resolve to real rows.
	outputFileID, err := e.files.Register(ctx, batch.AccountID, "batch_output", "output.jsonl", outputKey, outputBytes)
	if err != nil {
		return fmt.Errorf("executor: register output file: %w", err)
	}
	errorFileID := ""
	if uploadedErrs {
		errorFileID, err = e.files.Register(ctx, batch.AccountID, "batch_output", "errors.jsonl", errorKey, errorBytes)
		if err != nil {
			return fmt.Errorf("executor: register error file: %w", err)
		}
	}

	// Settle credits.
	actual, overconsumed, err := Settle(SettleInput{
		ReservedCredits: batch.ReservedCredits,
		ConsumedCredits: totalConsumed,
	})
	if err != nil {
		return fmt.Errorf("executor: settle: %w", err)
	}
	if err := e.reservations.Settle(ctx, batchID, batch.AccountID, batch.ReservationID, actual, overconsumed, "completed"); err != nil {
		return fmt.Errorf("executor: settle reservation: %w", err)
	}

	if err := e.batches.MarkCompleted(ctx, batchID, completed, failed, outputFileID, errorFileID, overconsumed, time.Now().UTC()); err != nil {
		return fmt.Errorf("executor: mark completed: %w", err)
	}

	<-scanDone
	<-poolDone
	_ = io.EOF // silence import in case poolDone path skipped

	return nil
}
