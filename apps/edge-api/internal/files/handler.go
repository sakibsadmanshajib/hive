package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
)

// Authorizer is the interface used by Handler to validate API keys and extract account context.
type Authorizer interface {
	AuthorizeRequest(r *http.Request) (AuthResult, error)
}

// AuthResult carries the authorized account and API key identifiers.
type AuthResult struct {
	AccountID string
	APIKeyID  string
}

// StorageBackend is the interface for blob storage operations.
type StorageBackend interface {
	Upload(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) error
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, bucket, key string) error
	InitMultipartUpload(ctx context.Context, bucket, key, contentType string) (string, error)
	UploadPart(ctx context.Context, bucket, key, uploadID string, partNum int, reader io.Reader, size int64) (string, error)
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []CompletePart) error
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
}

// FileClientBackend is the interface for the control-plane filestore HTTP client.
type FileClientBackend interface {
	CreateFile(ctx context.Context, accountID, purpose, filename string, bytes int64, storagePath string) (*FileObject, error)
	GetFile(ctx context.Context, id, accountID string) (*FileObject, error)
	ListFiles(ctx context.Context, accountID string, purpose *string) (*FileListResponse, error)
	DeleteFile(ctx context.Context, id, accountID string) error
	CreateUpload(ctx context.Context, accountID, filename string, bytes int64, mimeType, purpose string) (*UploadObject, error)
	GetUpload(ctx context.Context, id, accountID string) (*UploadObject, error)
	AddUploadPart(ctx context.Context, uploadID, partID string, partNum int, etag string) (*UploadPartObject, error)
	CompleteUpload(ctx context.Context, uploadID, accountID string, partIDs []string) (*UploadObject, error)
	CancelUpload(ctx context.Context, uploadID, accountID string) (*UploadObject, error)
}

// Handler serves the Files API and Uploads API endpoints.
type Handler struct {
	authorizer Authorizer
	storage    StorageBackend
	fileClient FileClientBackend
	bucket     string
}

// NewHandler creates a new Handler with the given dependencies.
func NewHandler(authorizer Authorizer, storage StorageBackend, fileClient FileClientBackend, bucket string) *Handler {
	return &Handler{
		authorizer: authorizer,
		storage:    storage,
		fileClient: fileClient,
		bucket:     bucket,
	}
}

// ServeHTTP dispatches to the appropriate handler based on method and path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	// Files routes
	case r.Method == http.MethodPost && path == "/v1/files":
		h.handleUploadFile(w, r)
	case r.Method == http.MethodGet && path == "/v1/files":
		h.handleListFiles(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/content"):
		h.handleFileContent(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/files/"):
		h.handleRetrieveFile(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/v1/files/"):
		h.handleDeleteFile(w, r)

	// Uploads routes
	case r.Method == http.MethodPost && path == "/v1/uploads":
		h.handleCreateUpload(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/parts"):
		h.handleAddPart(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/complete"):
		h.handleCompleteUpload(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/cancel"):
		h.handleCancelUpload(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/uploads/"):
		h.handleRetrieveUpload(w, r)

	default:
		apierrors.WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed", nil)
	}
}

// authorize calls the authorizer and writes an error response if authorization fails.
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (AuthResult, bool) {
	result, err := h.authorizer.AuthorizeRequest(r)
	if err != nil {
		code := "invalid_api_key"
		apierrors.WriteError(w, http.StatusUnauthorized, "invalid_request_error", "Invalid API key.", &code)
		return AuthResult{}, false
	}
	return result, true
}

// --- File handlers ---

func (h *Handler) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	if err := r.ParseMultipartForm(MaxFileSize); err != nil {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to parse multipart form", nil)
		return
	}

	purpose := r.FormValue("purpose")
	if !ValidPurposes[purpose] {
		code := "invalid_purpose"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Invalid purpose: %q. Must be one of: batch, assistants, fine-tune, vision.", purpose), &code)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing file field in form", nil)
		return
	}
	defer file.Close()

	size := fileHeader.Size
	if size > MaxFileSize {
		code := "file_too_large"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("File size %d exceeds maximum allowed size of 512MB.", size), &code)
		return
	}

	filename := filepath.Base(fileHeader.Filename)
	fileID := "file-" + uuid.New().String()
	storagePath := auth.AccountID + "/" + fileID + "/" + filename
	contentType := fileHeader.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	if err := h.storage.Upload(r.Context(), h.bucket, storagePath, file, size, contentType); err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to store file", nil)
		return
	}

	created, err := h.fileClient.CreateFile(r.Context(), auth.AccountID, purpose, filename, size, storagePath)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to create file metadata", nil)
		return
	}

	writeJSON(w, http.StatusOK, created)
}

