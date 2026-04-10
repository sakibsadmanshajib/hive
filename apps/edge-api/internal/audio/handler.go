package audio

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
// These constants document which routing capabilities audio endpoints require.
const (
	// NeedTTS is the routing capability required for /v1/audio/speech.
	NeedTTS = true
	// NeedSTT is the routing capability required for /v1/audio/transcriptions and /v1/audio/translations.
	NeedSTT = true
)

// Authorizer validates incoming API keys and returns account context.
type Authorizer interface {
	AuthorizeRequest(r *http.Request) (AuthResult, error)
}

// AuthResult carries the authorized account and API key identifiers.
type AuthResult struct {
	AccountID string
	APIKeyID  string
}

// RoutingInterface selects a provider route for a given model alias.
type RoutingInterface interface {
	SelectRoute(ctx context.Context, input RouteInput) (RouteResult, error)
}

// RouteInput specifies the alias and capability requirements for route selection.
type RouteInput struct {
	AliasID string
	NeedTTS bool
	NeedSTT bool
}

// RouteResult contains the selected route details.
type RouteResult struct {
	AliasID          string
	LiteLLMModelName string
}

// AccountingInterface manages credit reservations for audio requests.
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

// Handler routes audio requests to speech, transcription, and translation endpoints.
type Handler struct {
	authorizer     Authorizer
	routing        RoutingInterface
	accounting     AccountingInterface
	litellmBaseURL string
	masterKey      string
	httpClient     *http.Client
}

// NewHandler creates a new audio Handler.
func NewHandler(
	authorizer Authorizer,
	routing RoutingInterface,
	accounting AccountingInterface,
	litellmBaseURL, masterKey string,
) *Handler {
	return &Handler{
		authorizer:     authorizer,
		routing:        routing,
		accounting:     accounting,
		litellmBaseURL: strings.TrimRight(litellmBaseURL, "/"),
		masterKey:      masterKey,
		httpClient:     &http.Client{Timeout: 120 * time.Second},
	}
}

// authorize validates the request API key and writes a 401 on failure.
// Returns (result, true) on success or (zero, false) on failure (response already written).
func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (AuthResult, bool) {
	result, err := h.authorizer.AuthorizeRequest(r)
	if err != nil {
		code := "invalid_api_key"
		apierrors.WriteError(w, http.StatusUnauthorized, "invalid_request_error", "Invalid API key.", &code)
		return AuthResult{}, false
	}
	return result, true
}

// ServeHTTP dispatches audio requests by URL path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apierrors.WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed", nil)
		return
	}

	switch r.URL.Path {
	case "/v1/audio/speech":
		h.handleSpeech(w, r)
	case "/v1/audio/transcriptions":
		h.handleTranscription(w, r)
	case "/v1/audio/translations":
		h.handleTranslation(w, r)
	default:
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "Unknown endpoint", nil)
	}
}

// handleSpeech processes POST /v1/audio/speech.
// It pipes binary audio directly from LiteLLM to the client without buffering.
func (h *Handler) handleSpeech(w http.ResponseWriter, r *http.Request) {
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

	// Parse the request to extract the model alias.
	var speechReq SpeechRequest
	if err := json.Unmarshal(body, &speechReq); err != nil {
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body.", &code)
		return
	}

	// Select route based on model alias and TTS capability.
	route, err := h.routing.SelectRoute(ctx, RouteInput{
		AliasID: speechReq.Model,
		NeedTTS: true,
	})
	if err != nil {
		code := "model_not_found"
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "The requested model is not available for audio speech.", &code)
		return
	}

	// Reserve credits before dispatch.
	requestID := uuid.New().String()
	reservationID, err := h.accounting.CreateReservation(ctx, ReservationInput{
		AccountID:        auth.AccountID,
		APIKeyID:         auth.APIKeyID,
		RequestID:        requestID,
		Endpoint:         "/v1/audio/speech",
		ModelAlias:       route.AliasID,
		EstimatedCredits: 1000,
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

	// Dispatch to LiteLLM.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.litellmBaseURL+"/audio/speech", bytes.NewReader(rewrittenBody))
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to build upstream request.", &code)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.masterKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Upstream audio request failed.", &code)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, resp.StatusCode, "api_error", string(upstreamBody), &code)
		return
	}

	// Finalize reservation on success.
	_ = h.accounting.FinalizeReservation(ctx, FinalizeInput{
		AccountID:     auth.AccountID,
		ReservationID: reservationID,
		ActualCredits: 1000,
	})

	// Binary relay: copy Content-Type exactly from upstream, pipe body directly.
	ct := resp.Header.Get("Content-Type")
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body) //nolint:errcheck
}

