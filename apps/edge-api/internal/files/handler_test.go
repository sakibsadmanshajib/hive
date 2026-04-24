package files_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hivegpt/hive/apps/edge-api/internal/files"
)

// --- Mock authorizer ---

type mockAuthorizer struct {
	accountID string
	apiKeyID  string
}

func (m *mockAuthorizer) AuthorizeRequest(r *http.Request) (files.AuthResult, error) {
	return files.AuthResult{AccountID: m.accountID, APIKeyID: m.apiKeyID}, nil
}

// --- Mock storage ---

type mockStorage struct {
	uploadCalled   bool
	uploadBucket   string
	uploadKey      string
	downloadReader io.ReadCloser
	downloadErr    error
	deleteErr      error
	initUploadID   string
	uploadPartEtag string
	completeErr    error
	abortErr       error
}

func (m *mockStorage) Upload(_ context.Context, bucket, key string, _ io.Reader, _ int64, _ string) error {
	m.uploadCalled = true
	m.uploadBucket = bucket
	m.uploadKey = key
	return nil
}

func (m *mockStorage) Download(_ context.Context, _, _ string) (io.ReadCloser, error) {
	if m.downloadErr != nil {
		return nil, m.downloadErr
	}
	if m.downloadReader != nil {
		return m.downloadReader, nil
	}
	return io.NopCloser(strings.NewReader("file content")), nil
}

func (m *mockStorage) Delete(_ context.Context, _, _ string) error {
	return m.deleteErr
}

func (m *mockStorage) InitMultipartUpload(_ context.Context, _, _, _ string) (string, error) {
	return m.initUploadID, nil
}

func (m *mockStorage) UploadPart(_ context.Context, _, _, _ string, _ int, _ io.Reader, _ int64) (string, error) {
	etag := m.uploadPartEtag
	if etag == "" {
		etag = "etag-abc"
	}
	return etag, nil
}

func (m *mockStorage) CompleteMultipartUpload(_ context.Context, _, _, _ string, _ []files.CompletePart) error {
	return m.completeErr
}

func (m *mockStorage) AbortMultipartUpload(_ context.Context, _, _, _ string) error {
	return m.abortErr
}

// --- Mock file client ---

type mockFileClient struct {
	createdFile     *files.FileObject
	getFile         *files.FileObject
	getErr          error
	listResponse    *files.FileListResponse
	deleteErr       error
	createdUpload   *files.UploadObject
	getUpload       *files.UploadObject
	getUploadErr    error
	addedPart       *files.UploadPartObject
	completedUpload *files.UploadObject
	cancelledUpload *files.UploadObject
}

func (m *mockFileClient) CreateFile(_ context.Context, _, purpose, filename string, b int64, _ string) (*files.FileObject, error) {
	if m.createdFile != nil {
		return m.createdFile, nil
	}
	return &files.FileObject{
		ID:        "file-test-id",
		Object:    "file",
		Bytes:     b,
		CreatedAt: time.Now().Unix(),
		Filename:  filename,
		Purpose:   purpose,
		Status:    "uploaded",
	}, nil
}

func (m *mockFileClient) GetFile(_ context.Context, id, accountID string) (*files.FileObject, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.getFile != nil {
		return m.getFile, nil
	}
	return &files.FileObject{
		ID:          id,
		Object:      "file",
		Bytes:       1024,
		CreatedAt:   time.Now().Unix(),
		Filename:    "test.jsonl",
		Purpose:     "batch",
		Status:      "uploaded",
		StoragePath: accountID + "/" + id + "/test.jsonl",
	}, nil
}

func (m *mockFileClient) ListFiles(_ context.Context, _ string, _ *string) (*files.FileListResponse, error) {
	if m.listResponse != nil {
		return m.listResponse, nil
	}
	return &files.FileListResponse{
		Object: "list",
		Data:   []files.FileObject{},
	}, nil
}

func (m *mockFileClient) DeleteFile(_ context.Context, _, _ string) error {
	return m.deleteErr
}