func (h *Handler) handleListFiles(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	var purpose *string
	if p := r.URL.Query().Get("purpose"); p != "" {
		purpose = &p
	}

	resp, err := h.fileClient.ListFiles(r.Context(), auth.AccountID, purpose)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to list files", nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleRetrieveFile(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	fileID := strings.TrimPrefix(r.URL.Path, "/v1/files/")
	if fileID == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing file ID", nil)
		return
	}

	f, err := h.fileClient.GetFile(r.Context(), fileID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No file with ID %q found.", fileID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to retrieve file", nil)
		return
	}

	writeJSON(w, http.StatusOK, f)
}

func (h *Handler) handleFileContent(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	// Extract file ID from path: /v1/files/{id}/content
	path := strings.TrimPrefix(r.URL.Path, "/v1/files/")
	fileID := strings.TrimSuffix(path, "/content")
	if fileID == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing file ID", nil)
		return
	}

	f, err := h.fileClient.GetFile(r.Context(), fileID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No file with ID %q found.", fileID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to retrieve file metadata", nil)
		return
	}

	reader, err := h.storage.Download(r.Context(), h.bucket, f.StoragePath)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to download file content", nil)
		return
	}
	defer reader.Close()

	contentType := mimeTypeFromFilename(f.Filename)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	io.Copy(w, reader)
}

func (h *Handler) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	fileID := strings.TrimPrefix(r.URL.Path, "/v1/files/")
	if fileID == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing file ID", nil)
		return
	}

	f, err := h.fileClient.GetFile(r.Context(), fileID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No file with ID %q found.", fileID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to retrieve file metadata", nil)
		return
	}

	if f.StoragePath != "" {
		if err := h.storage.Delete(r.Context(), h.bucket, f.StoragePath); err != nil {
			apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to delete file content", nil)
			return
		}
	}

	if err := h.fileClient.DeleteFile(r.Context(), fileID, auth.AccountID); err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to delete file metadata", nil)
		return
	}

	writeJSON(w, http.StatusOK, DeletedFileResponse{
		ID:      fileID,
		Object:  "file",
		Deleted: true,
	})
}

// --- Upload handlers ---

type createUploadRequest struct {
	Filename string `json:"filename"`
	Purpose  string `json:"purpose"`
	Bytes    int64  `json:"bytes"`
	MimeType string `json:"mime_type"`
}

func (h *Handler) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	var req createUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body", nil)
		return
	}

	if !ValidPurposes[req.Purpose] {
		code := "invalid_purpose"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Invalid purpose: %q.", req.Purpose), &code)
		return
	}
	if req.Bytes > MaxFileSize {
		code := "file_too_large"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Declared size %d exceeds maximum allowed size of 512MB.", req.Bytes), &code)
		return
	}
	if req.Filename == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "filename is required", nil)
		return
	}

	mimeType := req.MimeType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	upload, err := h.fileClient.CreateUpload(r.Context(), auth.AccountID, req.Filename, req.Bytes, mimeType, req.Purpose)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to create upload", nil)
		return
	}

	// Initialize S3 multipart upload and store the ID.
	storagePath := auth.AccountID + "/" + upload.ID + "/" + req.Filename
	s3UploadID, err := h.storage.InitMultipartUpload(r.Context(), h.bucket, storagePath, mimeType)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to initialize multipart upload", nil)
		return
	}

	// Attach internal fields so subsequent part handlers can use them.
	upload.S3UploadID = &s3UploadID
	upload.StoragePath = storagePath

	writeJSON(w, http.StatusOK, upload)
}

