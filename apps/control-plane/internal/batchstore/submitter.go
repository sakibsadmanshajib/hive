package batchstore

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/filestore"
	"github.com/hivegpt/hive/apps/control-plane/internal/routing"
)

type BatchFileStore interface {
	GetFile(ctx context.Context, id, accountID string) (*filestore.File, error)
	UpdateBatchStatus(ctx context.Context, batchID, status string, updates map[string]interface{}) error
}

type BatchRouteSelector interface {
	SelectRoute(ctx context.Context, input routing.SelectionInput) (routing.SelectionResult, error)
}

type BatchInputStorage interface {
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)
}

type BatchTaskQueue interface {
	Enqueue(ctx context.Context, payload BatchPollPayload) error
}

type BatchReservationReleaser interface {
	ReleaseReservation(ctx context.Context, input accounting.ReleaseReservationInput) (accounting.Reservation, error)
}

type Submitter struct {
	files          BatchFileStore
	routing        BatchRouteSelector
	storage        BatchInputStorage
	queue          BatchTaskQueue
	accounting     BatchReservationReleaser
	litellmBaseURL string
	litellmKey     string
	bucket         string
	httpClient     *http.Client
	now            func() time.Time
}

func NewSubmitter(
	files BatchFileStore,
	routing BatchRouteSelector,
	storage BatchInputStorage,
	queue BatchTaskQueue,
	accounting BatchReservationReleaser,
	litellmBaseURL, litellmKey, bucket string,
) *Submitter {
	return &Submitter{
		files:          files,
		routing:        routing,
		storage:        storage,
		queue:          queue,
		accounting:     accounting,
		litellmBaseURL: strings.TrimRight(litellmBaseURL, "/"),
		litellmKey:     litellmKey,
		bucket:         bucket,
		httpClient:     &http.Client{Timeout: 60 * time.Second},
		now:            time.Now,
	}
}

func (s *Submitter) SubmitBatch(ctx context.Context, batch filestore.Batch) (filestore.Batch, error) {
	inputFile, err := s.files.GetFile(ctx, batch.InputFileID, batch.AccountID)
	if err != nil {
		return s.failSubmission(ctx, batch, fmt.Errorf("load input file metadata: %w", err))
	}

	route, err := s.routing.SelectRoute(ctx, routing.SelectionInput{
		AliasID:   batch.ModelAlias,
		NeedBatch: true,
	})
	if err != nil {
		return s.failSubmission(ctx, batch, fmt.Errorf("select batch route: %w", err))
	}

	upstreamFileID, err := s.uploadUpstreamBatchFile(ctx, inputFile, route)
	if err != nil {
		return s.failSubmission(ctx, batch, fmt.Errorf("upload upstream batch file: %w", err))
	}

	upstreamBatch, err := s.createUpstreamBatch(ctx, upstreamFileID, batch.Endpoint, batch.CompletionWindow)
	if err != nil {
		return s.failSubmission(ctx, batch, fmt.Errorf("create upstream batch: %w", err))
	}

	updates := map[string]interface{}{
		"upstream_batch_id":        upstreamBatch.ID,
		"request_counts_total":     upstreamBatch.RequestCounts.Total,
		"request_counts_completed": upstreamBatch.RequestCounts.Completed,
		"request_counts_failed":    upstreamBatch.RequestCounts.Failed,
	}
	if strings.EqualFold(upstreamBatch.Status, "in_progress") {
		updates["in_progress_at"] = s.now().UTC().Unix()
	}

	if err := s.files.UpdateBatchStatus(ctx, batch.ID, upstreamBatch.Status, updates); err != nil {
		return batch, fmt.Errorf("batchstore: update submitted batch: %w", err)
	}

	payload := BatchPollPayload{
		BatchID:          batch.ID,
		AccountID:        batch.AccountID,
		ReservationID:    stringPointerValue(batch.ReservationID),
		UpstreamBatchID:  upstreamBatch.ID,
		Provider:         route.Provider,
		InputFileID:      batch.InputFileID,
		Endpoint:         batch.Endpoint,
		APIKeyID:         stringPointerValue(batch.APIKeyID),
		ModelAlias:       batch.ModelAlias,
		EstimatedCredits: batch.EstimatedCredits,
	}
	if err := s.queue.Enqueue(ctx, payload); err != nil {
		return batch, fmt.Errorf("batchstore: enqueue batch poll: %w", err)
	}

	batch.Status = upstreamBatch.Status
	batch.UpstreamBatchID = stringPointer(upstreamBatch.ID)
	batch.RequestCountsTotal = upstreamBatch.RequestCounts.Total
	batch.RequestCountsCompleted = upstreamBatch.RequestCounts.Completed
	batch.RequestCountsFailed = upstreamBatch.RequestCounts.Failed
	if ts, ok := updates["in_progress_at"]; ok {
		seconds := ts.(int64)
		inProgressAt := time.Unix(seconds, 0).UTC()
		batch.InProgressAt = &inProgressAt
	}
	return batch, nil
}