// handleTranscription processes POST /v1/audio/transcriptions.
// Audio files are forwarded in-flight via multipart — never written to disk or storage.
func (h *Handler) handleTranscription(w http.ResponseWriter, r *http.Request) {
	h.handleMultipartAudio(w, r, "/audio/transcriptions", "/v1/audio/transcriptions")
}

// handleTranslation processes POST /v1/audio/translations.
// Audio files are forwarded in-flight via multipart — never written to disk or storage.
func (h *Handler) handleTranslation(w http.ResponseWriter, r *http.Request) {
	h.handleMultipartAudio(w, r, "/audio/translations", "/v1/audio/translations")
}

// handleMultipartAudio is shared logic for transcription and translation:
// rebuild multipart from the incoming request and forward to LiteLLM at the given path.
// litellmPath is the path segment appended to the LiteLLM base URL.
// accountingEndpoint is the full endpoint path used for credit reservation records.
func (h *Handler) handleMultipartAudio(w http.ResponseWriter, r *http.Request, litellmPath, accountingEndpoint string) {
	ctx := r.Context()

	// Authorize before parsing multipart form.
	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	// Parse multipart form (25MB for audio files).
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to parse multipart form.", &code)
		return
	}

	// Extract model alias from form.
	modelAlias := r.FormValue("model")

	// Select route based on model alias and STT capability.
	route, err := h.routing.SelectRoute(ctx, RouteInput{
		AliasID: modelAlias,
		NeedSTT: true,
	})
	if err != nil {
		code := "model_not_found"
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "The requested model is not available for audio transcription.", &code)
		return
	}

	// Reserve credits before dispatch.
	requestID := uuid.New().String()
	reservationID, err := h.accounting.CreateReservation(ctx, ReservationInput{
		AccountID:        auth.AccountID,
		APIKeyID:         auth.APIKeyID,
		RequestID:        requestID,
		Endpoint:         accountingEndpoint,
		ModelAlias:       route.AliasID,
		EstimatedCredits: 500,
	})
	if err != nil {
		code := "insufficient_quota"
		apierrors.WriteError(w, http.StatusPaymentRequired, "invalid_request_error", "Insufficient credits to complete this request.", &code)
		return
	}

	// Capture the LiteLLM model name for use inside the goroutine.
	litellmModel := route.LiteLLMModelName

	// Rebuild multipart body via io.Pipe for streaming (no disk writes).
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

		// Stream all file parts (audio data) directly — no intermediate storage.
		for fieldName, fileHeaders := range r.MultipartForm.File {
			for _, fh := range fileHeaders {
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
					pw.CloseWithError(fmt.Errorf("copy audio file %s: %w", fieldName, err))
					return
				}
				f.Close()
			}
		}
	}()

	// Forward to LiteLLM.
	upstreamURL := h.litellmBaseURL + litellmPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, pr)
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to build upstream request.", &code)
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+h.masterKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Upstream audio request failed.", &code)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, resp.StatusCode, "api_error", string(upstreamBody), &code)
		return
	}

	// Read response and extract duration for metering (non-fatal if missing).
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		_ = h.accounting.ReleaseReservation(ctx, auth.AccountID, reservationID, "upstream_error")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response.", &code)
		return
	}

	// Finalize reservation on success.
	_ = h.accounting.FinalizeReservation(ctx, FinalizeInput{
		AccountID:     auth.AccountID,
		ReservationID: reservationID,
		ActualCredits: 500,
	})

	// Extract duration for metering (best-effort).
	var durResp struct {
		Duration *float64 `json:"duration"`
	}
	_ = json.Unmarshal(respBody, &durResp)
	// durResp.Duration can be used for metering in the future.

	// Pass through JSON response to client.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBody)
}
