package batchstore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/filestore"
)

type batchFileService interface {
	CreateFile(ctx context.Context, accountID, purpose, filename string, bytes int64, storagePath string) (filestore.File, error)
	GetBatch(ctx context.Context, id, accountID string) (*filestore.Batch, error)
	UpdateBatchStatus(ctx context.Context, batchID, status string, updates map[string]interface{}) error
}

func TestBatchWorkerStoresCompletedOutputFiles(t *testing.T) {
	accountID := uuid.New().String()
	reservationID := uuid.New().String()
	apiKeyID := uuid.New().String()

	fileSvc := &fakeBatchFileService{
		getBatch: &filestore.Batch{
			ID:               "batch-1",
			AccountID:        accountID,
			InputFileID:      "file-input",
			Endpoint:         "/v1/chat/completions",
			ReservationID:    &reservationID,
			APIKeyID:         &apiKeyID,
			ModelAlias:       "hive-fast",
			EstimatedCredits: 3000,
		},
		fileIDsByFilename: map[string]string{
			"file-out.jsonl": "stored-output-file",
			"file-err.jsonl": "stored-error-file",
		},
	}
	var _ batchFileService = fileSvc

	storage := &fakeStorageUploader{}
	settler := &fakeAccountingSettler{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/batches/upstream-1":
			writeJSON(t, w, map[string]interface{}{
				"status":         "completed",
				"output_file_id": "file-out",
				"error_file_id":  "file-err",
				"request_counts": map[string]int{
					"total":     3,
					"completed": 2,
					"failed":    1,
				},
			})
		case "/v1/files/file-out/content":
			w.Header().Set("Content-Type", "application/jsonl")
			_, _ = io.WriteString(w, "{\"ok\":true}\n")
		case "/v1/files/file-err/content":
			w.Header().Set("Content-Type", "application/jsonl")
			_, _ = io.WriteString(w, "{\"error\":true}\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	worker := NewBatchWorker(fileSvc, server.URL, "litellm-key", storage, "hive-files", settler)
	worker.httpClient = server.Client()

	payload := BatchPollPayload{
		BatchID:          "batch-1",
		AccountID:        accountID,
		ReservationID:    reservationID,
		UpstreamBatchID:  "upstream-1",
		Provider:         "openrouter",
		InputFileID:      "file-input",
		Endpoint:         "/v1/chat/completions",
		APIKeyID:         apiKeyID,
		ModelAlias:       "hive-fast",
		EstimatedCredits: 3000,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if err := worker.HandleBatchPoll(context.Background(), asynq.NewTask(TypeBatchPoll, body)); err != nil {
		t.Fatalf("HandleBatchPoll returned error: %v", err)
	}

	if len(storage.uploads) != 2 {
		t.Fatalf("expected two storage uploads, got %d", len(storage.uploads))
	}
	assertUpload(t, storage.uploads[0], "hive-files", "application/jsonl", "{\"ok\":true}")
	assertUpload(t, storage.uploads[1], "hive-files", "application/jsonl", "{\"error\":true}")

	if len(fileSvc.createCalls) != 2 {
		t.Fatalf("expected two CreateFile calls, got %d", len(fileSvc.createCalls))
	}
	if fileSvc.createCalls[0].purpose != "batch_output" {
		t.Fatalf("expected output file purpose batch_output, got %q", fileSvc.createCalls[0].purpose)
	}
	if fileSvc.createCalls[1].purpose != "batch_output" {
		t.Fatalf("expected error file purpose batch_output, got %q", fileSvc.createCalls[1].purpose)
	}

	if len(settler.finalizeCalls) != 1 {
		t.Fatalf("expected one finalize call, got %#v", settler.finalizeCalls)
	}
	if len(settler.releaseCalls) != 0 {
		t.Fatalf("expected zero release calls, got %#v", settler.releaseCalls)
	}
	finalize := settler.finalizeCalls[0]
	if finalize.ActualCredits != 2000 || finalize.Status != "completed" || !finalize.TerminalUsageConfirmed {
		t.Fatalf("unexpected finalize input: %#v", finalize)
	}

	if len(fileSvc.updateCalls) != 1 {
		t.Fatalf("expected one UpdateBatchStatus call, got %d", len(fileSvc.updateCalls))
	}
	update := fileSvc.updateCalls[0]
	if update.batchID != "batch-1" || update.status != "completed" {
		t.Fatalf("expected completed update for batch-1, got batch=%q status=%q", update.batchID, update.status)
	}
	assertUpdateField(t, update.fields, "output_file_id", "stored-output-file")
	assertUpdateField(t, update.fields, "error_file_id", "stored-error-file")
	assertUpdateField(t, update.fields, "request_counts_total", int64(3))
	assertUpdateField(t, update.fields, "request_counts_completed", int64(2))
	assertUpdateField(t, update.fields, "request_counts_failed", int64(1))
	assertUpdateField(t, update.fields, "actual_credits", int64(2000))
}

func TestBatchWorkerFinalizesCancelledPartialBatches(t *testing.T) {
	accountID := uuid.New().String()
	reservationID := uuid.New().String()

	fileSvc := &fakeBatchFileService{
		getBatch: &filestore.Batch{
			ID:               "batch-2",
			AccountID:        accountID,
			InputFileID:      "file-input",
			Endpoint:         "/v1/chat/completions",
			ReservationID:    &reservationID,
			ModelAlias:       "hive-fast",
			EstimatedCredits: 2000,
		},
	}
	settler := &fakeAccountingSettler{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/batches/upstream-2" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]interface{}{
			"status": "cancelled",
			"request_counts": map[string]int{
				"total":     2,
				"completed": 1,
				"failed":    1,
			},
		})
	}))
	defer server.Close()

	worker := NewBatchWorker(fileSvc, server.URL, "litellm-key", &fakeStorageUploader{}, "hive-files", settler)
	worker.httpClient = server.Client()

	payload := BatchPollPayload{
		BatchID:          "batch-2",
		AccountID:        accountID,
		ReservationID:    reservationID,
		UpstreamBatchID:  "upstream-2",
		Endpoint:         "/v1/chat/completions",
		ModelAlias:       "hive-fast",
		EstimatedCredits: 2000,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if err := worker.HandleBatchPoll(context.Background(), asynq.NewTask(TypeBatchPoll, body)); err != nil {
		t.Fatalf("HandleBatchPoll returned error: %v", err)
	}

	if len(settler.finalizeCalls) != 1 {
		t.Fatalf("expected one finalize call, got %#v", settler.finalizeCalls)
	}
	if got := settler.finalizeCalls[0]; got.ActualCredits != 1000 || got.Status != "cancelled" || !got.TerminalUsageConfirmed {
		t.Fatalf("unexpected finalize input: %#v", got)
	}
	if len(settler.releaseCalls) != 0 {
		t.Fatalf("expected zero release calls, got %#v", settler.releaseCalls)
	}

	if len(fileSvc.updateCalls) != 1 {
		t.Fatalf("expected one update call, got %d", len(fileSvc.updateCalls))
	}
	update := fileSvc.updateCalls[0]
	if update.status != "cancelled" {
		t.Fatalf("expected cancelled status update, got %q", update.status)
	}
	assertUpdateField(t, update.fields, "actual_credits", int64(1000))
}