func (m *mockFileClient) CreateUpload(_ context.Context, _, filename string, b int64, _, purpose string) (*files.UploadObject, error) {
	if m.createdUpload != nil {
		return m.createdUpload, nil
	}
	return &files.UploadObject{
		ID:        "upload-test-id",
		Object:    "upload",
		Bytes:     b,
		CreatedAt: time.Now().Unix(),
		Filename:  filename,
		Purpose:   purpose,
		Status:    "pending",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}, nil
}

func (m *mockFileClient) GetUpload(_ context.Context, id, _ string) (*files.UploadObject, error) {
	if m.getUploadErr != nil {
		return nil, m.getUploadErr
	}
	if m.getUpload != nil {
		return m.getUpload, nil
	}
	s3ID := "s3-upload-id"
	storagePath := "account-id/" + id + "/file.jsonl"
	return &files.UploadObject{
		ID:          id,
		Object:      "upload",
		Bytes:       1024,
		CreatedAt:   time.Now().Unix(),
		Filename:    "file.jsonl",
		Purpose:     "batch",
		Status:      "pending",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
		S3UploadID:  &s3ID,
		StoragePath: storagePath,
	}, nil
}

func (m *mockFileClient) AddUploadPart(_ context.Context, uploadID, partID string, _ int, _ string) (*files.UploadPartObject, error) {
	if m.addedPart != nil {
		return m.addedPart, nil
	}
	return &files.UploadPartObject{
		ID:        partID,
		Object:    "upload.part",
		CreatedAt: time.Now().Unix(),
		UploadID:  uploadID,
	}, nil
}

func (m *mockFileClient) CompleteUpload(_ context.Context, uploadID, _ string, _ []string) (*files.UploadObject, error) {
	if m.completedUpload != nil {
		return m.completedUpload, nil
	}
	f := &files.FileObject{
		ID:       "file-completed",
		Object:   "file",
		Bytes:    1024,
		Filename: "file.jsonl",
		Purpose:  "batch",
		Status:   "uploaded",
	}
	return &files.UploadObject{
		ID:        uploadID,
		Object:    "upload",
		Status:    "completed",
		CreatedAt: time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
		File:      f,
	}, nil
}

func (m *mockFileClient) CancelUpload(_ context.Context, uploadID, _ string) (*files.UploadObject, error) {
	if m.cancelledUpload != nil {
		return m.cancelledUpload, nil
	}
	return &files.UploadObject{
		ID:        uploadID,
		Object:    "upload",
		Status:    "cancelled",
		CreatedAt: time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}, nil
}

// --- Test helper ---

func newTestHandler(storage files.StorageBackend, fileClient files.FileClientBackend) *files.Handler {
	auth := &mockAuthorizer{accountID: "test-account-id", apiKeyID: "key-1"}
	return files.NewHandler(auth, storage, fileClient, "hive-files")
}

// --- Tests ---

func TestFileUpload(t *testing.T) {
	storage := &mockStorage{}
	h := newTestHandler(storage, &mockFileClient{})

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "test.jsonl")
	io.WriteString(fw, `{"custom_id":"1"}`)
	w.WriteField("purpose", "batch")
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/files", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp files.FileObject
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Object != "file" {
		t.Errorf("expected object=file, got %s", resp.Object)
	}
	if resp.Purpose != "batch" {
		t.Errorf("expected purpose=batch, got %s", resp.Purpose)
	}
	if !storage.uploadCalled {
		t.Error("expected storage.Upload to be called")
	}
	if !strings.HasPrefix(storage.uploadKey, "test-account-id/") {
		t.Errorf("expected storage key to start with account id, got %s", storage.uploadKey)
	}
}

func TestFileUploadInvalidPurpose(t *testing.T) {
	h := newTestHandler(&mockStorage{}, &mockFileClient{})

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "test.jsonl")
	io.WriteString(fw, "data")
	w.WriteField("purpose", "invalid")
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/files", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestFileList(t *testing.T) {
	fileClient := &mockFileClient{
		listResponse: &files.FileListResponse{
			Object: "list",
			Data: []files.FileObject{
				{ID: "file-1", Object: "file", Purpose: "batch"},
			},
		},
	}
	h := newTestHandler(&mockStorage{}, fileClient)

	req := httptest.NewRequest(http.MethodGet, "/v1/files", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp files.FileListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "list" {
		t.Errorf("expected object=list, got %s", resp.Object)
	}
	if len(resp.Data) != 1 {
		t.Errorf("expected 1 file, got %d", len(resp.Data))
	}
}

