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

	"github.com/hibiken/asynq"
	"github.com/hivegpt/hive/apps/control-plane/internal/filestore"
)

type batchFileService interface {
	CreateFile(ctx context.Context, accountID, purpose, filename string, bytes int64, storagePath string) (filestore.File, error)
	UpdateBatchStatus(ctx context.Context, batchID, status string, updates map[string]interface{}) error
}

func TestBatchWorkerStoresCompletedOutputFiles(t *testing.T) {
	fileSvc := &fakeBatchFileService{
		fileIDsByFilename: map[string]string{
			"file-out.jsonl": "stored-output-file",
			"file-err.jsonl": "stored-error-file",
		},
	}
	var _ batchFileService = fileSvc

	storage := &fakeStorageUploader{}
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

	worker := NewBatchWorker(fileSvc, server.URL, "litellm-key", storage, "hive-files")
	worker.httpClient = server.Client()

	payload := BatchPollPayload{
		BatchID:         "batch-1",
		AccountID:       "acct-1",
		ReservationID:   "reservation-1",
		UpstreamBatchID: "upstream-1",
		Provider:        "openrouter",
		InputFileID:     "file-input",
		Endpoint:        "/v1/chat/completions",
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

	if len(fileSvc.updateCalls) != 1 {
		t.Fatalf("expected one UpdateBatchStatus call, got %d", len(fileSvc.updateCalls))
	}
	update := fileSvc.updateCalls[0]
	if update.batchID != "batch-1" || update.status != "completed" {
		t.Fatalf("expected completed update for batch-1, got batch=%q status=%q", update.batchID, update.status)
	}
	assertUpdateField(t, update.fields, "output_file_id", "stored-output-file")
	assertUpdateField(t, update.fields, "error_file_id", "stored-error-file")
	assertUpdateField(t, update.fields, "request_counts_total", 3)
	assertUpdateField(t, update.fields, "request_counts_completed", 2)
	assertUpdateField(t, update.fields, "request_counts_failed", 1)
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
	if got != want {
		t.Fatalf("expected update field %s=%v, got %v", key, want, got)
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