func (s *Submitter) uploadUpstreamBatchFile(ctx context.Context, inputFile *filestore.File, route routing.SelectionResult) (string, error) {
	reader, err := s.storage.Download(ctx, s.bucket, inputFile.StoragePath)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer mw.Close()

		if err := mw.WriteField("purpose", "batch"); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if strings.TrimSpace(route.Provider) != "" {
			if err := mw.WriteField("custom_llm_provider", route.Provider); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}

		fileWriter, err := mw.CreateFormFile("file", inputFile.Filename)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if err := rewriteBatchJSONL(reader, fileWriter, route.LiteLLMModelName); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.litellmBaseURL+"/v1/files", pr)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.litellmKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var uploaded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &uploaded); err != nil {
		return "", fmt.Errorf("decode upstream file response: %w", err)
	}
	if strings.TrimSpace(uploaded.ID) == "" {
		return "", fmt.Errorf("missing upstream file id")
	}
	return uploaded.ID, nil
}

func (s *Submitter) createUpstreamBatch(ctx context.Context, inputFileID, endpoint, completionWindow string) (upstreamBatchResponse, error) {
	payload := map[string]any{
		"input_file_id":     inputFileID,
		"endpoint":          endpoint,
		"completion_window": completionWindow,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return upstreamBatchResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.litellmBaseURL+"/v1/batches", strings.NewReader(string(body)))
	if err != nil {
		return upstreamBatchResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+s.litellmKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return upstreamBatchResponse{}, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return upstreamBatchResponse{}, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var upstreamBatch upstreamBatchResponse
	if err := json.Unmarshal(respBody, &upstreamBatch); err != nil {
		return upstreamBatchResponse{}, fmt.Errorf("decode upstream batch response: %w", err)
	}
	if strings.TrimSpace(upstreamBatch.ID) == "" {
		return upstreamBatchResponse{}, fmt.Errorf("missing upstream batch id")
	}
	if strings.TrimSpace(upstreamBatch.Status) == "" {
		upstreamBatch.Status = "validating"
	}
	return upstreamBatch, nil
}

func rewriteBatchJSONL(input io.Reader, output io.Writer, litellmModel string) error {
	reader := bufio.NewReader(input)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return err
		}

		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" {
			var payload map[string]any
			if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
				return fmt.Errorf("decode batch jsonl line: %w", err)
			}
			body, ok := payload["body"].(map[string]any)
			if !ok {
				return fmt.Errorf("batch jsonl line missing body object")
			}
			body["model"] = litellmModel
			encoded, err := json.Marshal(payload)
			if err != nil {
				return fmt.Errorf("encode batch jsonl line: %w", err)
			}
			if _, err := output.Write(encoded); err != nil {
				return err
			}
			if _, err := output.Write([]byte("\n")); err != nil {
				return err
			}
		}

		if err == io.EOF {
			break
		}
	}
	return nil
}

func (s *Submitter) failSubmission(ctx context.Context, batch filestore.Batch, err error) (filestore.Batch, error) {
	failedAt := s.now().UTC().Unix()
	_ = s.files.UpdateBatchStatus(ctx, batch.ID, "failed", map[string]interface{}{
		"failed_at": failedAt,
	})

	accountID := strings.TrimSpace(batch.AccountID)
	reservationID := stringPointerValue(batch.ReservationID)
	if s.accounting != nil && accountID != "" && reservationID != "" {
		parsedAccountID, accountErr := uuid.Parse(accountID)
		parsedReservationID, reservationErr := uuid.Parse(reservationID)
		if accountErr == nil && reservationErr == nil {
			_, _ = s.accounting.ReleaseReservation(ctx, accounting.ReleaseReservationInput{
				AccountID:     parsedAccountID,
				ReservationID: parsedReservationID,
				Reason:        "batch_submission_failed",
			})
		}
	}
	return batch, err
}

type upstreamBatchResponse struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	RequestCounts struct {
		Total     int `json:"total"`
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
	} `json:"request_counts"`
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func stringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	copy := value
	return &copy
}
