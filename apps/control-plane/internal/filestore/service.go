package filestore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// validBatchEndpoints lists the allowed batch API endpoints.
var validBatchEndpoints = map[string]bool{
	"/v1/chat/completions": true,
	"/v1/completions":      true,
	"/v1/embeddings":       true,
}

// Service wraps Repository and adds business logic for file, upload, and batch operations.
type Service struct {
	repo *Repository
}

// NewService creates a new Service.
func NewService(repo *Repository) *Service {
	return &Service{repo: repo}
}

// --- File operations ---

// CreateFile creates a new file metadata record with a generated ID and storage path.
func (s *Service) CreateFile(ctx context.Context, accountID, purpose, filename string, bytes int64, storagePath string) (File, error) {
	id := "file-" + uuid.New().String()
	now := time.Now().UTC()

	path := storagePath
	if path == "" {
		path = fmt.Sprintf("%s/%s/%s", accountID, id, filename)
	}

	f := File{
		ID:          id,
		AccountID:   accountID,
		Purpose:     purpose,
		Filename:    filename,
		Bytes:       bytes,
		Status:      "uploaded",
		StoragePath: path,
		CreatedAt:   now,
	}

	// Files used for batch expire after 30 days.
	if purpose == "batch" {
		expiry := now.Add(30 * 24 * time.Hour)
		f.ExpiresAt = &expiry
	}

	if err := s.repo.CreateFile(ctx, f); err != nil {
		return File{}, err
	}
	return f, nil
}

// GetFile retrieves a file by ID, validating account ownership.
func (s *Service) GetFile(ctx context.Context, id, accountID string) (*File, error) {
	return s.repo.GetFile(ctx, id, accountID)
}

// ListFiles retrieves all files for an account, optionally filtered by purpose.
func (s *Service) ListFiles(ctx context.Context, accountID string, purpose *string) ([]File, error) {
	return s.repo.ListFiles(ctx, accountID, purpose)
}

// DeleteFile removes a file record, validating account ownership.
func (s *Service) DeleteFile(ctx context.Context, id, accountID string) error {
	return s.repo.DeleteFile(ctx, id, accountID)
}

// --- Upload operations ---

// CreateUpload creates a new upload record with a generated ID.
func (s *Service) CreateUpload(ctx context.Context, accountID, filename string, bytes int64, mimeType, purpose string) (Upload, error) {
	id := "upload-" + uuid.New().String()
	now := time.Now().UTC()

	u := Upload{
		ID:          id,
		AccountID:   accountID,
		Filename:    filename,
		Bytes:       bytes,
		MimeType:    mimeType,
		Purpose:     purpose,
		Status:      "pending",
		StoragePath: fmt.Sprintf("%s/%s/%s", accountID, id, filename),
		CreatedAt:   now,
		ExpiresAt:   now.Add(1 * time.Hour),
	}

	if err := s.repo.CreateUpload(ctx, u); err != nil {
		return Upload{}, err
	}
	return u, nil
}

// GetUpload retrieves an upload by ID, validating account ownership.
func (s *Service) GetUpload(ctx context.Context, id, accountID string) (*Upload, error) {
	return s.repo.GetUpload(ctx, id, accountID)
}

// AddUploadPart records a part that has been uploaded for a multipart upload.
func (s *Service) AddUploadPart(ctx context.Context, uploadID, partID string, partNum int, etag string) (UploadPart, error) {
	p := UploadPart{
		ID:        partID,
		UploadID:  uploadID,
		PartNum:   partNum,
		ETag:      etag,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.repo.CreateUploadPart(ctx, p); err != nil {
		return UploadPart{}, err
	}
	return p, nil
}

// CompleteUpload transitions an upload to completed status and creates a File record.
// Returns the created File.
func (s *Service) CompleteUpload(ctx context.Context, uploadID, accountID string) (File, error) {
	upload, err := s.repo.GetUpload(ctx, uploadID, accountID)
	if err != nil {
		return File{}, err
	}

	if upload.Status != "pending" {
		return File{}, fmt.Errorf("filestore: upload %s is not pending (status: %s)", uploadID, upload.Status)
	}

	if err := s.repo.UpdateUploadStatus(ctx, uploadID, "completed", nil); err != nil {
		return File{}, err
	}

	// Create a corresponding File record from the upload.
	return s.CreateFile(ctx, upload.AccountID, upload.Purpose, upload.Filename, upload.Bytes, upload.StoragePath)
}

// CancelUpload transitions a pending upload to cancelled status.
func (s *Service) CancelUpload(ctx context.Context, uploadID, accountID string) error {
	upload, err := s.repo.GetUpload(ctx, uploadID, accountID)
	if err != nil {
		return err
	}

	if upload.Status != "pending" {
		return fmt.Errorf("filestore: upload %s is not pending (status: %s)", uploadID, upload.Status)
	}

	return s.repo.UpdateUploadStatus(ctx, uploadID, "cancelled", nil)
}

// --- Batch operations ---

// CreateBatch creates a new batch record with a generated ID and validates the endpoint.
func (s *Service) CreateBatch(ctx context.Context, accountID, inputFileID, endpoint, completionWindow string) (Batch, error) {
	if !validBatchEndpoints[endpoint] {
		return Batch{}, fmt.Errorf("filestore: endpoint %q is not allowed for batch; must be one of: %s",
			endpoint, strings.Join(validEndpointList(), ", "))
	}

	if completionWindow == "" {
		completionWindow = "24h"
	}

	id := "batch-" + uuid.New().String()
	now := time.Now().UTC()

	b := Batch{
		ID:               id,
		AccountID:        accountID,
		InputFileID:      inputFileID,
		Endpoint:         endpoint,
		CompletionWindow: completionWindow,
		Status:           "validating",
		CreatedAt:        now,
		ExpiresAt:        now.Add(24 * time.Hour),
	}

	if err := s.repo.CreateBatch(ctx, b); err != nil {
		return Batch{}, err
	}
	return b, nil
}

// GetBatch retrieves a batch by ID, validating account ownership.
func (s *Service) GetBatch(ctx context.Context, id, accountID string) (*Batch, error) {
	return s.repo.GetBatch(ctx, id, accountID)
}

// ListBatches retrieves batches for an account with cursor-based pagination.
func (s *Service) ListBatches(ctx context.Context, accountID string, limit int, after *string) ([]Batch, error) {
	return s.repo.ListBatches(ctx, accountID, limit, after)
}

// CancelBatch transitions a batch to cancelling (if in_progress) or cancelled (if validating).
func (s *Service) CancelBatch(ctx context.Context, batchID, accountID string) (*Batch, error) {
	batch, err := s.repo.GetBatch(ctx, batchID, accountID)
	if err != nil {
		return nil, err
	}

	newStatus := ""
	switch batch.Status {
	case "validating":
		newStatus = "cancelled"
	case "in_progress":
		newStatus = "cancelling"
	default:
		return nil, fmt.Errorf("filestore: batch %s cannot be cancelled (status: %s)", batchID, batch.Status)
	}

	if err := s.repo.UpdateBatchStatus(ctx, batchID, newStatus, nil); err != nil {
		return nil, err
	}

	batch.Status = newStatus
	return batch, nil
}

// UpdateBatchStatus updates a batch's status with optional additional field updates.
func (s *Service) UpdateBatchStatus(ctx context.Context, batchID, status string, updates map[string]interface{}) error {
	return s.repo.UpdateBatchStatus(ctx, batchID, status, updates)
}

func validEndpointList() []string {
	out := make([]string, 0, len(validBatchEndpoints))
	for k := range validBatchEndpoints {
		out = append(out, k)
	}
	return out
}