func TestBatchWorkerReleasesFailedCancelledAndExpiredReservations(t *testing.T) {
	tests := []struct {
		name   string
		status string
		reason string
	}{
		{name: "failed", status: "failed", reason: "batch_failed"},
		{name: "cancelled", status: "cancelled", reason: "batch_cancelled"},
		{name: "expired", status: "expired", reason: "batch_expired"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			accountID := uuid.New().String()
			reservationID := uuid.New().String()

			fileSvc := &fakeBatchFileService{
				getBatch: &filestore.Batch{
					ID:               "batch-" + tc.name,
					AccountID:        accountID,
					Endpoint:         "/v1/chat/completions",
					ReservationID:    &reservationID,
					ModelAlias:       "hive-fast",
					EstimatedCredits: 1000,
				},
			}
			settler := &fakeAccountingSettler{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/batches/upstream-"+tc.name {
					http.NotFound(w, r)
					return
				}
				writeJSON(t, w, map[string]interface{}{"status": tc.status})
			}))
			defer server.Close()

			worker := NewBatchWorker(fileSvc, server.URL, "litellm-key", &fakeStorageUploader{}, "hive-files", settler)
			worker.httpClient = server.Client()

			payload := BatchPollPayload{
				BatchID:          "batch-" + tc.name,
				AccountID:        accountID,
				ReservationID:    reservationID,
				UpstreamBatchID:  "upstream-" + tc.name,
				Endpoint:         "/v1/chat/completions",
				ModelAlias:       "hive-fast",
				EstimatedCredits: 1000,
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}

			if err := worker.HandleBatchPoll(context.Background(), asynq.NewTask(TypeBatchPoll, body)); err != nil {
				t.Fatalf("HandleBatchPoll returned error: %v", err)
			}

			if len(settler.finalizeCalls) != 0 {
				t.Fatalf("expected zero finalize calls, got %#v", settler.finalizeCalls)
			}
			if len(settler.releaseCalls) != 1 {
				t.Fatalf("expected one release call, got %#v", settler.releaseCalls)
			}
			if got := settler.releaseCalls[0]; got.Reason != tc.reason {
				t.Fatalf("release reason = %q, want %q", got.Reason, tc.reason)
			}

			if len(fileSvc.updateCalls) != 1 {
				t.Fatalf("expected one update call, got %d", len(fileSvc.updateCalls))
			}
			assertUpdateField(t, fileSvc.updateCalls[0].fields, "actual_credits", int64(0))
		})
	}
}

