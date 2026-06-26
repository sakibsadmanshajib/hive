// Package stt provides a two-tier speech-to-text client that dispatches
// transcription requests to self-hosted backends by language:
//   - English ("en") routes to NVIDIA Parakeet (fast, ONNX, CPU-capable).
//   - All other languages (including Bangla "bn" and auto-detect "") route to
//     faster-whisper (multilingual). Parakeet v3 does not support Bangla.
//
// Both backends speak the OpenAI Whisper API (/v1/audio/transcriptions).
// Backend identity is never exposed to callers; all error messages are
// provider-blind.
package stt

import (
	"io"
	"net/http"
	"strings"
	"time"

	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// Config holds the base URLs for the two STT backends.
// An empty URL means that backend is not configured; requests routed to an
// unconfigured backend return a provider-blind 503.
type Config struct {
	// ParakeetBaseURL is the base URL of the Parakeet sidecar
	// (e.g. http://parakeet:5092). English transcription routes here.
	// Set via PARAKEET_BASE_URL.
	ParakeetBaseURL string

	// FasterWhisperBaseURL is the base URL of the faster-whisper sidecar
	// (e.g. http://faster-whisper:9000). Bangla and all other languages
	// route here.
	// Set via FASTER_WHISPER_BASE_URL.
	FasterWhisperBaseURL string
}

// TieredClient dispatches transcription requests to the correct backend
// based on the language field in the multipart form.
type TieredClient struct {
	cfg        Config
	httpClient *http.Client
}

// NewTieredClient creates a TieredClient from the given Config.
func NewTieredClient(cfg Config) *TieredClient {
	return &TieredClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// Transcribe parses the incoming multipart request, selects a backend by
// language, forwards the request verbatim, and writes the response.
// It implements http.Handler semantics: w is fully written before return.
func (c *TieredClient) Transcribe(w http.ResponseWriter, r *http.Request) {
	// Parse multipart (25 MiB cap, consistent with the audio handler).
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to parse multipart form.", &code)
		return
	}

	language := strings.ToLower(strings.TrimSpace(r.FormValue("language")))
	backendURL := c.selectBackend(language)
	if backendURL == "" {
		code := "feature_unavailable"
		apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error", "Speech transcription is not available.", &code)
		return
	}

	c.forwardToBackend(w, r, backendURL)
}

// selectBackend returns the base URL for the correct backend, or "" if not configured.
// ponytail: English-only check; everything else (including auto-detect) goes to
// faster-whisper which handles multilingual. If the English tier is not configured
// and English is requested, we return "" (503) rather than silently falling back,
// preserving explicit operator intent.
func (c *TieredClient) selectBackend(language string) string {
	if language == "en" {
		return strings.TrimRight(c.cfg.ParakeetBaseURL, "/")
	}
	return strings.TrimRight(c.cfg.FasterWhisperBaseURL, "/")
}

// forwardToBackend streams the multipart request to the selected backend and
// pipes the response back to the caller. All upstream error details are
// sanitised through provider-blind error handling before reaching the client.
func (c *TieredClient) forwardToBackend(w http.ResponseWriter, r *http.Request, backendBase string) {
	upstreamURL := backendBase + "/v1/audio/transcriptions"

	// Stream the raw body directly; Content-Type header (multipart boundary) is preserved.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, r.Body)
	if err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to build transcription request.", &code)
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		apierrors.WriteProviderBlindUpstreamError(w, "speech transcription", http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read transcription response.", &code)
		return
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Use a generic status-mapped message rather than forwarding the raw backend
		// body, which may contain filesystem paths or internal service names.
		code := "upstream_error"
		errType := "api_error"
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			errType = "rate_limit_error"
			code = "upstream_rate_limited"
			apierrors.WriteError(w, resp.StatusCode, errType, "Speech transcription is temporarily rate limited.", &code)
		case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			apierrors.WriteError(w, resp.StatusCode, errType, "Speech transcription is temporarily unavailable.", &code)
		default:
			apierrors.WriteError(w, resp.StatusCode, errType, "Speech transcription request failed.", &code)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBody) //nolint:errcheck
}
