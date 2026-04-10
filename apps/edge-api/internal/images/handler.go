package images

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
	"github.com/google/uuid"
)

// Capability flags used when this handler calls the routing layer.
// These constants document which routing capabilities image endpoints require.
const (
	// NeedImageGeneration is the routing capability required for /v1/images/generations.
	NeedImageGeneration = true
	// NeedImageEdit is the routing capability required for /v1/images/edits.
	NeedImageEdit = true
)

// StorageInterface abstracts S3-compatible storage for testability.
type StorageInterface interface {
	Upload(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) error
	PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// Handler routes image requests to generation, edits, or variations endpoints.
type Handler struct {
	litellmBaseURL string
	masterKey      string
	httpClient     *http.Client
	storage        StorageInterface
	bucket         string
}

// NewHandler creates a new image Handler.
func NewHandler(
	litellmBaseURL string,
	masterKey string,
	storage StorageInterface,
	bucket string,
) *Handler {
	return &Handler{
		litellmBaseURL: strings.TrimRight(litellmBaseURL, "/"),
		masterKey:      masterKey,
		httpClient:     &http.Client{Timeout: 120 * time.Second},
		storage:        storage,
		bucket:         bucket,
	}
}

// ServeHTTP dispatches image requests by URL path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apierrors.WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed", nil)
		return
	}

	switch r.URL.Path {
	case "/v1/images/generations":
		h.handleGeneration(w, r)
	case "/v1/images/edits":
		h.handleEdit(w, r)
	case "/v1/images/variations":
		code := "unsupported_operation"
		apierrors.WriteError(w, http.StatusNotImplemented, "invalid_request_error",
			"Image variations are not supported. Use generations or edits instead.", &code)
	default:
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "Unknown endpoint", nil)
	}
}

// handleGeneration processes POST /v1/images/generations.
func (h *Handler) handleGeneration(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
	if err != nil {
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body.", &code)
		return
	}

	// Parse request to determine response_format and model alias.
	var req ImageGenerationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body.", &code)
		return
	}

	// Determine response_format (default is "url").
	responseFormat := "url"
	if req.ResponseFormat != nil && *req.ResponseFormat == "b64_json" {
		responseFormat = "b64_json"
	}

	// Dispatch to LiteLLM.
	upstreamResp, err := h.dispatchJSON(ctx, "/images/generations", body)
	if err != nil {
		apierrors.WriteProviderBlindUpstreamError(w, req.Model, http.StatusBadGateway, err.Error())
		return
	}
	defer upstreamResp.Body.Close()

	if upstreamResp.StatusCode < 200 || upstreamResp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(upstreamResp.Body, 4096))
		apierrors.WriteProviderBlindUpstreamError(w, req.Model, upstreamResp.StatusCode, string(upstreamBody))
		return
	}

	respBody, err := io.ReadAll(io.LimitReader(upstreamResp.Body, 10*1024*1024))
	if err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response.", &code)
		return
	}

	var imageResp ImageResponse
	if err := json.Unmarshal(respBody, &imageResp); err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response.", &code)
		return
	}

	// Normalize: for URL mode, upload each image to S3 and replace with presigned URL.
	if responseFormat == "url" {
		for i, item := range imageResp.Data {
			if item.URL == nil {
				continue
			}
			signedURL, err := h.uploadProviderImage(ctx, *item.URL)
			if err != nil {
				// Non-fatal: leave the URL as-is and log.
				continue
			}
			imageResp.Data[i].URL = &signedURL
		}
	}

	normalized, err := json.Marshal(imageResp)
	if err != nil {
		code := "internal_error"
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to serialize response.", &code)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(normalized)
}

