package batches

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

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

// BatchClientBackend is the interface for the control-plane batchstore HTTP client.
type BatchClientBackend interface {
	CreateBatch(ctx context.Context, accountID, inputFileID, endpoint, completionWindow string, totalRequests int, reservationID string) (*BatchObject, error)
	GetBatch(ctx context.Context, id, accountID string) (*BatchObject, error)
	ListBatches(ctx context.Context, accountID string, limit int, after *string) (*BatchListResponse, error)
	CancelBatch(ctx context.Context, id, accountID string) (*BatchObject, error)
}

// FileClientBackend is the subset of the filestore client needed by the batches handler.
type FileClientBackend interface {
	GetFile(ctx context.Context, id, accountID string) (*FileObject, error)
}

// FileObject is the minimal file representation needed by the batches handler.
// It re-uses the files package types via import, but we define what we need here
// to avoid circular imports. The actual type comes from files.FileObject.
type FileObject struct {
	ID          string
	StoragePath string
}

// StorageBackend provides file content download for JSONL validation.
type StorageBackend interface {
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)
}

// ReservationInput is the parameters for creating a credit reservation for a batch.
type ReservationInput struct {
	AccountID        string
	APIKeyID         string
	RequestID        string
	Endpoint         string
	EstimatedCredits int64
}

// AccountingBackend provides credit reservation capabilities.
type AccountingBackend interface {
	CreateReservation(ctx context.Context, input ReservationInput) (string, error)
}

// Handler serves the Batches API endpoints.
type Handler struct {
	authorizer  Authorizer
	batchClient BatchClientBackend
	fileClient  FileClientBackend
	storage     StorageBackend
	accounting  AccountingBackend
	bucket      string
}

// NewHandler creates a new batches Handler with the given dependencies.
func NewHandler(
	authorizer Authorizer,
	batchClient BatchClientBackend,
	fileClient FileClientBackend,
	storage StorageBackend,
	accounting AccountingBackend,
	bucket string,
) *Handler {
	return &Handler{
		authorizer:  authorizer,
		batchClient: batchClient,
		fileClient:  fileClient,
		storage:     storage,
		accounting:  accounting,
		bucket:      bucket,
	}
}

// ServeHTTP dispatches to the appropriate handler based on method and path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case r.Method == http.MethodPost && path == "/v1/batches":
		h.handleCreate(w, r)
	case r.Method == http.MethodGet && path == "/v1/batches":
		h.handleList(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/v1/batches/") && !strings.HasSuffix(path, "/cancel"):
		h.handleRetrieve(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/cancel"):
		h.handleCancel(w, r)
	default:
		apierrors.WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed", nil)
	}
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (AuthResult, bool) {
	result, err := h.authorizer.AuthorizeRequest(r)
	if err != nil {
		code := "invalid_api_key"
		apierrors.WriteError(w, http.StatusUnauthorized, "invalid_request_error", "Invalid API key.", &code)
		return AuthResult{}, false
	}
	return result, true
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	var req BatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body", nil)
		return
	}

	// Validate endpoint
	if !ValidBatchEndpoints[req.Endpoint] {
		code := "invalid_endpoint"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Invalid endpoint %q. Supported endpoints: /v1/chat/completions, /v1/completions, /v1/embeddings.", req.Endpoint), &code)
		return
	}

	// Validate completion_window
	if req.CompletionWindow != "24h" {
		code := "invalid_completion_window"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
			"Only completion_window=24h is supported.", &code)
		return
	}

	// Get input file metadata (validates account ownership)
	inputFile, err := h.fileClient.GetFile(r.Context(), req.InputFileID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No file with ID %q found.", req.InputFileID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to retrieve input file", nil)
		return
	}

	// Validate JSONL BEFORE reserving credits
	lineCount, validationErr := h.validateJSONL(r.Context(), inputFile.StoragePath, req.Endpoint)
	if validationErr != nil {
		code := "invalid_jsonl"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", validationErr.Error(), &code)
		return
	}

	// Estimate credit cost and reserve credits
	estimatedCredits := estimateBatchCredits(lineCount, req.Endpoint)
	reservationID, err := h.accounting.CreateReservation(r.Context(), ReservationInput{
		AccountID:        auth.AccountID,
		APIKeyID:         auth.APIKeyID,
		RequestID:        "batch-" + req.InputFileID,
		Endpoint:         req.Endpoint,
		EstimatedCredits: estimatedCredits,
	})
	if err != nil {
		apierrors.WriteError(w, http.StatusPaymentRequired, "insufficient_quota", "Failed to reserve credits for batch", nil)
		return
	}

	// Create batch record in control-plane
	batch, err := h.batchClient.CreateBatch(r.Context(), auth.AccountID, req.InputFileID, req.Endpoint, req.CompletionWindow, lineCount, reservationID)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to create batch", nil)
		return
	}

	writeJSON(w, http.StatusOK, batch)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	limit := 20
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	var after *string
	if a := r.URL.Query().Get("after"); a != "" {
		after = &a
	}

	resp, err := h.batchClient.ListBatches(r.Context(), auth.AccountID, limit, after)
	if err != nil {
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to list batches", nil)
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleRetrieve(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	batchID := strings.TrimPrefix(r.URL.Path, "/v1/batches/")
	if batchID == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing batch ID", nil)
		return
	}

	batch, err := h.batchClient.GetBatch(r.Context(), batchID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No batch with ID %q found.", batchID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to retrieve batch", nil)
		return
	}

	writeJSON(w, http.StatusOK, batch)
}

