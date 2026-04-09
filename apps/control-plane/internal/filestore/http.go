package filestore

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// RegisterRoutes registers all internal filestore HTTP endpoints on mux.
func RegisterRoutes(mux *http.ServeMux, svc *Service) {
	h := &handler{svc: svc}

	// File endpoints
	mux.Handle("/internal/files/create", h)
	mux.Handle("/internal/files/get", h)
	mux.Handle("/internal/files/list", h)
	mux.Handle("/internal/files/delete", h)

	// Upload endpoints
	mux.Handle("/internal/uploads/create", h)
	mux.Handle("/internal/uploads/get", h)
	mux.Handle("/internal/uploads/add-part", h)
	mux.Handle("/internal/uploads/complete", h)
	mux.Handle("/internal/uploads/cancel", h)

	// Batch endpoints
	mux.Handle("/internal/batches/create", h)
	mux.Handle("/internal/batches/get", h)
	mux.Handle("/internal/batches/list", h)
	mux.Handle("/internal/batches/cancel", h)
	mux.Handle("/internal/batches/update-status", h)
}

type handler struct {
	svc *Service
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/internal/files/create":
		h.handleCreateFile(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/internal/files/get":
		h.handleGetFile(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/internal/files/list":
		h.handleListFiles(w, r)
	case r.Method == http.MethodDelete && r.URL.Path == "/internal/files/delete":
		h.handleDeleteFile(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/uploads/create":
		h.handleCreateUpload(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/internal/uploads/get":
		h.handleGetUpload(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/uploads/add-part":
		h.handleAddUploadPart(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/uploads/complete":
		h.handleCompleteUpload(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/uploads/cancel":
		h.handleCancelUpload(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/batches/create":
		h.handleCreateBatch(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/internal/batches/get":
		h.handleGetBatch(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/internal/batches/list":
		h.handleListBatches(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/batches/cancel":
		h.handleCancelBatch(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/internal/batches/update-status":
		h.handleUpdateBatchStatus(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// --- File handlers ---

type createFileRequest struct {
	AccountID   string `json:"account_id"`
	Purpose     string `json:"purpose"`
	Filename    string `json:"filename"`
	Bytes       int64  `json:"bytes"`
	StoragePath string `json:"storage_path"`
}

func (h *handler) handleCreateFile(w http.ResponseWriter, r *http.Request) {
	var req createFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	req.AccountID = strings.TrimSpace(req.AccountID)
	req.Filename = strings.TrimSpace(req.Filename)
	if req.AccountID == "" || req.Filename == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id and filename are required"})
		return
	}

	f, err := h.svc.CreateFile(r.Context(), req.AccountID, req.Purpose, req.Filename, req.Bytes, req.StoragePath)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, fileToResponse(f))
}

func (h *handler) handleGetFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if id == "" || accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id and account_id are required"})
		return
	}

	f, err := h.svc.GetFile(r.Context(), id, accountID)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, fileToResponse(*f))
}

type fileResponse struct {
	ID        string  `json:"id"`
	Object    string  `json:"object"`
	Bytes     int64   `json:"bytes"`
	CreatedAt int64   `json:"created_at"`
	Filename  string  `json:"filename"`
	Purpose   string  `json:"purpose"`
	Status    string  `json:"status"`
}

func (h *handler) handleListFiles(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id is required"})
		return
	}

	var purpose *string
	if p := r.URL.Query().Get("purpose"); p != "" {
		purpose = &p
	}

	files, err := h.svc.ListFiles(r.Context(), accountID, purpose)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	data := make([]fileResponse, 0, len(files))
	for _, f := range files {
		data = append(data, fileToResponse(f))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

func (h *handler) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if id == "" || accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id and account_id are required"})
		return
	}

	if err := h.svc.DeleteFile(r.Context(), id, accountID); err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":      id,
		"object":  "file",
		"deleted": true,
	})
}

// --- Upload handlers ---

type createUploadRequest struct {
	AccountID string `json:"account_id"`
	Filename  string `json:"filename"`
	Bytes     int64  `json:"bytes"`
	MimeType  string `json:"mime_type"`
	Purpose   string `json:"purpose"`
}

func (h *handler) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	var req createUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	req.AccountID = strings.TrimSpace(req.AccountID)
	req.Filename = strings.TrimSpace(req.Filename)
	if req.AccountID == "" || req.Filename == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id and filename are required"})
		return
	}

	u, err := h.svc.CreateUpload(r.Context(), req.AccountID, req.Filename, req.Bytes, req.MimeType, req.Purpose)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, uploadToResponse(u, nil))
}

func (h *handler) handleGetUpload(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if id == "" || accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id and account_id are required"})
		return
	}

	u, err := h.svc.GetUpload(r.Context(), id, accountID)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, uploadToResponse(*u, nil))
}

type addUploadPartRequest struct {
	UploadID string `json:"upload_id"`
	PartID   string `json:"part_id"`
	PartNum  int    `json:"part_num"`
	ETag     string `json:"etag"`
}

func (h *handler) handleAddUploadPart(w http.ResponseWriter, r *http.Request) {
	var req addUploadPartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	if req.UploadID == "" || req.PartID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload_id and part_id are required"})
		return
	}

	p, err := h.svc.AddUploadPart(r.Context(), req.UploadID, req.PartID, req.PartNum, req.ETag)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":         p.ID,
		"object":     "upload.part",
		"created_at": p.CreatedAt.Unix(),
		"upload_id":  p.UploadID,
	})
}

