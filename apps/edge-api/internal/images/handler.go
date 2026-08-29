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

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// Capability flags used when this handler calls the routing layer.
// These constants document which routing capabilities image endpoints require.
const (
	// NeedImageGeneration is the routing capability required for /v1/images/generations.
	NeedImageGeneration = true
	// NeedImageEdit is the routing capability required for /v1/images/edits.
	NeedImageEdit = true
)

// imageReservationCredits is the flat pre-dispatch hold both image endpoints
// take: $0.05 equivalent at the current credit unit (1 USD = 1e9 credits
// since migration 20260823_40_credit_unit_rescale_billion.sql; previously
// 5,000 credits at 100k per USD, the same real money). It is an
// authorization floor, never a charge: settlement replaces it with the
// catalog-priced cost.
const imageReservationCredits int64 = 50_000_000

// Authorizer validates incoming API keys and returns account context.
type Authorizer interface {
	AuthorizeRequest(r *http.Request) (AuthResult, error)
}

// AuthResult carries the authorized account and API key identifiers.
type AuthResult struct {
	AccountID string
	APIKeyID  string
	// TenantID is resolved control-plane-side from AccountID (D-030, via
	// public.tenant_billing_accounts). Empty means the account has no
	// resolvable tenant; RoutingAdapter fails the request closed on that
	// rather than skipping the tenant-scoped entitlement check (#623).
	TenantID string
}

// RoutingInterface selects a provider route for a given model alias.
type RoutingInterface interface {
	SelectRoute(ctx context.Context, input RouteInput) (RouteResult, error)
}

// RouteInput specifies the alias and capability requirements for route selection.
type RouteInput struct {
	AliasID string
	// TenantID is the requesting API key's resolved tenant (AuthResult.TenantID).
	// RoutingAdapter binds it onto ctx before calling the routing client and
	// fails closed if it is missing or unparseable (#623).
	TenantID string
	// AccountID and APIKeyID are threaded through for exactly one reason:
	// authz.ParseTenantID logs them when TenantID fails to parse, so an
	// account_not_provisioned 403 from this endpoint is operator-visible the
	// same way it is on /v1/models and /v1/chat/completions.
	AccountID           string
	APIKeyID            string
	NeedImageGeneration bool
	NeedImageEdit       bool
}

// RouteResult contains the selected route details.
type RouteResult struct {
	AliasID          string
	LiteLLMModelName string
}

// AccountingInterface manages credit reservations for image requests.
type AccountingInterface interface {
	CreateReservation(ctx context.Context, input ReservationInput) (string, error)
	FinalizeReservation(ctx context.Context, input FinalizeInput) error
	ReleaseReservation(ctx context.Context, accountID, reservationID, reason string) error
}

// ReservationInput holds the parameters for creating a credit reservation.
type ReservationInput struct {
	AccountID        string
	APIKeyID         string
	RequestID        string
	Endpoint         string
	ModelAlias       string
	EstimatedCredits int64
}

// FinalizeInput holds the parameters for finalizing a credit reservation.
type FinalizeInput struct {
	AccountID     string
	ReservationID string
	ActualCredits int64
}

