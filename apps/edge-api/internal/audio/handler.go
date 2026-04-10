package audio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
)

// Capability flags used when this handler calls the routing layer.
// These constants document which routing capabilities audio endpoints require.
const (
	// NeedTTS is the routing capability required for /v1/audio/speech.
	NeedTTS = true
	// NeedSTT is the routing capability required for /v1/audio/transcriptions and /v1/audio/translations.
	NeedSTT = true
)

// Handler routes audio requests to speech, transcription, and translation endpoints.
type Handler struct {
	litellmBaseURL string
	masterKey      string
	httpClient     *http.Client
}

// NewHandler creates a new audio Handler.
func NewHandler(litellmBaseURL, masterKey string) *Handler {
	return &Handler{
		litellmBaseURL: strings.TrimRight(litellmBaseURL, "/"),
		masterKey:      masterKey,
		httpClient:     &http.Client{Timeout: 120 * time.Second},
	}
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

	body, err := io.ReadAll(io.LimitReader(r.Body, 1*1024*1024))
	if err != nil {
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read request body.", &code)
		return
	}

	// Dispatch to LiteLLM.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.litellmBaseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to build upstream request.", &code)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.masterKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Upstream audio request failed.", &code)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		code := "upstream_error"
		apierrors.WriteError(w, resp.StatusCode, "api_error", string(upstreamBody), &code)
		return
	}

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
	h.handleMultipartAudio(w, r, "/audio/transcriptions")
}

// handleTranslation processes POST /v1/audio/translations.
// Audio files are forwarded in-flight via multipart — never written to disk or storage.
func (h *Handler) handleTranslation(w http.ResponseWriter, r *http.Request) {
	h.handleMultipartAudio(w, r, "/audio/translations")
}

// handleMultipartAudio is shared logic for transcription and translation:
// rebuild multipart from the incoming request and forward to LiteLLM at the given path.
func (h *Handler) handleMultipartAudio(w http.ResponseWriter, r *http.Request, litellmPath string) {
	ctx := r.Context()

	// Parse multipart form (25MB for audio files).
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to parse multipart form.", &code)
		return
	}

	// Rebuild multipart body via io.Pipe for streaming (no disk writes).
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
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to build upstream request.", &code)
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+h.masterKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Upstream audio request failed.", &code)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		code := "upstream_error"
		apierrors.WriteError(w, resp.StatusCode, "api_error", string(upstreamBody), &code)
		return
	}

	// Read response and extract duration for metering (non-fatal if missing).
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response.", &code)
		return
	}

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
