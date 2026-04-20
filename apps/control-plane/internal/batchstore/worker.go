package batchstore

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/hivegpt/hive/apps/control-plane/internal/filestore"
)

// StorageUploader uploads content to blob storage for batch output files.
type StorageUploader interface {
	Upload(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) error
}

type FileService interface {
	CreateFile(ctx context.Context, accountID, purpose, filename string, bytes int64, storagePath string) (filestore.File, error)
	UpdateBatchStatus(ctx context.Context, batchID, status string, updates map[string]interface{}) error
}

// BatchWorker handles Asynq batch polling tasks.
// It polls upstream provider (LiteLLM) for batch completion, downloads output files,
// uploads them to S3, creates file metadata, and updates batch status.
type BatchWorker struct {
	fileService    FileService
	litellmBaseURL string
	litellmKey     string
	storage        StorageUploader
	bucket         string
	httpClient     *http.Client
}

// NewBatchWorker creates a new BatchWorker.
func NewBatchWorker(
	fileService FileService,
	litellmBaseURL string,
	litellmKey string,
	storage StorageUploader,
	bucket string,
) *BatchWorker {
	return &BatchWorker{
		fileService:    fileService,
		litellmBaseURL: strings.TrimRight(litellmBaseURL, "/"),
		litellmKey:     litellmKey,
		storage:        storage,
		bucket:         bucket,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}
}

// HandleBatchPoll processes a batch:poll Asynq task.
// It checks the upstream batch status and handles each terminal/non-terminal state.
func (w *BatchWorker) HandleBatchPoll(ctx context.Context, t *asynq.Task) error {
	var payload BatchPollPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("batchstore: unmarshal payload: %w", err)
	}

	// Poll upstream batch status.
	upstreamResp, err := w.fetchUpstreamBatch(ctx, payload.UpstreamBatchID)
	if err != nil {
		return fmt.Errorf("batchstore: poll upstream batch %s: %w", payload.UpstreamBatchID, err)
	}

	status, _ := upstreamResp["status"].(string)

	switch status {
	case "completed":
		return w.handleCompleted(ctx, payload, upstreamResp)
	case "in_progress", "validating", "finalizing":
		// Update request counts if available, then signal Asynq to retry.
		w.updateRequestCounts(ctx, payload.BatchID, upstreamResp)
		return fmt.Errorf("batch still in progress (status: %s)", status)
	case "failed":
		return w.handleFailed(ctx, payload)
	case "cancelled", "expired":
		return w.handleCancelled(ctx, payload, status)
	default:
		// Unknown status — retry.
		return fmt.Errorf("unknown upstream batch status: %q", status)
	}
}

// handleCompleted processes a completed upstream batch:
// downloads output/error files, uploads to S3, creates File records, updates batch status.
func (w *BatchWorker) handleCompleted(ctx context.Context, payload BatchPollPayload, upstreamResp map[string]interface{}) error {
	fields := map[string]interface{}{
		"completed_at": time.Now().UTC().Unix(),
	}

	// Download and store output file if present.
	if outputFileID, ok := upstreamResp["output_file_id"].(string); ok && outputFileID != "" {
		fileID, err := w.downloadAndStoreFile(ctx, payload.AccountID, outputFileID, "batch_output")
		if err != nil {
			log.Printf("batchstore: failed to store output file for batch %s: %v", payload.BatchID, err)
		} else {
			fields["output_file_id"] = fileID
		}
	}

	// Download and store error file if present.
	if errorFileID, ok := upstreamResp["error_file_id"].(string); ok && errorFileID != "" {
		fileID, err := w.downloadAndStoreFile(ctx, payload.AccountID, errorFileID, "batch_output")
		if err != nil {
			log.Printf("batchstore: failed to store error file for batch %s: %v", payload.BatchID, err)
		} else {
			fields["error_file_id"] = fileID
		}
	}

	// Propagate request counts from upstream response.
	if rc, ok := upstreamResp["request_counts"].(map[string]interface{}); ok {
		if total, ok := rc["total"].(float64); ok {
			fields["request_counts_total"] = int(total)
		}
		if completed, ok := rc["completed"].(float64); ok {
			fields["request_counts_completed"] = int(completed)
		}
		if failed, ok := rc["failed"].(float64); ok {
			fields["request_counts_failed"] = int(failed)
		}
	}

	if err := w.fileService.UpdateBatchStatus(ctx, payload.BatchID, "completed", fields); err != nil {
		return fmt.Errorf("batchstore: update batch status to completed: %w", err)
	}

	return nil
}