func TestFileRetrieve(t *testing.T) {
	fileClient := &mockFileClient{
		getFile: &files.FileObject{
			ID:          "file-abc",
			Object:      "file",
			Bytes:       512,
			Filename:    "data.jsonl",
			Purpose:     "batch",
			Status:      "uploaded",
			StoragePath: "test-account-id/file-abc/data.jsonl",
		},
	}
	h := newTestHandler(&mockStorage{}, fileClient)

	req := httptest.NewRequest(http.MethodGet, "/v1/files/file-abc", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp files.FileObject
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != "file-abc" {
		t.Errorf("expected id=file-abc, got %s", resp.ID)
	}
}

func TestFileRetrieveNotFound(t *testing.T) {
	fileClient := &mockFileClient{
		getErr: files.ErrNotFound,
	}
	h := newTestHandler(&mockStorage{}, fileClient)

	req := httptest.NewRequest(http.MethodGet, "/v1/files/file-xyz", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestFileDelete(t *testing.T) {
	fileClient := &mockFileClient{
		getFile: &files.FileObject{
			ID:          "file-del",
			Object:      "file",
			StoragePath: "test-account-id/file-del/data.jsonl",
		},
	}
	h := newTestHandler(&mockStorage{}, fileClient)

	req := httptest.NewRequest(http.MethodDelete, "/v1/files/file-del", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp files.DeletedFileResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Deleted {
		t.Error("expected deleted=true")
	}
	if resp.ID != "file-del" {
		t.Errorf("expected id=file-del, got %s", resp.ID)
	}
}

func TestFileContent(t *testing.T) {
	fileClient := &mockFileClient{
		getFile: &files.FileObject{
			ID:          "file-content",
			StoragePath: "test-account-id/file-content/test.jsonl",
		},
	}
	storage := &mockStorage{
		downloadReader: io.NopCloser(strings.NewReader("file bytes here")),
	}
	h := newTestHandler(storage, fileClient)

	req := httptest.NewRequest(http.MethodGet, "/v1/files/file-content/content", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "file bytes here" {
		t.Errorf("unexpected body: %q", rec.Body.String())
	}
}

func TestUploadCreate(t *testing.T) {
	storage := &mockStorage{initUploadID: "s3-upload-id"}
	h := newTestHandler(storage, &mockFileClient{})

	body := `{"filename":"data.jsonl","purpose":"batch","bytes":1024,"mime_type":"application/jsonl"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/uploads", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp files.UploadObject
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "upload" {
		t.Errorf("expected object=upload, got %s", resp.Object)
	}
	if resp.Status != "pending" {
		t.Errorf("expected status=pending, got %s", resp.Status)
	}
}

func TestUploadAddPart(t *testing.T) {
	storage := &mockStorage{initUploadID: "s3-upload-id"}
	h := newTestHandler(storage, &mockFileClient{})

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("data", "part.bin")
	io.WriteString(fw, "part data bytes")
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/uploads/upload-test-id/parts", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp files.UploadPartObject
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Object != "upload.part" {
		t.Errorf("expected object=upload.part, got %s", resp.Object)
	}
	if resp.UploadID != "upload-test-id" {
		t.Errorf("expected upload_id=upload-test-id, got %s", resp.UploadID)
	}
}

func TestUploadComplete(t *testing.T) {
	h := newTestHandler(&mockStorage{}, &mockFileClient{})

	body := `{"part_ids":["part-1","part-2"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/uploads/upload-test-id/complete", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp files.UploadObject
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("expected status=completed, got %s", resp.Status)
	}
	if resp.File == nil {
		t.Error("expected file field to be populated after completion")
	}
}

func TestUploadCancel(t *testing.T) {
	h := newTestHandler(&mockStorage{}, &mockFileClient{})

	req := httptest.NewRequest(http.MethodPost, "/v1/uploads/upload-test-id/cancel", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp files.UploadObject
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "cancelled" {
		t.Errorf("expected status=cancelled, got %s", resp.Status)
	}
}
