package files

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FilestoreClient calls the control-plane internal filestore and upload HTTP endpoints.
type FilestoreClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewFilestoreClient creates a FilestoreClient pointing at the control-plane base URL.
func NewFilestoreClient(controlPlaneURL string) *FilestoreClient {
	return &FilestoreClient{
		baseURL:    strings.TrimRight(controlPlaneURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// CreateFile creates file metadata in the control-plane filestore.
// POST /internal/files/create
func (c *FilestoreClient) CreateFile(ctx context.Context, accountID, purpose, filename string, fileBytes int64, storagePath string) (*FileObject, error) {
	body := map[string]interface{}{
		"account_id":   accountID,
		"purpose":      purpose,
		"filename":     filename,
		"bytes":        fileBytes,
		"storage_path": storagePath,
	}
	var resp fileResponse
	if err := c.post(ctx, "/internal/files/create", body, &resp); err != nil {
		return nil, fmt.Errorf("filestore: create file: %w", err)
	}
	return &FileObject{
		ID:          resp.ID,
		Object:      "file",
		Bytes:       resp.Bytes,
		CreatedAt:   resp.CreatedAt,
		Filename:    resp.Filename,
		Purpose:     resp.Purpose,
		Status:      resp.Status,
		StoragePath: storagePath,
	}, nil
}

// GetFile retrieves file metadata for a specific account (enforces ownership).
// GET /internal/files/get?id={id}&account_id={account_id}
func (c *FilestoreClient) GetFile(ctx context.Context, id, accountID string) (*FileObject, error) {
	params := url.Values{}
	params.Set("id", id)
	params.Set("account_id", accountID)

	var resp fileGetResponse
	if err := c.get(ctx, "/internal/files/get?"+params.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("filestore: get file: %w", err)
	}
	return &FileObject{
		ID:          resp.ID,
		Object:      "file",
		Bytes:       resp.Bytes,
		CreatedAt:   resp.CreatedAt,
		Filename:    resp.Filename,
		Purpose:     resp.Purpose,
		Status:      resp.Status,
		StoragePath: resp.StoragePath,
	}, nil
}

// ListFiles lists files for an account with optional purpose filter.
// GET /internal/files/list?account_id={account_id}&purpose={purpose}
func (c *FilestoreClient) ListFiles(ctx context.Context, accountID string, purpose *string) (*FileListResponse, error) {
	params := url.Values{}
	params.Set("account_id", accountID)
	if purpose != nil && *purpose != "" {
		params.Set("purpose", *purpose)
	}

	var resp fileListAPIResponse
	if err := c.get(ctx, "/internal/files/list?"+params.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("filestore: list files: %w", err)
	}

	objects := make([]FileObject, 0, len(resp.Data))
	for _, f := range resp.Data {
		objects = append(objects, FileObject{
			ID:        f.ID,
			Object:    "file",
			Bytes:     f.Bytes,
			CreatedAt: f.CreatedAt,
			Filename:  f.Filename,
			Purpose:   f.Purpose,
			Status:    f.Status,
		})
	}
	return &FileListResponse{Object: "list", Data: objects}, nil
}

// DeleteFile removes file metadata from the control-plane filestore.
// DELETE /internal/files/delete?id={id}&account_id={account_id}
func (c *FilestoreClient) DeleteFile(ctx context.Context, id, accountID string) error {
	params := url.Values{}
	params.Set("id", id)
	params.Set("account_id", accountID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.baseURL+"/internal/files/delete?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("filestore: delete file: build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("filestore: delete file: request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("filestore: delete file: status %d", resp.StatusCode)
	}
	return nil
}

// CreateUpload creates a multipart upload record in the control-plane.
// POST /internal/uploads/create
func (c *FilestoreClient) CreateUpload(ctx context.Context, accountID, filename string, fileBytes int64, mimeType, purpose string) (*UploadObject, error) {
	body := map[string]interface{}{
		"account_id": accountID,
		"filename":   filename,
		"bytes":      fileBytes,
		"mime_type":  mimeType,
		"purpose":    purpose,
	}
	var resp uploadAPIResponse
	if err := c.post(ctx, "/internal/uploads/create", body, &resp); err != nil {
		return nil, fmt.Errorf("filestore: create upload: %w", err)
	}
	return apiResponseToUpload(resp), nil
}

// GetUpload retrieves upload metadata for a specific account.
// GET /internal/uploads/get?id={id}&account_id={account_id}
func (c *FilestoreClient) GetUpload(ctx context.Context, id, accountID string) (*UploadObject, error) {
	params := url.Values{}
	params.Set("id", id)
	params.Set("account_id", accountID)

	var resp uploadAPIResponse
	if err := c.get(ctx, "/internal/uploads/get?"+params.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("filestore: get upload: %w", err)
	}
	return apiResponseToUpload(resp), nil
}

// AddUploadPart records an uploaded part in the control-plane.
// POST /internal/uploads/add-part
func (c *FilestoreClient) AddUploadPart(ctx context.Context, uploadID, partID string, partNum int, etag string) (*UploadPartObject, error) {
	body := map[string]interface{}{
		"upload_id": uploadID,
		"part_id":   partID,
		"part_num":  partNum,
		"etag":      etag,
	}
	var resp uploadPartAPIResponse
	if err := c.post(ctx, "/internal/uploads/add-part", body, &resp); err != nil {
		return nil, fmt.Errorf("filestore: add upload part: %w", err)
	}
	return &UploadPartObject{
		ID:        resp.ID,
		Object:    "upload.part",
		CreatedAt: resp.CreatedAt,
		UploadID:  resp.UploadID,
	}, nil
}

// CompleteUpload finalizes a multipart upload and creates the resulting File record.
// POST /internal/uploads/complete
func (c *FilestoreClient) CompleteUpload(ctx context.Context, uploadID, accountID string, partIDs []string) (*UploadObject, error) {
	body := map[string]interface{}{
		"upload_id":  uploadID,
		"account_id": accountID,
		"part_ids":   partIDs,
	}
	var resp completeUploadAPIResponse
	if err := c.post(ctx, "/internal/uploads/complete", body, &resp); err != nil {
		return nil, fmt.Errorf("filestore: complete upload: %w", err)
	}

	u := &UploadObject{
		ID:        resp.ID,
		Object:    "upload",
		CreatedAt: resp.CreatedAt,
		Status:    resp.Status,
	}
	if resp.File != nil {
		u.File = &FileObject{
			ID:        resp.File.ID,
			Object:    "file",
			Bytes:     resp.File.Bytes,
			CreatedAt: resp.File.CreatedAt,
			Filename:  resp.File.Filename,
			Purpose:   resp.File.Purpose,
			Status:    resp.File.Status,
		}
	}
	return u, nil
}

// CancelUpload aborts a multipart upload.
// POST /internal/uploads/cancel
func (c *FilestoreClient) CancelUpload(ctx context.Context, uploadID, accountID string) (*UploadObject, error) {
	body := map[string]interface{}{
		"upload_id":  uploadID,
		"account_id": accountID,
	}
	var resp cancelUploadAPIResponse
	if err := c.post(ctx, "/internal/uploads/cancel", body, &resp); err != nil {
		return nil, fmt.Errorf("filestore: cancel upload: %w", err)
	}
	return &UploadObject{
		ID:        resp.ID,
		Object:    "upload",
		Status:    resp.Status,
		CreatedAt: resp.CreatedAt,
		ExpiresAt: resp.ExpiresAt,
	}, nil
}

// --- Internal response types for deserializing control-plane responses ---

type fileResponse struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Bytes       int64  `json:"bytes"`
	CreatedAt   int64  `json:"created_at"`
	Filename    string `json:"filename"`
	Purpose     string `json:"purpose"`
	Status      string `json:"status"`
	StoragePath string `json:"storage_path"`
}

// fileGetResponse is used when the control-plane returns full file detail including storage_path.
type fileGetResponse struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Bytes       int64  `json:"bytes"`
	CreatedAt   int64  `json:"created_at"`
	Filename    string `json:"filename"`
	Purpose     string `json:"purpose"`
	Status      string `json:"status"`
	StoragePath string `json:"storage_path"`
}

type fileListAPIResponse struct {
	Object string         `json:"object"`
	Data   []fileResponse `json:"data"`
}

type uploadAPIResponse struct {
	ID          string        `json:"id"`
	Object      string        `json:"object"`
	Bytes       int64         `json:"bytes"`
	CreatedAt   int64         `json:"created_at"`
	Filename    string        `json:"filename"`
	Purpose     string        `json:"purpose"`
	Status      string        `json:"status"`
	ExpiresAt   int64         `json:"expires_at"`
	S3UploadID  *string       `json:"s3_upload_id"`
	StoragePath string        `json:"storage_path"`
	File        *fileResponse `json:"file,omitempty"`
}

type uploadPartAPIResponse struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	CreatedAt int64  `json:"created_at"`
	UploadID  string `json:"upload_id"`
}

type completeUploadAPIResponse struct {
	ID        string        `json:"id"`
	Object    string        `json:"object"`
	Status    string        `json:"status"`
	CreatedAt int64         `json:"created_at"`
	File      *fileResponse `json:"file,omitempty"`
}

type cancelUploadAPIResponse struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func apiResponseToUpload(resp uploadAPIResponse) *UploadObject {
	u := &UploadObject{
		ID:          resp.ID,
		Object:      "upload",
		Bytes:       resp.Bytes,
		CreatedAt:   resp.CreatedAt,
		Filename:    resp.Filename,
		Purpose:     resp.Purpose,
		Status:      resp.Status,
		ExpiresAt:   resp.ExpiresAt,
		S3UploadID:  resp.S3UploadID,
		StoragePath: resp.StoragePath,
	}
	if resp.File != nil {
		u.File = &FileObject{
			ID:        resp.File.ID,
			Object:    "file",
			Bytes:     resp.File.Bytes,
			CreatedAt: resp.File.CreatedAt,
			Filename:  resp.File.Filename,
			Purpose:   resp.File.Purpose,
			Status:    resp.File.Status,
		}
	}
	return u
}

// --- HTTP helpers ---

func (c *FilestoreClient) post(ctx context.Context, path string, input any, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if output != nil {
		if err := json.Unmarshal(body, output); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
	}
	return nil
}

func (c *FilestoreClient) get(ctx context.Context, pathAndQuery string, output any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+pathAndQuery, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if output != nil {
		if err := json.Unmarshal(body, output); err != nil {
			return fmt.Errorf("decode: %w", err)
		}
	}
	return nil
}