// handleFailed processes a failed upstream batch: releases credits and marks batch failed.
func (w *BatchWorker) handleFailed(ctx context.Context, payload BatchPollPayload) error {
	fields := map[string]interface{}{
		"failed_at": time.Now().UTC().Unix(),
	}
	if err := w.fileService.UpdateBatchStatus(ctx, payload.BatchID, "failed", fields); err != nil {
		return fmt.Errorf("batchstore: update batch status to failed: %w", err)
	}
	return nil
}

// handleCancelled processes a cancelled or expired upstream batch.
func (w *BatchWorker) handleCancelled(ctx context.Context, payload BatchPollPayload, status string) error {
	fields := map[string]interface{}{
		"cancelled_at": time.Now().UTC().Unix(),
	}
	if err := w.fileService.UpdateBatchStatus(ctx, payload.BatchID, status, fields); err != nil {
		return fmt.Errorf("batchstore: update batch status to %s: %w", status, err)
	}
	return nil
}

// updateRequestCounts updates request counts during in_progress polling.
func (w *BatchWorker) updateRequestCounts(ctx context.Context, batchID string, upstreamResp map[string]interface{}) {
	if rc, ok := upstreamResp["request_counts"].(map[string]interface{}); ok {
		fields := map[string]interface{}{}
		if total, ok := rc["total"].(float64); ok {
			fields["request_counts_total"] = int(total)
		}
		if completed, ok := rc["completed"].(float64); ok {
			fields["request_counts_completed"] = int(completed)
		}
		if failed, ok := rc["failed"].(float64); ok {
			fields["request_counts_failed"] = int(failed)
		}
		if len(fields) > 0 {
			if err := w.fileService.UpdateBatchStatus(ctx, batchID, "in_progress", fields); err != nil {
				log.Printf("batchstore: update request counts for batch %s: %v", batchID, err)
			}
		}
	}
}

// downloadAndStoreFile downloads an output file from upstream and stores it in S3 + filestore.
func (w *BatchWorker) downloadAndStoreFile(ctx context.Context, accountID, upstreamFileID, purpose string) (string, error) {
	if w.storage == nil {
		return "", fmt.Errorf("storage uploader not configured — cannot store output file %s", upstreamFileID)
	}

	reader, size, contentType, filename, err := w.downloadUpstreamFile(ctx, upstreamFileID)
	if err != nil {
		return "", fmt.Errorf("download upstream file %s: %w", upstreamFileID, err)
	}
	defer reader.Close()

	fileID := "file-" + uuid.New().String()
	storagePath := fmt.Sprintf("%s/%s/%s", accountID, fileID, filename)

	if err := w.storage.Upload(ctx, w.bucket, storagePath, reader, size, contentType); err != nil {
		return "", fmt.Errorf("upload to storage %s: %w", storagePath, err)
	}

	f, err := w.fileService.CreateFile(ctx, accountID, purpose, filename, size, storagePath)
	if err != nil {
		return "", fmt.Errorf("create file record: %w", err)
	}

	return f.ID, nil
}

// fetchUpstreamBatch retrieves the current batch status from LiteLLM.
func (w *BatchWorker) fetchUpstreamBatch(ctx context.Context, upstreamBatchID string) (map[string]interface{}, error) {
	url := fmt.Sprintf("%s/v1/batches/%s", w.litellmBaseURL, upstreamBatchID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.litellmKey)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// downloadUpstreamFile downloads the content of an upstream file and returns a reader + metadata.
func (w *BatchWorker) downloadUpstreamFile(ctx context.Context, upstreamFileID string) (io.ReadCloser, int64, string, string, error) {
	url := fmt.Sprintf("%s/v1/files/%s/content", w.litellmBaseURL, upstreamFileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, "", "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+w.litellmKey)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		return nil, 0, "", "", fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, 0, "", "", fmt.Errorf("status %d downloading file %s", resp.StatusCode, upstreamFileID)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/jsonl"
	}

	// Derive filename from upstream file ID with a default extension.
	filename := upstreamFileID + ".jsonl"

	return resp.Body, resp.ContentLength, contentType, filename, nil
}