// StorageInterface abstracts S3-compatible storage for testability.
type StorageInterface interface {
	Upload(ctx context.Context, bucket, key string, reader io.Reader, size int64, contentType string) error
	PresignedURL(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// Handler routes image requests to generation, edits, or variations endpoints.
type Handler struct {
	authorizer     Authorizer
	routing        RoutingInterface
	accounting     AccountingInterface
	litellmBaseURL string
	masterKey      string
	httpClient     *http.Client
	storage        StorageInterface
	bucket         string
}

// NewHandler creates a new image Handler.
func NewHandler(
	authorizer Authorizer,
	routing RoutingInterface,
	accounting AccountingInterface,
	litellmBaseURL string,
	masterKey string,
	storage StorageInterface,
	bucket string,
) *Handler {
	return &Handler{
		authorizer:     authorizer,
		routing:        routing,
		accounting:     accounting,
		litellmBaseURL: strings.TrimRight(litellmBaseURL, "/"),
		masterKey:      masterKey,
		httpClient:     &http.Client{Timeout: 120 * time.Second},
		storage:        storage,
		bucket:         bucket,
	}
}

// authorize validates the request API key and writes a 401 on failure.
// Returns (result, true) on success or (zero, false) on failure (response already written).
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (AuthResult, bool) {
	result, err := h.authorizer.AuthorizeRequest(r)
	if err != nil {
		if ae, ok := authz.AsAuthzError(err); ok {
			apierrors.WriteAuthFailure(w, ae.OpenAIErr, ae.Headers)
			return AuthResult{}, false
		}
		code := "invalid_api_key"
		apierrors.WriteError(w, http.StatusUnauthorized, "invalid_request_error", "Invalid API key.", &code)
		return AuthResult{}, false
	}
	return result, true
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

	// Authorize before reading body.
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

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

	// Select route based on model alias and capability.
	route, err := h.routing.SelectRoute(ctx, RouteInput{
		AliasID:             req.Model,
		TenantID:            auth.TenantID,
		AccountID:           auth.AccountID,
		APIKeyID:            auth.APIKeyID,
		NeedImageGeneration: true,
	})
	if err != nil {
		code := "model_not_found"
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "The requested model is not available for image generation.", &code)
		return
	}

	// Reserve credits before dispatch.
	requestID := uuid.New().String()
	reservationID, err := h.accounting.CreateReservation(ctx, ReservationInput{
		AccountID:        auth.AccountID,
		APIKeyID:         auth.APIKeyID,
		RequestID:        requestID,
		Endpoint:         "/v1/images/generations",
		ModelAlias:       route.AliasID,
		EstimatedCredits: imageReservationCredits,
	})
	if err != nil {
		code := "insufficient_quota"
		apierrors.WriteError(w, http.StatusPaymentRequired, "invalid_request_error", "Insufficient credits to complete this request.", &code)
		return
	}

	// Rewrite the model field to the LiteLLM model name.
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "request_error")
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body.", &code)
		return
	}
	bodyMap["model"] = route.LiteLLMModelName
	rewrittenBody, err := json.Marshal(bodyMap)
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "request_error")
		code := "internal_error"
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to serialize request.", &code)
		return
	}

	// Determine response_format (default is "url").
	responseFormat := "url"
	if req.ResponseFormat != nil && *req.ResponseFormat == "b64_json" {
		responseFormat = "b64_json"
	}

	// Dispatch to LiteLLM.
	upstreamResp, err := h.dispatchJSON(ctx, "/images/generations", rewrittenBody)
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		apierrors.WriteProviderBlindUpstreamError(w, req.Model, http.StatusBadGateway, err.Error())
		return
	}
	defer upstreamResp.Body.Close()

	if upstreamResp.StatusCode < 200 || upstreamResp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(upstreamResp.Body, 4096))
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		apierrors.WriteProviderBlindUpstreamError(w, req.Model, upstreamResp.StatusCode, string(upstreamBody))
		return
	}

	respBody, err := io.ReadAll(io.LimitReader(upstreamResp.Body, 10*1024*1024))
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response.", &code)
		return
	}

	var imageResp ImageResponse
	if err := json.Unmarshal(respBody, &imageResp); err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response.", &code)
		return
	}

	// A 2xx carrying no usable image is a fake success (#1319). Every SDK
	// reports it to the caller as success, so their code fails somewhere
	// further away from the cause, and a retry loop built on it can never
	// recover. Refuse it here, BEFORE settlement, so the flat hold is
	// released rather than charged for nothing delivered.
	if !hasImagePayload(imageResp.Data) {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		apierrors.WriteProviderBlindUpstreamError(w, req.Model, http.StatusBadGateway, upstreamErrorSnippet(respBody))
		return
	}

	// Finalize reservation on success; falls back to releasing the hold if
	// finalize itself fails, so it never strands (#616).
	h.settleReservation(ctx, auth.AccountID, reservationID, imageReservationCredits, "/v1/images/generations")

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

	// Authorize before parsing multipart form.
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

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

	// Extract model alias from form.
	modelAlias := r.FormValue("model")

	// Select route based on model alias and capability.
	route, err := h.routing.SelectRoute(ctx, RouteInput{
		AliasID:       modelAlias,
		TenantID:      auth.TenantID,
		AccountID:     auth.AccountID,
		APIKeyID:      auth.APIKeyID,
		NeedImageEdit: true,
	})
	if err != nil {
		code := "model_not_found"
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "The requested model is not available for image edits.", &code)
		return
	}

	// Reserve credits before dispatch.
	requestID := uuid.New().String()
	reservationID, err := h.accounting.CreateReservation(ctx, ReservationInput{
		AccountID:        auth.AccountID,
		APIKeyID:         auth.APIKeyID,
		RequestID:        requestID,
		Endpoint:         "/v1/images/edits",
		ModelAlias:       route.AliasID,
		EstimatedCredits: imageReservationCredits,
	})
	if err != nil {
		code := "insufficient_quota"
		apierrors.WriteError(w, http.StatusPaymentRequired, "invalid_request_error", "Insufficient credits to complete this request.", &code)
		return
	}

	// Capture the LiteLLM model name for use inside the goroutine.
	litellmModel := route.LiteLLMModelName

	// Rebuild multipart body for LiteLLM using io.Pipe for streaming.
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer mw.Close()

		// Copy all text form fields, rewriting the model field.
		for key, values := range r.MultipartForm.Value {
			for _, val := range values {
				writeVal := val
				if key == "model" {
					writeVal = litellmModel
				}
				if err := mw.WriteField(key, writeVal); err != nil {
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
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, pr)
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to build upstream request.", &code)
		return
	}
	upstreamReq.Header.Set("Content-Type", mw.FormDataContentType())
	upstreamReq.Header.Set("Authorization", "Bearer "+h.masterKey)

	upstreamResp, err := h.httpClient.Do(upstreamReq)
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		apierrors.WriteProviderBlindUpstreamError(w, "", http.StatusBadGateway, err.Error())
		return
	}
	defer upstreamResp.Body.Close()

	if upstreamResp.StatusCode < 200 || upstreamResp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(upstreamResp.Body, 4096))
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		apierrors.WriteProviderBlindUpstreamError(w, "", upstreamResp.StatusCode, string(upstreamBody))
		return
	}

	respBody, err := io.ReadAll(io.LimitReader(upstreamResp.Body, 10*1024*1024))
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response.", &code)
		return
	}

	var imageResp ImageResponse
	if err := json.Unmarshal(respBody, &imageResp); err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to parse upstream response.", &code)
		return
	}

	// Same empty-success refusal as the generation path above (#1319): an
	// edit that returns no image is not an edit.
	if !hasImagePayload(imageResp.Data) {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		apierrors.WriteProviderBlindUpstreamError(w, modelAlias, http.StatusBadGateway, upstreamErrorSnippet(respBody))
		return
	}

	// Finalize reservation on success; falls back to releasing the hold if
	// finalize itself fails, so it never strands (#616).
	h.settleReservation(ctx, auth.AccountID, reservationID, imageReservationCredits, "/v1/images/edits")

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

// hasImagePayload reports whether an upstream image response actually carries
// an image the caller can use.
//
// It counts payloads rather than array entries on purpose: `data: []` and
// `data: [{}]` are the same answer to the caller, and a guard that only
// checked the length would pass the second one straight through as a success
// (#1319). revised_prompt alone is metadata about an image, not an image.
func hasImagePayload(data []ImageData) bool {
	for _, item := range data {
		if item.URL != nil && *item.URL != "" {
			return true
		}
		if item.B64JSON != nil && *item.B64JSON != "" {
			return true
		}
	}
	return false
}

// upstreamErrorSnippet bounds how much of an upstream body is handed to the
// provider-blind sanitizer, which also writes it to the operator log. The
// error paths that read a non-2xx body already cap themselves at this size
// with io.LimitReader; the empty-image guard reads a 2xx body capped at 10
// MiB instead, and a multi-megabyte log line helps nobody.
func upstreamErrorSnippet(body []byte) string {
	const maxSnippetBytes = 4096
	if len(body) > maxSnippetBytes {
		return string(body[:maxSnippetBytes])
	}
	return string(body)
}