func TestBatchWorkerRequiresReservationForTerminalSettlement(t *testing.T) {
	accountID := uuid.New().String()

	fileSvc := &fakeBatchFileService{
		getBatch: &filestore.Batch{
			ID:               "batch-3",
			AccountID:        accountID,
			Endpoint:         "/v1/chat/completions",
			ModelAlias:       "hive-fast",
			EstimatedCredits: 1000,
		},
	}
	settler := &fakeAccountingSettler{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/batches/upstream-3" {
			http.NotFound(w, r)
			return
		}
		writeJSON(t, w, map[string]interface{}{
			"status": "completed",
			"request_counts": map[string]int{
				"completed": 1,
			},
		})
	}))
	defer server.Close()

	worker := NewBatchWorker(fileSvc, server.URL, "litellm-key", &fakeStorageUploader{}, "hive-files", settler)
	worker.httpClient = server.Client()

	payload := BatchPollPayload{
		BatchID:         "batch-3",
		AccountID:       accountID,
		UpstreamBatchID: "upstream-3",
		Endpoint:        "/v1/chat/completions",
		ModelAlias:      "hive-fast",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	err = worker.HandleBatchPoll(context.Background(), asynq.NewTask(TypeBatchPoll, body))
	if err == nil || !strings.Contains(err.Error(), "missing reservation_id for terminal batch settlement") {
		t.Fatalf("expected missing reservation_id error, got %v", err)
	}
	if len(fileSvc.updateCalls) != 0 {
		t.Fatalf("expected zero batch status updates, got %#v", fileSvc.updateCalls)
	}
}

type fakeStorageUploader struct {
	uploads []storageUpload
}

type storageUpload struct {
	bucket      string
	key         string
	contentType string
	body        string
}

func (f *fakeStorageUploader) Upload(_ context.Context, bucket, key string, reader io.Reader, _ int64, contentType string) error {
	body, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	f.uploads = append(f.uploads, storageUpload{
		bucket:      bucket,
		key:         key,
		contentType: contentType,
		body:        string(body),
	})
	return nil
}

type fakeBatchFileService struct {
	getBatch          *filestore.Batch
	getBatchErr       error
	fileIDsByFilename map[string]string
	createCalls       []createFileCall
	updateCalls       []updateBatchStatusCall
}

type createFileCall struct {
	accountID   string
	purpose     string
	filename    string
	bytes       int64
	storagePath string
}