type completeUploadRequest struct {
	UploadID  string   `json:"upload_id"`
	AccountID string   `json:"account_id"`
	PartIDs   []string `json:"part_ids"`
}

func (h *handler) handleCompleteUpload(w http.ResponseWriter, r *http.Request) {
	var req completeUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	req.UploadID = strings.TrimSpace(req.UploadID)
	req.AccountID = strings.TrimSpace(req.AccountID)
	if req.UploadID == "" || req.AccountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload_id and account_id are required"})
		return
	}

	f, err := h.svc.CompleteUpload(r.Context(), req.UploadID, req.AccountID)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":         req.UploadID,
		"object":     "upload",
		"status":     "completed",
		"created_at": f.CreatedAt.Unix(),
		"file":       fileToResponse(f),
	})
}

type cancelUploadRequest struct {
	UploadID  string `json:"upload_id"`
	AccountID string `json:"account_id"`
}

func (h *handler) handleCancelUpload(w http.ResponseWriter, r *http.Request) {
	var req cancelUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	req.UploadID = strings.TrimSpace(req.UploadID)
	req.AccountID = strings.TrimSpace(req.AccountID)
	if req.UploadID == "" || req.AccountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload_id and account_id are required"})
		return
	}

	if err := h.svc.CancelUpload(r.Context(), req.UploadID, req.AccountID); err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":     req.UploadID,
		"object": "upload",
		"status": "cancelled",
	})
}

// --- Batch handlers ---

type createBatchRequest struct {
	AccountID        string `json:"account_id"`
	InputFileID      string `json:"input_file_id"`
	Endpoint         string `json:"endpoint"`
	CompletionWindow string `json:"completion_window"`
}

func (h *handler) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	var req createBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	req.AccountID = strings.TrimSpace(req.AccountID)
	req.InputFileID = strings.TrimSpace(req.InputFileID)
	if req.AccountID == "" || req.InputFileID == "" || req.Endpoint == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id, input_file_id, and endpoint are required"})
		return
	}

	b, err := h.svc.CreateBatch(r.Context(), req.AccountID, req.InputFileID, req.Endpoint, req.CompletionWindow)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, batchToResponse(b))
}

func (h *handler) handleGetBatch(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if id == "" || accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id and account_id are required"})
		return
	}

	b, err := h.svc.GetBatch(r.Context(), id, accountID)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, batchToResponse(*b))
}

