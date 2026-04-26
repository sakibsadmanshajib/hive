package batchstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/batchstore/executor"
	"github.com/hivegpt/hive/apps/control-plane/internal/filestore"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BatchSnapshotLoader is the slice of filestore.Service the BatchStore adapter needs.
type BatchSnapshotLoader interface {
	GetBatchByID(ctx context.Context, id string) (*filestore.Batch, error)
	UpdateBatchStatus(ctx context.Context, batchID, status string, updates map[string]interface{}) error
}

// FileLookup resolves an input file ID to its storage path. The executor runs
// server-side and looks up the file by ID without account scoping.
type FileLookup interface {
	GetFileByID(ctx context.Context, id string) (*filestore.File, error)
}

// pgxBatchStore implements executor.BatchStore against the existing
// filestore.Service plus a direct pgx pool for the new executor-specific
// columns added in supabase/migrations/0015_batch_local_executor.sql.
type pgxBatchStore struct {
	loader BatchSnapshotLoader
	files  FileLookup
}

// NewPgxBatchStore wires the production BatchStore adapter.
func NewPgxBatchStore(loader BatchSnapshotLoader, files FileLookup) executor.BatchStore {
	return &pgxBatchStore{loader: loader, files: files}
}

func (p *pgxBatchStore) LoadBatch(ctx context.Context, batchID string) (executor.BatchSnapshot, error) {
	batch, err := p.loader.GetBatchByID(ctx, batchID)
	if err != nil {
		return executor.BatchSnapshot{}, fmt.Errorf("get batch: %w", err)
	}
	if batch == nil {
		return executor.BatchSnapshot{}, fmt.Errorf("batch %s not found", batchID)
	}
	file, err := p.files.GetFileByID(ctx, batch.InputFileID)
	if err != nil {
		return executor.BatchSnapshot{}, fmt.Errorf("get input file: %w", err)
	}
	if file == nil {
		return executor.BatchSnapshot{}, fmt.Errorf("input file %s not found", batch.InputFileID)
	}
	snap := executor.BatchSnapshot{
		ID:              batch.ID,
		AccountID:       batch.AccountID,
		InputFileID:     batch.InputFileID,
		InputFilePath:   file.StoragePath,
		Endpoint:        batch.Endpoint,
		ModelAlias:      batch.ModelAlias,
		ReservedCredits: batch.EstimatedCredits,
	}
	if batch.ReservationID != nil {
		snap.ReservationID = *batch.ReservationID
	}
	return snap, nil
}

func (p *pgxBatchStore) MarkCompleted(ctx context.Context, batchID string, completedLines, failedLines int, outputFileID, errorFileID string, overconsumed bool, completedAt time.Time) error {
	updates := map[string]interface{}{
		"completed_at":    completedAt.Unix(),
		"completed_lines": completedLines,
		"failed_lines":    failedLines,
		"overconsumed":    overconsumed,
	}
	if outputFileID != "" {
		updates["output_file_id"] = outputFileID
	}
	if errorFileID != "" {
		updates["error_file_id"] = errorFileID
	}
	return p.loader.UpdateBatchStatus(ctx, batchID, "completed", updates)
}

// pgxLineStore implements executor.LineStore against public.batch_lines.
type pgxLineStore struct {
	pool *pgxpool.Pool
}

// NewPgxLineStore wires the production LineStore.
func NewPgxLineStore(pool *pgxpool.Pool) executor.LineStore {
	return &pgxLineStore{pool: pool}
}