func (h *Handler) handleCancel(w http.ResponseWriter, r *http.Request) {
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	// Path: /v1/batches/{id}/cancel
	path := strings.TrimPrefix(r.URL.Path, "/v1/batches/")
	batchID := strings.TrimSuffix(path, "/cancel")
	if batchID == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing batch ID", nil)
		return
	}

	batch, err := h.batchClient.CancelBatch(r.Context(), batchID, auth.AccountID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			code := "not_found"
			apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error",
				fmt.Sprintf("No batch with ID %q found.", batchID), &code)
			return
		}
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to cancel batch", nil)
		return
	}

	writeJSON(w, http.StatusOK, batch)
}

// validateJSONL downloads and scans the JSONL file, returning line count or a validation error.
func (h *Handler) validateJSONL(ctx context.Context, storagePath, expectedEndpoint string) (int, error) {
	reader, err := h.storage.Download(ctx, h.bucket, storagePath)
	if err != nil {
		return 0, fmt.Errorf("failed to download input file for validation: %w", err)
	}
	defer reader.Close()

	scanner := bufio.NewScanner(reader)
	lineNum := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lineNum++

		var input BatchInputLine
		if err := json.Unmarshal([]byte(line), &input); err != nil {
			return 0, fmt.Errorf("invalid JSON on line %d: %w", lineNum, err)
		}
		if input.CustomID == "" {
			return 0, fmt.Errorf("line %d: custom_id is required", lineNum)
		}
		if input.Method != "POST" {
			return 0, fmt.Errorf("line %d: method must be POST, got %q", lineNum, input.Method)
		}
		if input.URL != expectedEndpoint {
			return 0, fmt.Errorf("line %d: url %q does not match batch endpoint %q", lineNum, input.URL, expectedEndpoint)
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading input file: %w", err)
	}

	return lineNum, nil
}

// estimateBatchCredits estimates the credit cost for a batch job.
// This is a rough estimate; actual cost is settled on completion.
func estimateBatchCredits(lineCount int, endpoint string) int64 {
	// Conservative estimate: 1000 credits per line for chat/completions, 10 per line for embeddings.
	switch endpoint {
	case "/v1/embeddings":
		return int64(lineCount) * 10
	default:
		return int64(lineCount) * 1000
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