func (h *handler) handleListBatches(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimSpace(r.URL.Query().Get("account_id"))
	if accountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account_id is required"})
		return
	}

	limit := 20
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			limit = n
		}
	}

	var after *string
	if a := r.URL.Query().Get("after"); a != "" {
		after = &a
	}

	batches, err := h.svc.ListBatches(r.Context(), accountID, limit, after)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	data := make([]interface{}, 0, len(batches))
	for _, b := range batches {
		data = append(data, batchToResponse(b))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

type cancelBatchRequest struct {
	BatchID   string `json:"batch_id"`
	AccountID string `json:"account_id"`
}

func (h *handler) handleCancelBatch(w http.ResponseWriter, r *http.Request) {
	var req cancelBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	req.BatchID = strings.TrimSpace(req.BatchID)
	req.AccountID = strings.TrimSpace(req.AccountID)
	if req.BatchID == "" || req.AccountID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "batch_id and account_id are required"})
		return
	}

	b, err := h.svc.CancelBatch(r.Context(), req.BatchID, req.AccountID)
	if err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, batchToResponse(*b))
}

type updateBatchStatusRequest struct {
	BatchID string                 `json:"batch_id"`
	Status  string                 `json:"status"`
	Fields  map[string]interface{} `json:"fields"`
}

func (h *handler) handleUpdateBatchStatus(w http.ResponseWriter, r *http.Request) {
	var req updateBatchStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	req.BatchID = strings.TrimSpace(req.BatchID)
	req.Status = strings.TrimSpace(req.Status)
	if req.BatchID == "" || req.Status == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "batch_id and status are required"})
		return
	}

	if err := h.svc.UpdateBatchStatus(r.Context(), req.BatchID, req.Status, req.Fields); err != nil {
		writeFilestoreError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Response helpers ---

func fileToResponse(f File) fileResponse {
	return fileResponse{
		ID:        f.ID,
		Object:    "file",
		Bytes:     f.Bytes,
		CreatedAt: f.CreatedAt.Unix(),
		Filename:  f.Filename,
		Purpose:   f.Purpose,
		Status:    f.Status,
	}
}

type uploadResponse struct {
	ID        string        `json:"id"`
	Object    string        `json:"object"`
	Bytes     int64         `json:"bytes"`
	CreatedAt int64         `json:"created_at"`
	Filename  string        `json:"filename"`
	Purpose   string        `json:"purpose"`
	Status    string        `json:"status"`
	ExpiresAt int64         `json:"expires_at"`
	File      *fileResponse `json:"file,omitempty"`
}

func uploadToResponse(u Upload, file *File) uploadResponse {
	resp := uploadResponse{
		ID:        u.ID,
		Object:    "upload",
		Bytes:     u.Bytes,
		CreatedAt: u.CreatedAt.Unix(),
		Filename:  u.Filename,
		Purpose:   u.Purpose,
		Status:    u.Status,
		ExpiresAt: u.ExpiresAt.Unix(),
	}
	if file != nil {
		fr := fileToResponse(*file)
		resp.File = &fr
	}
	return resp
}

type batchResponse struct {
	ID               string `json:"id"`
	Object           string `json:"object"`
	Endpoint         string `json:"endpoint"`
	Status           string `json:"status"`
	InputFileID      string `json:"input_file_id"`
	CompletionWindow string `json:"completion_window"`
	CreatedAt        int64  `json:"created_at"`
	ExpiresAt        int64  `json:"expires_at"`
	RequestCounts    struct {
		Total     int `json:"total"`
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
	} `json:"request_counts"`
}

func batchToResponse(b Batch) batchResponse {
	resp := batchResponse{
		ID:               b.ID,
		Object:           "batch",
		Endpoint:         b.Endpoint,
		Status:           b.Status,
		InputFileID:      b.InputFileID,
		CompletionWindow: b.CompletionWindow,
		CreatedAt:        b.CreatedAt.Unix(),
		ExpiresAt:        b.ExpiresAt.Unix(),
	}
	resp.RequestCounts.Total = b.RequestCountsTotal
	resp.RequestCounts.Completed = b.RequestCountsCompleted
	resp.RequestCounts.Failed = b.RequestCountsFailed
	return resp
}

func writeFilestoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
