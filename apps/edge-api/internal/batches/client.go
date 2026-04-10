package batches

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is returned when a batch resource does not exist for the given account.
var ErrNotFound = errors.New("not found")

// BatchClient calls the control-plane internal batch HTTP endpoints.
type BatchClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewBatchClient creates a new BatchClient pointing at the control-plane base URL.
func NewBatchClient(controlPlaneURL string) *BatchClient {
	return &BatchClient{
		baseURL:    strings.TrimRight(controlPlaneURL, "/"),
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// CreateBatch creates a new batch record in the control-plane.
// POST /internal/batches/create
func (c *BatchClient) CreateBatch(ctx context.Context, accountID, inputFileID, endpoint, completionWindow string, totalRequests int, reservationID string) (*BatchObject, error) {
	body := map[string]interface{}{
		"account_id":        accountID,
		"input_file_id":     inputFileID,
		"endpoint":          endpoint,
		"completion_window": completionWindow,
		"total_requests":    totalRequests,
		"reservation_id":    reservationID,
	}
	var resp batchAPIResponse
	if err := c.post(ctx, "/internal/batches/create", body, &resp); err != nil {
		return nil, fmt.Errorf("batchstore: create batch: %w", err)
	}
	return apiResponseToBatch(resp), nil
}

// GetBatch retrieves batch metadata for a specific account.
// GET /internal/batches/get?id={id}&account_id={account_id}
func (c *BatchClient) GetBatch(ctx context.Context, id, accountID string) (*BatchObject, error) {
	params := url.Values{}
	params.Set("id", id)
	params.Set("account_id", accountID)

	var resp batchAPIResponse
	if err := c.get(ctx, "/internal/batches/get?"+params.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("batchstore: get batch: %w", err)
	}
	return apiResponseToBatch(resp), nil
}

// ListBatches lists batches for an account with optional cursor-based pagination.
// GET /internal/batches/list?account_id={account_id}&limit={n}&after={id}
func (c *BatchClient) ListBatches(ctx context.Context, accountID string, limit int, after *string) (*BatchListResponse, error) {
	params := url.Values{}
	params.Set("account_id", accountID)
	params.Set("limit", strconv.Itoa(limit))
	if after != nil && *after != "" {
		params.Set("after", *after)
	}

	var resp batchListAPIResponse
	if err := c.get(ctx, "/internal/batches/list?"+params.Encode(), &resp); err != nil {
		return nil, fmt.Errorf("batchstore: list batches: %w", err)
	}

	objects := make([]BatchObject, 0, len(resp.Data))
	for _, b := range resp.Data {
		obj := apiResponseToBatch(b)
		objects = append(objects, *obj)
	}

	result := &BatchListResponse{
		Object:  "list",
		Data:    objects,
		HasMore: resp.HasMore,
	}
	if len(objects) > 0 {
		firstID := objects[0].ID
		lastID := objects[len(objects)-1].ID
		result.FirstID = &firstID
		result.LastID = &lastID
	}
	return result, nil
}

// CancelBatch cancels a batch job.
// POST /internal/batches/cancel
func (c *BatchClient) CancelBatch(ctx context.Context, id, accountID string) (*BatchObject, error) {
	body := map[string]interface{}{
		"batch_id":   id,
		"account_id": accountID,
	}
	var resp batchAPIResponse
	if err := c.post(ctx, "/internal/batches/cancel", body, &resp); err != nil {
		return nil, fmt.Errorf("batchstore: cancel batch: %w", err)
	}
	return apiResponseToBatch(resp), nil
}

// --- Internal response types ---

type batchAPIResponse struct {
	ID               string `json:"id"`
	Object           string `json:"object"`
	Endpoint         string `json:"endpoint"`
	Status           string `json:"status"`
	InputFileID      string `json:"input_file_id"`
	OutputFileID     *string `json:"output_file_id,omitempty"`
	ErrorFileID      *string `json:"error_file_id,omitempty"`
	CompletionWindow string `json:"completion_window"`
	CreatedAt        int64  `json:"created_at"`
	ExpiresAt        int64  `json:"expires_at"`
	RequestCounts    struct {
		Total     int `json:"total"`
		Completed int `json:"completed"`
		Failed    int `json:"failed"`
	} `json:"request_counts"`
}

type batchListAPIResponse struct {
	Object  string             `json:"object"`
	Data    []batchAPIResponse `json:"data"`
	HasMore bool               `json:"has_more"`
}

func apiResponseToBatch(resp batchAPIResponse) *BatchObject {
	b := &BatchObject{
		ID:               resp.ID,
		Object:           "batch",
		Endpoint:         resp.Endpoint,
		InputFileID:      resp.InputFileID,
		OutputFileID:     resp.OutputFileID,
		ErrorFileID:      resp.ErrorFileID,
		CompletionWindow: resp.CompletionWindow,
		Status:           resp.Status,
		CreatedAt:        resp.CreatedAt,
		RequestCounts: &BatchRequestCounts{
			Total:     resp.RequestCounts.Total,
			Completed: resp.RequestCounts.Completed,
			Failed:    resp.RequestCounts.Failed,
		},
	}
	if resp.ExpiresAt > 0 {
		ea := resp.ExpiresAt
		b.ExpiresAt = &ea
	}
	return b
}

// --- HTTP helpers ---

func (c *BatchClient) post(ctx context.Context, path string, input any, output any) error {
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

func (c *BatchClient) get(ctx context.Context, pathAndQuery string, output any) error {
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
