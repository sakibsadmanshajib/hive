package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("filestore: record not found")

// Repository handles all Postgres CRUD operations for files, uploads, upload_parts, and batches.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a repository over migration-managed filestore tables.
func NewRepository(pool *pgxpool.Pool) (*Repository, error) {
	return &Repository{pool: pool}, nil
}

// --- File methods ---

// CreateFile inserts a new file record.
func (r *Repository) CreateFile(ctx context.Context, f File) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO files (id, account_id, purpose, filename, bytes, status, storage_path, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, f.ID, f.AccountID, f.Purpose, f.Filename, f.Bytes, f.Status, f.StoragePath, f.CreatedAt, f.ExpiresAt)
	if err != nil {
		return fmt.Errorf("filestore: create file: %w", err)
	}
	return nil
}

// GetFile retrieves a file by ID and account ID.
func (r *Repository) GetFile(ctx context.Context, id, accountID string) (*File, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, purpose, filename, bytes, status, storage_path, created_at, expires_at
		FROM files
		WHERE id = $1 AND account_id = $2
	`, id, accountID)

	f, err := scanFile(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("filestore: get file: %w", err)
	}
	return &f, nil
}

// ListFiles retrieves all files for an account, optionally filtered by purpose.
func (r *Repository) ListFiles(ctx context.Context, accountID string, purpose *string) ([]File, error) {
	var rows pgx.Rows
	var err error

	if purpose != nil {
		rows, err = r.pool.Query(ctx, `
			SELECT id, account_id, purpose, filename, bytes, status, storage_path, created_at, expires_at
			FROM files
			WHERE account_id = $1 AND purpose = $2
			ORDER BY created_at DESC
		`, accountID, *purpose)
	} else {
		rows, err = r.pool.Query(ctx, `
			SELECT id, account_id, purpose, filename, bytes, status, storage_path, created_at, expires_at
			FROM files
			WHERE account_id = $1
			ORDER BY created_at DESC
		`, accountID)
	}
	if err != nil {
		return nil, fmt.Errorf("filestore: list files: %w", err)
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("filestore: iterate files: %w", err)
	}
	return files, nil
}

// DeleteFile removes a file by ID and account ID.
func (r *Repository) DeleteFile(ctx context.Context, id, accountID string) error {
	result, err := r.pool.Exec(ctx, `
		DELETE FROM files WHERE id = $1 AND account_id = $2
	`, id, accountID)
	if err != nil {
		return fmt.Errorf("filestore: delete file: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Upload methods ---

// CreateUpload inserts a new upload record.
func (r *Repository) CreateUpload(ctx context.Context, u Upload) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO uploads (id, account_id, filename, bytes, mime_type, purpose, status, s3_upload_id, storage_path, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, u.ID, u.AccountID, u.Filename, u.Bytes, u.MimeType, u.Purpose, u.Status, u.S3UploadID, u.StoragePath, u.CreatedAt, u.ExpiresAt)
	if err != nil {
		return fmt.Errorf("filestore: create upload: %w", err)
	}
	return nil
}

// GetUpload retrieves an upload by ID and account ID.
func (r *Repository) GetUpload(ctx context.Context, id, accountID string) (*Upload, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, filename, bytes, mime_type, purpose, status, s3_upload_id, storage_path, created_at, expires_at
		FROM uploads
		WHERE id = $1 AND account_id = $2
	`, id, accountID)

	u, err := scanUpload(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("filestore: get upload: %w", err)
	}
	return &u, nil
}

