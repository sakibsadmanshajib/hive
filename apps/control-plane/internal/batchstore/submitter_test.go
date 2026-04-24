package batchstore

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounting"
	"github.com/hivegpt/hive/apps/control-plane/internal/filestore"
	"github.com/hivegpt/hive/apps/control-plane/internal/routing"
)

func TestSubmitterSubmitsUpstreamBatchAndEnqueuesPoll(t *testing.T) {
	accountID := uuid.New().String()
	reservationID := uuid.New().String()
	apiKeyID := uuid.New().String()
	inputJSONL := strings.Join([]string{
		`{"custom_id":"req-1","method":"POST","url":"/v1/chat/completions","body":{"model":"hive-default","messages":[{"role":"user","content":"reply with ok"}]}}`,
		"",
	}, "\n")

	files := &fakeSubmitterFileStore{
		file: &filestore.File{
			ID:          "file-input",
			AccountID:   accountID,
			Filename:    "input.jsonl",
			StoragePath: accountID + "/file-input/input.jsonl",
		},
	}
	storage := &fakeSubmitterStorage{body: inputJSONL}
	routes := &fakeSubmitterRouteSelector{
		result: routing.SelectionResult{
			AliasID:          "hive-default",
			RouteID:          "route-openrouter-default",
			LiteLLMModelName: "route-openrouter-default",
			Provider:         "openrouter",
		},
	}
	queue := &fakeSubmitterQueue{}
	accountingSvc := &fakeSubmitterAccounting{}

	var uploadBody string
	var uploadPurpose string
	var uploadProvider string
	var createBatchRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("parse multipart form: %v", err)
			}
			uploadPurpose = r.FormValue("purpose")
			uploadProvider = r.FormValue("custom_llm_provider")
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Fatalf("expected file field: %v", err)
			}
			defer file.Close()
			payload, err := io.ReadAll(file)
			if err != nil {
				t.Fatalf("read uploaded file: %v", err)
			}
			uploadBody = string(payload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":         "file-upstream-1",
				"object":     "file",
				"purpose":    "batch",
				"filename":   "input.jsonl",
				"bytes":      len(payload),
				"created_at": time.Now().Unix(),
				"status":     "processed",
			})
		case "/v1/batches":
			if err := json.NewDecoder(r.Body).Decode(&createBatchRequest); err != nil {
				t.Fatalf("decode create batch request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                "batch-upstream-1",
				"object":            "batch",
				"status":            "validating",
				"input_file_id":     "file-upstream-1",
				"endpoint":          "/v1/chat/completions",
				"completion_window": "24h",
				"created_at":        time.Now().Unix(),
				"request_counts": map[string]any{
					"total":     1,
					"completed": 0,
					"failed":    0,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	submitter := NewSubmitter(
		files,
		routes,
		storage,
		queue,
		accountingSvc,
		server.URL,
		"litellm-key",
		"hive-files",
	)
	submitter.httpClient = server.Client()

	batch := filestore.Batch{
		ID:               "batch-local-1",
		AccountID:        accountID,
		InputFileID:      "file-input",
		Endpoint:         "/v1/chat/completions",
		CompletionWindow: "24h",
		Status:           "validating",
		ReservationID:    &reservationID,
		APIKeyID:         &apiKeyID,
		ModelAlias:       "hive-default",
		EstimatedCredits: 1000,
		CreatedAt:        time.Now().UTC(),
		ExpiresAt:        time.Now().UTC().Add(24 * time.Hour),
	}

	updated, err := submitter.SubmitBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("SubmitBatch returned error: %v", err)
	}

	if uploadPurpose != "batch" {
		t.Fatalf("upload purpose = %q, want %q", uploadPurpose, "batch")
	}
	if uploadProvider != "openrouter" {
		t.Fatalf("upload custom_llm_provider = %q, want %q", uploadProvider, "openrouter")
	}
	if strings.Contains(uploadBody, `"model":"hive-default"`) {
		t.Fatalf("expected uploaded JSONL to rewrite model alias, got %s", uploadBody)
	}
	if !strings.Contains(uploadBody, `"model":"route-openrouter-default"`) {
		t.Fatalf("expected uploaded JSONL to contain routed model, got %s", uploadBody)
	}

	if got := createBatchRequest["input_file_id"]; got != "file-upstream-1" {
		t.Fatalf("input_file_id = %v, want %q", got, "file-upstream-1")
	}
	if got := createBatchRequest["endpoint"]; got != "/v1/chat/completions" {
		t.Fatalf("endpoint = %v, want %q", got, "/v1/chat/completions")
	}

	if len(queue.payloads) != 1 {
		t.Fatalf("expected one queued poll payload, got %#v", queue.payloads)
	}
	payload := queue.payloads[0]
	if payload.BatchID != batch.ID || payload.UpstreamBatchID != "batch-upstream-1" {
		t.Fatalf("unexpected queued payload: %#v", payload)
	}
	if payload.ModelAlias != "hive-default" || payload.APIKeyID != apiKeyID {
		t.Fatalf("unexpected queued attribution: %#v", payload)
	}

	if len(files.updateCalls) != 1 {
		t.Fatalf("expected one status update, got %#v", files.updateCalls)
	}
	update := files.updateCalls[0]
	if update.batchID != batch.ID || update.status != "validating" {
		t.Fatalf("unexpected update call: %#v", update)
	}
	if got := update.fields["upstream_batch_id"]; got != "batch-upstream-1" {
		t.Fatalf("upstream_batch_id = %v, want %q", got, "batch-upstream-1")
	}

	if updated.Status != "validating" {
		t.Fatalf("updated status = %q, want %q", updated.Status, "validating")
	}
	if updated.UpstreamBatchID == nil || *updated.UpstreamBatchID != "batch-upstream-1" {
		t.Fatalf("updated upstream batch id = %v, want batch-upstream-1", updated.UpstreamBatchID)
	}
	if updated.RequestCountsTotal != 1 || updated.RequestCountsCompleted != 0 || updated.RequestCountsFailed != 0 {
		t.Fatalf("unexpected updated request counts: %#v", updated)
	}
	if len(accountingSvc.releaseCalls) != 0 {
		t.Fatalf("expected no release calls on success, got %#v", accountingSvc.releaseCalls)
	}
}

func TestSubmitterReleasesReservationAndMarksBatchFailedOnImmediateSubmissionError(t *testing.T) {
	accountID := uuid.New().String()
	reservationID := uuid.New().String()
	files := &fakeSubmitterFileStore{
		file: &filestore.File{
			ID:          "file-input",
			AccountID:   accountID,
			Filename:    "input.jsonl",
			StoragePath: accountID + "/file-input/input.jsonl",
		},
	}
	storage := &fakeSubmitterStorage{body: `{"custom_id":"req-1","method":"POST","url":"/v1/chat/completions","body":{"model":"hive-default"}}` + "\n"}
	routes := &fakeSubmitterRouteSelector{
		result: routing.SelectionResult{
			AliasID:          "hive-default",
			RouteID:          "route-openrouter-default",
			LiteLLMModelName: "route-openrouter-default",
			Provider:         "openrouter",
		},
	}
	queue := &fakeSubmitterQueue{}
	accountingSvc := &fakeSubmitterAccounting{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/files" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "files_settings is not set",
			},
		})
	}))
	defer server.Close()

	submitter := NewSubmitter(
		files,
		routes,
		storage,
		queue,
		accountingSvc,
		server.URL,
		"litellm-key",
		"hive-files",
	)
	submitter.httpClient = server.Client()

	batch := filestore.Batch{
		ID:               "batch-local-2",
		AccountID:        accountID,
		InputFileID:      "file-input",
		Endpoint:         "/v1/chat/completions",
		CompletionWindow: "24h",
		Status:           "validating",
		ReservationID:    &reservationID,
		ModelAlias:       "hive-default",
		EstimatedCredits: 1000,
		CreatedAt:        time.Now().UTC(),
		ExpiresAt:        time.Now().UTC().Add(24 * time.Hour),
	}

	_, err := submitter.SubmitBatch(context.Background(), batch)
	if err == nil || !strings.Contains(err.Error(), "upload upstream batch file") {
		t.Fatalf("expected upstream upload error, got %v", err)
	}

	if len(accountingSvc.releaseCalls) != 1 {
		t.Fatalf("expected one release call, got %#v", accountingSvc.releaseCalls)
	}
	if accountingSvc.releaseCalls[0].Reason != "batch_submission_failed" {
		t.Fatalf("release reason = %q, want %q", accountingSvc.releaseCalls[0].Reason, "batch_submission_failed")
	}

	if len(files.updateCalls) != 1 {
		t.Fatalf("expected one failed status update, got %#v", files.updateCalls)
	}
	update := files.updateCalls[0]
	if update.status != "failed" {
		t.Fatalf("status update = %q, want %q", update.status, "failed")
	}
	if _, ok := update.fields["failed_at"]; !ok {
		t.Fatalf("expected failed_at in update fields, got %#v", update.fields)
	}

	if len(queue.payloads) != 0 {
		t.Fatalf("expected no queued poll payloads, got %#v", queue.payloads)
	}
}