type updateBatchStatusCall struct {
	batchID string
	status  string
	fields  map[string]interface{}
}

func (f *fakeBatchFileService) CreateFile(_ context.Context, accountID, purpose, filename string, bytes int64, storagePath string) (filestore.File, error) {
	f.createCalls = append(f.createCalls, createFileCall{
		accountID:   accountID,
		purpose:     purpose,
		filename:    filename,
		bytes:       bytes,
		storagePath: storagePath,
	})

	id := f.fileIDsByFilename[filename]
	if id == "" {
		id = "stored-" + strings.TrimSuffix(filename, ".jsonl")
	}
	return filestore.File{
		ID:          id,
		AccountID:   accountID,
		Purpose:     purpose,
		Filename:    filename,
		Bytes:       bytes,
		StoragePath: storagePath,
		Status:      "uploaded",
	}, nil
}

func (f *fakeBatchFileService) GetBatch(_ context.Context, id, accountID string) (*filestore.Batch, error) {
	if f.getBatchErr != nil {
		return nil, f.getBatchErr
	}
	if f.getBatch != nil {
		return f.getBatch, nil
	}
	return &filestore.Batch{
		ID:        id,
		AccountID: accountID,
		Endpoint:  "/v1/chat/completions",
	}, nil
}

func (f *fakeBatchFileService) UpdateBatchStatus(_ context.Context, batchID, status string, updates map[string]interface{}) error {
	copied := make(map[string]interface{}, len(updates))
	for key, value := range updates {
		copied[key] = value
	}
	f.updateCalls = append(f.updateCalls, updateBatchStatusCall{
		batchID: batchID,
		status:  status,
		fields:  copied,
	})
	return nil
}

type fakeAccountingSettler struct {
	finalizeCalls []accounting.FinalizeReservationInput
	releaseCalls  []accounting.ReleaseReservationInput
	finalizeErr   error
	releaseErr    error
}

func (f *fakeAccountingSettler) FinalizeReservation(_ context.Context, input accounting.FinalizeReservationInput) (accounting.Reservation, error) {
	f.finalizeCalls = append(f.finalizeCalls, input)
	if f.finalizeErr != nil {
		return accounting.Reservation{}, f.finalizeErr
	}
	return accounting.Reservation{ID: input.ReservationID, AccountID: input.AccountID}, nil
}

func (f *fakeAccountingSettler) ReleaseReservation(_ context.Context, input accounting.ReleaseReservationInput) (accounting.Reservation, error) {
	f.releaseCalls = append(f.releaseCalls, input)
	if f.releaseErr != nil {
		return accounting.Reservation{}, f.releaseErr
	}
	return accounting.Reservation{ID: input.ReservationID, AccountID: input.AccountID}, nil
}

func assertUpload(t *testing.T, upload storageUpload, bucket, contentType, bodyContains string) {
	t.Helper()

	if upload.bucket != bucket {
		t.Fatalf("expected upload bucket %q, got %q", bucket, upload.bucket)
	}
	if upload.key == "" {
		t.Fatal("expected upload key to be set")
	}
	if upload.contentType != contentType {
		t.Fatalf("expected content type %q, got %q", contentType, upload.contentType)
	}
	if !strings.Contains(upload.body, bodyContains) {
		t.Fatalf("expected upload body to contain %q, got %q", bodyContains, upload.body)
	}
}

func assertUpdateField(t *testing.T, fields map[string]interface{}, key string, want interface{}) {
	t.Helper()

	got, ok := fields[key]
	if !ok {
		t.Fatalf("expected update field %s", key)
	}
	switch wantValue := want.(type) {
	case int64:
		gotInt, ok := numericField(got)
		if !ok || gotInt != wantValue {
			t.Fatalf("expected update field %s=%v, got %v", key, want, got)
		}
	default:
		if got != want {
			t.Fatalf("expected update field %s=%v, got %v", key, want, got)
		}
	}
}

func numericField(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()

	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(value); err != nil {
		t.Fatalf("encode JSON response: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body.Bytes())
}