// UpdateUploadStatus transitions an upload's status and optionally links the completed file.
func (r *Repository) UpdateUploadStatus(ctx context.Context, id, status string, fileID *string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE uploads SET status = $1 WHERE id = $2
	`, status, id)
	if err != nil {
		return fmt.Errorf("filestore: update upload status: %w", err)
	}
	return nil
}

// CreateUploadPart inserts a new upload part record.
func (r *Repository) CreateUploadPart(ctx context.Context, p UploadPart) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO upload_parts (id, upload_id, part_num, etag, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, p.ID, p.UploadID, p.PartNum, p.ETag, p.CreatedAt)
	if err != nil {
		return fmt.Errorf("filestore: create upload part: %w", err)
	}
	return nil
}

// ListUploadParts retrieves all parts for a given upload, ordered by part number.
func (r *Repository) ListUploadParts(ctx context.Context, uploadID string) ([]UploadPart, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, upload_id, part_num, etag, created_at
		FROM upload_parts
		WHERE upload_id = $1
		ORDER BY part_num ASC
	`, uploadID)
	if err != nil {
		return nil, fmt.Errorf("filestore: list upload parts: %w", err)
	}
	defer rows.Close()

	var parts []UploadPart
	for rows.Next() {
		var p UploadPart
		if err := rows.Scan(&p.ID, &p.UploadID, &p.PartNum, &p.ETag, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("filestore: scan upload part: %w", err)
		}
		parts = append(parts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("filestore: iterate upload parts: %w", err)
	}
	return parts, nil
}

// --- Batch methods ---

// CreateBatch inserts a new batch record.
func (r *Repository) CreateBatch(ctx context.Context, b Batch) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO batches (
			id, account_id, input_file_id, output_file_id, error_file_id,
			endpoint, completion_window, status, provider, upstream_batch_id, reservation_id, api_key_id, model_alias,
			estimated_credits, actual_credits,
			request_counts_total, request_counts_completed, request_counts_failed,
			created_at, in_progress_at, completed_at, failed_at, cancelled_at, expires_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11, $12, $13,
			$14, $15,
			$16, $17, $18,
			$19, $20, $21, $22, $23, $24
		)
	`, b.ID, b.AccountID, b.InputFileID, b.OutputFileID, b.ErrorFileID,
		b.Endpoint, b.CompletionWindow, b.Status, b.Provider, b.UpstreamBatchID, b.ReservationID, b.APIKeyID, b.ModelAlias,
		b.EstimatedCredits, b.ActualCredits,
		b.RequestCountsTotal, b.RequestCountsCompleted, b.RequestCountsFailed,
		b.CreatedAt, b.InProgressAt, b.CompletedAt, b.FailedAt, b.CancelledAt, b.ExpiresAt)
	if err != nil {
		return fmt.Errorf("filestore: create batch: %w", err)
	}
	return nil
}

// GetBatchByID retrieves a batch by ID without an account-id scope check —
// for server-side workers (e.g., the local batch executor) that need to load
// a batch row before they know which account owns it.
func (r *Repository) GetBatchByID(ctx context.Context, id string) (*Batch, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, input_file_id, output_file_id, error_file_id,
		       endpoint, completion_window, status, provider, upstream_batch_id, reservation_id, api_key_id, model_alias,
		       estimated_credits, actual_credits,
		       request_counts_total, request_counts_completed, request_counts_failed,
		       created_at, in_progress_at, completed_at, failed_at, cancelled_at, expires_at
		FROM batches
		WHERE id = $1
	`, id)
	b, err := scanBatch(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("filestore: get batch by id: %w", err)
	}
	return &b, nil
}

// GetFileByID retrieves a file by ID without an account-id scope check —
// counterpart to GetBatchByID for the local batch executor.
func (r *Repository) GetFileByID(ctx context.Context, id string) (*File, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, purpose, filename, bytes, status, storage_path, created_at, expires_at
		FROM files
		WHERE id = $1
	`, id)
	f, err := scanFile(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("filestore: get file by id: %w", err)
	}
	return &f, nil
}

// GetBatch retrieves a batch by ID and account ID.
func (r *Repository) GetBatch(ctx context.Context, id, accountID string) (*Batch, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, input_file_id, output_file_id, error_file_id,
		       endpoint, completion_window, status, provider, upstream_batch_id, reservation_id, api_key_id, model_alias,
		       estimated_credits, actual_credits,
		       request_counts_total, request_counts_completed, request_counts_failed,
		       created_at, in_progress_at, completed_at, failed_at, cancelled_at, expires_at
		FROM batches
		WHERE id = $1 AND account_id = $2
	`, id, accountID)

	b, err := scanBatch(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("filestore: get batch: %w", err)
	}
	return &b, nil
}