// handleEdit processes POST /v1/images/edits.
func (h *Handler) handleEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse multipart form (32MB limit).
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to parse multipart form.", &code)
		return
	}

	// Parse response_format from form values.
	responseFormat := "url"
	if rf := r.FormValue("response_format"); rf == "b64_json" {
		responseFormat = "b64_json"
	}

	// Rebuild multipart body for LiteLLM using io.Pipe for streaming.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer mw.Close()

		// Copy all text form fields.
		for key, values := range r.MultipartForm.Value {
			for _, val := range values {
				if err := mw.WriteField(key, val); err != nil {
					pw.CloseWithError(fmt.Errorf("write field %s: %w", key, err))
					return
				}
			}
		}

		// Stream all file parts.
		for fieldName, files := range r.MultipartForm.File {
			for _, fh := range files {
				f, err := fh.Open()
				if err != nil {
					pw.CloseWithError(fmt.Errorf("open file %s: %w", fieldName, err))
					return
				}
				fw, err := mw.CreateFormFile(fieldName, fh.Filename)
				if err != nil {
					f.Close()
					pw.CloseWithError(fmt.Errorf("create form file %s: %w", fieldName, err))
					return
				}
				if _, err := io.Copy(fw, f); err != nil {
					f.Close()
					pw.CloseWithError(fmt.Errorf("copy file %s: %w", fieldName, err))
					return
				}
				f.Close()
			}
		}
	}()

	// Build request to LiteLLM.
	upstreamURL := h.litellmBaseURL + "/images/edits"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, pr)
	if err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to build upstream request.", &code)
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+h.masterKey)

	upstreamResp, err := h.httpClient.Do(req)
	if err != nil {
		apierrors.WriteProviderBlindUpstreamError(w, "", http.StatusBadGateway, err.Error())
		return
	}
	defer upstreamResp.Body.Close()

	if upstreamResp.StatusCode < 200 || upstreamResp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(upstreamResp.Body, 4096))
		apierrors.WriteProviderBlindUpstreamError(w, "", upstreamResp.StatusCode, string(upstreamBody))
		return
	}

	respBody, err := io.ReadAll(io.LimitReader(upstreamResp.Body, 10*1024*1024))
	if err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response.", &code)
		return
	}

	var imageResp ImageResponse
	if err := json.Unmarshal(respBody, &imageResp); err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response.", &code)
		return
	}

	// Normalize: for URL mode, upload each image to S3 and replace with presigned URL.
	if responseFormat == "url" {
		for i, item := range imageResp.Data {
			if item.URL == nil {
				continue
			}
			signedURL, err := h.uploadProviderImage(ctx, *item.URL)
			if err != nil {
				continue
			}
			imageResp.Data[i].URL = &signedURL
		}
	}

	normalized, err := json.Marshal(imageResp)
	if err != nil {
		code := "internal_error"
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to serialize response.", &code)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(normalized)
}

// dispatchJSON sends a JSON body to LiteLLM at the given path.
func (h *Handler) dispatchJSON(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.litellmBaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("litellm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.masterKey)
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("litellm: request failed: %w", err)
	}
	return resp, nil
}

// uploadProviderImage downloads an image from providerURL and uploads it to S3,
// returning a 1-hour presigned URL.
func (h *Handler) uploadProviderImage(ctx context.Context, providerURL string) (string, error) {
	// Download the image from the provider URL.
	dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, providerURL, nil)
	if err != nil {
		return "", fmt.Errorf("images: build download request: %w", err)
	}
	dlResp, err := h.httpClient.Do(dlReq)
	if err != nil {
		return "", fmt.Errorf("images: download provider image: %w", err)
	}
	defer dlResp.Body.Close()

	ct := dlResp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/png"
	}

	// Determine file extension from content type.
	ext := "png"
	if strings.Contains(ct, "jpeg") || strings.Contains(ct, "jpg") {
		ext = "jpg"
	} else if strings.Contains(ct, "webp") {
		ext = "webp"
	} else if strings.Contains(ct, "gif") {
		ext = "gif"
	}

	key := fmt.Sprintf("images/%s.%s", uuid.New().String(), ext)

	// Upload to S3 (-1 size means unknown/streaming).
	if err := h.storage.Upload(ctx, h.bucket, key, dlResp.Body, -1, ct); err != nil {
		return "", fmt.Errorf("images: upload to storage: %w", err)
	}

	// Generate presigned URL with 1-hour TTL.
	u, err := h.storage.PresignedURL(ctx, h.bucket, key, time.Hour)
	if err != nil {
		return "", fmt.Errorf("images: presign URL: %w", err)
	}

	return u, nil
}