func (s *pgxLineStore) LoadLines(ctx context.Context, batchID string) ([]executor.LineRow, error) {
	rows, err := s.pool.Query(ctx, `
		select batch_id, custom_id, status, attempt, consumed_credits,
		       output_index, error_index, coalesce(last_error, '')
		from public.batch_lines
		where batch_id = $1`, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]executor.LineRow, 0)
	for rows.Next() {
		var r executor.LineRow
		var consumed float64
		var outIdx, errIdx *int
		if err := rows.Scan(&r.BatchID, &r.CustomID, &r.Status, &r.Attempt, &consumed, &outIdx, &errIdx, &r.LastError); err != nil {
			return nil, err
		}
		r.ConsumedCredits = int64(consumed)
		r.OutputIndex = outIdx
		r.ErrorIndex = errIdx
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *pgxLineStore) UpsertPending(ctx context.Context, batchID, customID string) error {
	_, err := s.pool.Exec(ctx, `
		insert into public.batch_lines (batch_id, custom_id, status)
		values ($1, $2, 'pending')
		on conflict (batch_id, custom_id) do nothing`, batchID, customID)
	return err
}

func (s *pgxLineStore) MarkSucceeded(ctx context.Context, batchID, customID string, attempt int, consumedCredits int64, outputIndex int) error {
	_, err := s.pool.Exec(ctx, `
		insert into public.batch_lines
		    (batch_id, custom_id, status, attempt, consumed_credits, output_index, completed_at)
		values ($1, $2, 'succeeded', $3, $4, $5, now())
		on conflict (batch_id, custom_id) do update
		set status = 'succeeded',
		    attempt = excluded.attempt,
		    consumed_credits = excluded.consumed_credits,
		    output_index = excluded.output_index,
		    completed_at = excluded.completed_at`,
		batchID, customID, attempt, consumedCredits, outputIndex)
	return err
}

func (s *pgxLineStore) MarkFailed(ctx context.Context, batchID, customID string, attempt int, errorIndex int, lastError string) error {
	_, err := s.pool.Exec(ctx, `
		insert into public.batch_lines
		    (batch_id, custom_id, status, attempt, error_index, last_error, completed_at)
		values ($1, $2, 'failed', $3, $4, $5, now())
		on conflict (batch_id, custom_id) do update
		set status = 'failed',
		    attempt = excluded.attempt,
		    error_index = excluded.error_index,
		    last_error = excluded.last_error,
		    completed_at = excluded.completed_at`,
		batchID, customID, attempt, errorIndex, lastError)
	return err
}

// AccountingReservationAdapter adapts accounting.Service to executor.ReservationPort.
type AccountingReservationAdapter struct {
	svc AccountingSettler
}

// NewAccountingReservationAdapter wires the production ReservationPort.
func NewAccountingReservationAdapter(svc AccountingSettler) executor.ReservationPort {
	return &AccountingReservationAdapter{svc: svc}
}

// Settle finalizes the reservation when actualCredits > 0; otherwise it
// releases the full reservation. Mirrors the existing settleTerminalReservation
// path in worker.go for the upstream-batch case.
func (a *AccountingReservationAdapter) Settle(ctx context.Context, batchID, accountID, reservationID string, actualCredits int64, overconsumed bool, terminalStatus string) error {
	if a.svc == nil {
		return fmt.Errorf("accounting settler not configured")
	}
	parsedAccount, err := uuid.Parse(strings.TrimSpace(accountID))
	if err != nil {
		return fmt.Errorf("parse account_id: %w", err)
	}
	parsedReservation, err := uuid.Parse(strings.TrimSpace(reservationID))
	if err != nil {
		return fmt.Errorf("parse reservation_id: %w", err)
	}
	if actualCredits > 0 {
		_, err := a.svc.FinalizeReservation(ctx, accounting.FinalizeReservationInput{
			AccountID:              parsedAccount,
			ReservationID:          parsedReservation,
			ActualCredits:          actualCredits,
			TerminalUsageConfirmed: true,
			Status:                 terminalStatus,
		})
		return err
	}
	_, err = a.svc.ReleaseReservation(ctx, accounting.ReleaseReservationInput{
		AccountID:     parsedAccount,
		ReservationID: parsedReservation,
		Reason:        "batch_completed_unused",
	})
	return err
}