// ListBatches retrieves batches for an account with cursor-based pagination.
func (r *Repository) ListBatches(ctx context.Context, accountID string, limit int, after *string) ([]Batch, error) {
	if limit <= 0 {
		limit = 20
	}

	var rows pgx.Rows
	var err error

	if after != nil {
		rows, err = r.pool.Query(ctx, `
			SELECT id, account_id, input_file_id, output_file_id, error_file_id,
			       endpoint, completion_window, status, provider, upstream_batch_id, reservation_id, api_key_id, model_alias,
			       estimated_credits, actual_credits,
			       request_counts_total, request_counts_completed, request_counts_failed,
			       created_at, in_progress_at, completed_at, failed_at, cancelled_at, expires_at
			FROM batches
			WHERE account_id = $1 AND created_at < (SELECT created_at FROM batches WHERE id = $2)
			ORDER BY created_at DESC
			LIMIT $3
		`, accountID, *after, limit)
	} else {
		rows, err = r.pool.Query(ctx, `
			SELECT id, account_id, input_file_id, output_file_id, error_file_id,
			       endpoint, completion_window, status, provider, upstream_batch_id, reservation_id, api_key_id, model_alias,
			       estimated_credits, actual_credits,
			       request_counts_total, request_counts_completed, request_counts_failed,
			       created_at, in_progress_at, completed_at, failed_at, cancelled_at, expires_at
			FROM batches
			WHERE account_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		`, accountID, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("filestore: list batches: %w", err)
	}
	defer rows.Close()

	var batches []Batch
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		batches = append(batches, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("filestore: iterate batches: %w", err)
	}
	return batches, nil
}

// UpdateBatchStatus updates a batch's status and any additional fields provided.
func (r *Repository) UpdateBatchStatus(ctx context.Context, id, status string, updates map[string]interface{}) error {
	assignments := []string{"status = $1"}
	args := []interface{}{status}

	keys := make([]string, 0, len(updates))
	for key := range updates {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		spec, ok := allowedBatchStatusUpdateFields[key]
		if !ok {
			return fmt.Errorf("filestore: unsupported batch update field %q", key)
		}

		value, err := normalizeBatchUpdateValue(key, updates[key], spec.kind)
		if err != nil {
			return err
		}

		args = append(args, value)
		assignments = append(assignments, fmt.Sprintf("%s = $%d", spec.column, len(args)))
	}

	args = append(args, id)
	query := fmt.Sprintf("UPDATE batches SET %s WHERE id = $%d", strings.Join(assignments, ", "), len(args))
	_, err := r.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("filestore: update batch status: %w", err)
	}
	return nil
}

type batchUpdateKind int

const (
	batchUpdateString batchUpdateKind = iota
	batchUpdateInteger
	batchUpdateTimestamp
	batchUpdateBool
)

type batchStatusUpdateField struct {
	column string
	kind   batchUpdateKind
}