type fakeSubmitterFileStore struct {
	file        *filestore.File
	updateCalls []submitterUpdateCall
}

type submitterUpdateCall struct {
	batchID string
	status  string
	fields  map[string]interface{}
}

func (f *fakeSubmitterFileStore) GetFile(_ context.Context, id, accountID string) (*filestore.File, error) {
	if f.file != nil && f.file.ID == id && f.file.AccountID == accountID {
		return f.file, nil
	}
	return nil, filestore.ErrNotFound
}

func (f *fakeSubmitterFileStore) UpdateBatchStatus(_ context.Context, batchID, status string, fields map[string]interface{}) error {
	copied := make(map[string]interface{}, len(fields))
	for key, value := range fields {
		copied[key] = value
	}
	f.updateCalls = append(f.updateCalls, submitterUpdateCall{
		batchID: batchID,
		status:  status,
		fields:  copied,
	})
	return nil
}

type fakeSubmitterStorage struct {
	body string
}

func (f *fakeSubmitterStorage) Download(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.body)), nil
}

type fakeSubmitterRouteSelector struct {
	result routing.SelectionResult
	err    error
}

func (f *fakeSubmitterRouteSelector) SelectRoute(_ context.Context, input routing.SelectionInput) (routing.SelectionResult, error) {
	if f.err != nil {
		return routing.SelectionResult{}, f.err
	}
	if !input.NeedBatch {
		return routing.SelectionResult{}, nil
	}
	return f.result, nil
}