func (h *Handler) handleRetrieveUpload(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	uploadID := strings.TrimPrefix(r.URL.Path, "/v1/uploads/")
	if uploadID == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing upload ID", nil)
		return
	}

	u, err := h.fileClient.GetUpload(r.Context(), uploadID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No upload with ID %q found.", uploadID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to retrieve upload", nil)
		return
	}

	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) handleAddPart(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	// Path: /v1/uploads/{id}/parts
	path := strings.TrimPrefix(r.URL.Path, "/v1/uploads/")
	uploadID := strings.TrimSuffix(path, "/parts")
	if uploadID == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing upload ID", nil)
		return
	}

	upload, err := h.fileClient.GetUpload(r.Context(), uploadID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No upload with ID %q found.", uploadID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to retrieve upload metadata", nil)
		return
	}

	if upload.S3UploadID == nil || upload.StoragePath == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Upload is not in a valid state for adding parts", nil)
		return
	}

	if err := r.ParseMultipartForm(MaxFileSize); err != nil {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to parse multipart form", nil)
		return
	}

	dataFile, dataHeader, err := r.FormFile("data")
	if err != nil {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing data field in form", nil)
		return
	}
	defer dataFile.Close()

	// Part numbers are 1-based; use a sequential counter tracked via the form or default to 1.
	partNum := 1

	etag, err := h.storage.UploadPart(r.Context(), h.bucket, upload.StoragePath, *upload.S3UploadID, partNum, dataFile, dataHeader.Size)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to upload part", nil)
		return
	}

	partID := "part-" + uuid.New().String()
	part, err := h.fileClient.AddUploadPart(r.Context(), uploadID, partID, partNum, etag)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to record upload part", nil)
		return
	}

	writeJSON(w, http.StatusOK, part)
}

type completeUploadBody struct {
	PartIDs []string `json:"part_ids"`
}

func (h *Handler) handleCompleteUpload(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	// Path: /v1/uploads/{id}/complete
	path := strings.TrimPrefix(r.URL.Path, "/v1/uploads/")
	uploadID := strings.TrimSuffix(path, "/complete")
	if uploadID == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing upload ID", nil)
		return
	}

	var body completeUploadBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body", nil)
		return
	}

	upload, err := h.fileClient.GetUpload(r.Context(), uploadID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No upload with ID %q found.", uploadID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to retrieve upload metadata", nil)
		return
	}

	if upload.S3UploadID == nil || upload.StoragePath == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Upload is not in a valid state for completion", nil)
		return
	}

	// Build complete parts from part IDs - in this implementation we use sequential part numbers.
	parts := make([]CompletePart, len(body.PartIDs))
	for i, pid := range body.PartIDs {
		parts[i] = CompletePart{PartNumber: i + 1, ETag: pid}
	}

	if err := h.storage.CompleteMultipartUpload(r.Context(), h.bucket, upload.StoragePath, *upload.S3UploadID, parts); err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to complete multipart upload", nil)
		return
	}

	completed, err := h.fileClient.CompleteUpload(r.Context(), uploadID, auth.AccountID, body.PartIDs)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to complete upload metadata", nil)
		return
	}

	writeJSON(w, http.StatusOK, completed)
}

func (h *Handler) handleCancelUpload(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	// Path: /v1/uploads/{id}/cancel
	path := strings.TrimPrefix(r.URL.Path, "/v1/uploads/")
	uploadID := strings.TrimSuffix(path, "/cancel")
	if uploadID == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing upload ID", nil)
		return
	}

	upload, err := h.fileClient.GetUpload(r.Context(), uploadID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No upload with ID %q found.", uploadID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to retrieve upload metadata", nil)
		return
	}

	if upload.S3UploadID != nil && upload.StoragePath != "" {
		if err := h.storage.AbortMultipartUpload(r.Context(), h.bucket, upload.StoragePath, *upload.S3UploadID); err != nil {
			// Log but do not block cancellation; storage may have already cleaned up.
			_ = err
		}
	}

	cancelled, err := h.fileClient.CancelUpload(r.Context(), uploadID, auth.AccountID)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to cancel upload", nil)
		return
	}

	writeJSON(w, http.StatusOK, cancelled)
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func mimeTypeFromFilename(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jsonl", ".ndjson":
		return "application/jsonl"
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".txt":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}