var allowedBatchStatusUpdateFields = map[string]batchStatusUpdateField{
	"upstream_batch_id":        {column: "upstream_batch_id", kind: batchUpdateString},
	"reservation_id":           {column: "reservation_id", kind: batchUpdateString},
	"output_file_id":           {column: "output_file_id", kind: batchUpdateString},
	"error_file_id":            {column: "error_file_id", kind: batchUpdateString},
	"request_counts_total":     {column: "request_counts_total", kind: batchUpdateInteger},
	"request_counts_completed": {column: "request_counts_completed", kind: batchUpdateInteger},
	"request_counts_failed":    {column: "request_counts_failed", kind: batchUpdateInteger},
	"actual_credits":           {column: "actual_credits", kind: batchUpdateInteger},
	"in_progress_at":           {column: "in_progress_at", kind: batchUpdateTimestamp},
	"completed_at":             {column: "completed_at", kind: batchUpdateTimestamp},
	"failed_at":                {column: "failed_at", kind: batchUpdateTimestamp},
	"cancelled_at":             {column: "cancelled_at", kind: batchUpdateTimestamp},
	// Phase 15 — local batch executor columns (migration 20260427_01).
	"executor_kind":   {column: "executor_kind", kind: batchUpdateString},
	"completed_lines": {column: "completed_lines", kind: batchUpdateInteger},
	"failed_lines":    {column: "failed_lines", kind: batchUpdateInteger},
	"overconsumed":    {column: "overconsumed", kind: batchUpdateBool},
}

func normalizeBatchUpdateValue(field string, value interface{}, kind batchUpdateKind) (interface{}, error) {
	switch kind {
	case batchUpdateString:
		if value == nil {
			return nil, nil
		}
		if text, ok := value.(string); ok {
			return text, nil
		}
		return nil, fmt.Errorf("filestore: invalid batch string field %s: %T", field, value)
	case batchUpdateInteger:
		value, err := batchUpdateInt64(value)
		if err != nil {
			return nil, fmt.Errorf("filestore: invalid batch integer field %s: %w", field, err)
		}
		return value, nil
	case batchUpdateTimestamp:
		seconds, err := batchUpdateInt64(value)
		if err != nil {
			return nil, fmt.Errorf("filestore: invalid batch timestamp field %s: %w", field, err)
		}
		return time.Unix(seconds, 0).UTC(), nil
	case batchUpdateBool:
		if value == nil {
			return nil, nil
		}
		if b, ok := value.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("filestore: invalid batch bool field %s: %T", field, value)
	default:
		return nil, fmt.Errorf("filestore: unsupported batch update field %s", field)
	}
}

func batchUpdateInt64(value interface{}) (int64, error) {
	switch v := value.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		if math.Trunc(v) != v {
			return 0, fmt.Errorf("non-integer number %v", v)
		}
		return int64(v), nil
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, nil
		}
		f, err := v.Float64()
		if err != nil || math.Trunc(f) != f {
			return 0, fmt.Errorf("invalid number %q", v.String())
		}
		return int64(f), nil
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

// --- Scanners ---

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFile(s rowScanner) (File, error) {
	var f File
	if err := s.Scan(
		&f.ID, &f.AccountID, &f.Purpose, &f.Filename,
		&f.Bytes, &f.Status, &f.StoragePath, &f.CreatedAt, &f.ExpiresAt,
	); err != nil {
		return File{}, err
	}
	return f, nil
}

func scanUpload(s rowScanner) (Upload, error) {
	var u Upload
	if err := s.Scan(
		&u.ID, &u.AccountID, &u.Filename, &u.Bytes, &u.MimeType,
		&u.Purpose, &u.Status, &u.S3UploadID, &u.StoragePath,
		&u.CreatedAt, &u.ExpiresAt,
	); err != nil {
		return Upload{}, err
	}
	return u, nil
}

func scanBatch(s rowScanner) (Batch, error) {
	var b Batch
	if err := s.Scan(
		&b.ID, &b.AccountID, &b.InputFileID, &b.OutputFileID, &b.ErrorFileID,
		&b.Endpoint, &b.CompletionWindow, &b.Status, &b.Provider, &b.UpstreamBatchID, &b.ReservationID, &b.APIKeyID, &b.ModelAlias,
		&b.EstimatedCredits, &b.ActualCredits,
		&b.RequestCountsTotal, &b.RequestCountsCompleted, &b.RequestCountsFailed,
		&b.CreatedAt, &b.InProgressAt, &b.CompletedAt, &b.FailedAt, &b.CancelledAt, &b.ExpiresAt,
	); err != nil {
		return Batch{}, err
	}
	return b, nil
}