type fakeSubmitterQueue struct {
	payloads []BatchPollPayload
}

func (f *fakeSubmitterQueue) Enqueue(_ context.Context, payload BatchPollPayload) error {
	f.payloads = append(f.payloads, payload)
	return nil
}

type fakeSubmitterAccounting struct {
	releaseCalls []accounting.ReleaseReservationInput
}

func (f *fakeSubmitterAccounting) ReleaseReservation(_ context.Context, input accounting.ReleaseReservationInput) (accounting.Reservation, error) {
	f.releaseCalls = append(f.releaseCalls, input)
	return accounting.Reservation{}, nil
}

func readUploadedMultipartFile(t *testing.T, contentType string, body io.Reader) (purpose, provider, payload string) {
	t.Helper()

	mediaType := strings.TrimSpace(strings.Split(contentType, ";")[0])
	if mediaType != "multipart/form-data" {
		t.Fatalf("content type = %q, want multipart/form-data", contentType)
	}

	reader := multipart.NewReader(body, strings.TrimPrefix(strings.Split(contentType, "boundary=")[1], "\""))
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read multipart part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read multipart content: %v", err)
		}
		switch part.FormName() {
		case "purpose":
			purpose = string(data)
		case "custom_llm_provider":
			provider = string(data)
		case "file":
			payload = string(data)
		}
	}
	return purpose, provider, payload
}
