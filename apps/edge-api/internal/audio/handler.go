package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/stt"
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
	AccountID string
	APIKeyID  string
	NeedTTS   bool
	NeedSTT   bool
}

// RouteResult contains the selected route details.
type RouteResult struct {
	AliasID          string
	LiteLLMModelName string

	// UnitPriceCredits is the alias's catalog price in credits per MILLION
	// metered units, and PriceUnit names the unit it is quoted in
	// (model_aliases.price_unit: characters for speech, seconds for
	// transcription). Both come from the routing result rather than from a
	// literal in this package (#627).
	//
	// For any non-token unit the price lives in output_price_credits and
	// input_price_credits is constrained to zero at the database level
	// (supabase/migrations/20260801_13_alias_price_unit.sql), so a
	// single-quantity modality has exactly one price and no ambiguity about
	// which column applies.
	UnitPriceCredits int64
	PriceUnit        string
}

// ErrInsufficientCredits reports that the accounting layer refused the
// reservation because the account lacks credits. CreateReservation
// implementations must wrap it for that case only, so a reservation that
// could not be reached at all is never reported to the caller as a
// billing problem.
var ErrInsufficientCredits = errors.New("audio: insufficient credits")

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
	stt            *stt.TieredClient // non-nil when local STT backends are configured
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

// WithSTT attaches a two-tier local STT client to the handler. When set,
// POST /v1/audio/transcriptions routes directly to the local backends
// instead of LiteLLM. Call before serving requests.
func (h *Handler) WithSTT(c *stt.TieredClient) {
	h.stt = c
}

// writeReservationFailure answers a failed credit reservation. Only a real
// credit refusal is a 402; a reservation that timed out or errored is an
// infrastructure fault, logged and reported as retryable so operators are
// not chasing a phantom billing problem.
func writeReservationFailure(w http.ResponseWriter, endpoint, alias string, err error) {
	if errors.Is(err, ErrInsufficientCredits) {
		code := "insufficient_quota"
		apierrors.WriteError(w, http.StatusPaymentRequired, "invalid_request_error", "Insufficient credits to complete this request.", &code)
		return
	}
	log.Printf("audio: create reservation failed for endpoint=%s alias=%s: %v", endpoint, alias, err)
	code := "upstream_error"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error", "Credit reservation is temporarily unavailable. Please retry.", &code)
}

// requirePriceUnit refuses a request whose resolved alias is not priced in the
// unit this endpoint meters, before any hold is taken (#627). A speech request
// against a token-priced alias, or a transcription against a character-priced
// one, cannot be converted into a charge without inventing a rate, and serving
// it anyway would mean serving it free. Both are worse than a refusal, so this
// fails closed.
//
// The message names neither the provider nor any amount: it is the same shape
// every other unavailable-model answer on this handler uses.
func (h *Handler) requirePriceUnit(w http.ResponseWriter, route RouteResult, meteredUnit, endpoint string) bool {
	if canPrice(route, meteredUnit) {
		return true
	}
	log.Printf("audio: refusing endpoint=%s alias=%s: catalog price is %d credits per million %q but this endpoint meters %q",
		endpoint, route.AliasID, route.UnitPriceCredits, route.PriceUnit, meteredUnit)
	code := "model_not_supported"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error",
		"The requested model is not available for this audio endpoint. Please retry with a supported model.", &code)
	return false
}

// refuseUnpriceableResponse releases the hold and refuses a request whose
// upstream answered 2xx but reported nothing to meter (#627, D-034). Charging
// a guess and serving it free are both excluded, so the only honest answer
// left is a retryable failure.
//
// The release runs on its own bounded context via releaseHold, never the
// request context: this site executes after the upstream call has completed,
// which is the moment a client is most likely to have disconnected, and a
// release that inherits a cancelled context strands the hold (#616).
func (h *Handler) refuseUnpriceableResponse(w http.ResponseWriter, accountID, reservationID, endpoint, alias string) {
	log.Printf("audio: endpoint=%s alias=%s upstream reported no audio duration; refusing rather than charging a guess", endpoint, alias)
	h.releaseHold(accountID, reservationID, "unpriceable_response", endpoint)
	code := "upstream_error"
	apierrors.WriteError(w, http.StatusBadGateway, "api_error",
		"The transcription could not be metered and was not charged. Please retry.", &code)
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

	// Resolve the voice before anything else costs money. The caller sends an
	// OpenAI voice name, the upstream roster is entirely different, and
	// forwarding the name verbatim earned a 400 from the upstream that reached
	// the caller as a sanitized 500 (#1318). A name in neither set is the
	// caller mistake it looks like, so it is answered as one: a 400 that names
	// the parameter and the roster, never a 5xx, and never after a reservation
	// has been taken for a request that cannot succeed.
	upstreamVoice, voiceOK := resolveVoice(speechReq.Voice)
	if !voiceOK {
		code := "invalid_value"
		apierrors.WriteErrorWithParam(w, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Unsupported voice. Supported voices are: %s.", supportedVoiceNames()),
			&code, "voice")
		return
	}

	// Select route based on model alias and TTS capability.
	route, err := h.routing.SelectRoute(ctx, RouteInput{
		AliasID:   speechReq.Model,
		TenantID:  auth.TenantID,
		AccountID: auth.AccountID,
		APIKeyID:  auth.APIKeyID,
		NeedTTS:   true,
	})
	if err != nil {
		code := "model_not_found"
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "The requested model is not available for audio speech.", &code)
		return
	}

	if !h.requirePriceUnit(w, route, PriceUnitCharacters, "/v1/audio/speech") {
		return
	}

	// Speech is priced per character of synthesized text, and the character
	// count is known before dispatch, so the hold and the final charge are the
	// same figure and no settlement drift is possible. Runes, not bytes: a
	// character is a character whatever alphabet it is written in.
	characters := int64(utf8.RuneCountInString(speechReq.Input))
	credits := creditsForQuantity(characters, route)

	// Reserve credits before dispatch.
	requestID := uuid.New().String()
	reservationID, err := h.accounting.CreateReservation(ctx, ReservationInput{
		AccountID:        auth.AccountID,
		APIKeyID:         auth.APIKeyID,
		RequestID:        requestID,
		Endpoint:         "/v1/audio/speech",
		ModelAlias:       route.AliasID,
		EstimatedCredits: credits,
	})
	if err != nil {
		writeReservationFailure(w, "/v1/audio/speech", route.AliasID, err)
		return
	}

	// Rewrite the model field to the LiteLLM model name.
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		h.releaseHold(auth.AccountID, reservationID, "request_error", "/v1/audio/speech")
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON body.", &code)
		return
	}
	bodyMap["model"] = route.LiteLLMModelName
	// Rewrite the voice as well, so the upstream receives a name from its own
	// roster whatever the caller sent (#1318). Writing it unconditionally, not
	// only when a translation happened: resolveVoice already normalized case
	// and whitespace, and a body that disagrees with the value the guard above
	// accepted is the kind of drift this rewrite exists to prevent.
	bodyMap["voice"] = upstreamVoice
	rewrittenBody, err := json.Marshal(bodyMap)
	if err != nil {
		h.releaseHold(auth.AccountID, reservationID, "request_error", "/v1/audio/speech")
		code := "internal_error"
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to serialize request.", &code)
		return
	}

	// Dispatch to LiteLLM, retrying up to maxSpeechAttempts times if the
	// upstream TTS backend returns a 200 with silent (all-zero) WAV sample
	// data instead of the requested speech. This is a confirmed live defect
	// in Groq's Orpheus TTS backend -- reproduced identically calling Groq
	// directly and through LiteLLM's route-groq-tts, so it is not fixable by
	// swapping providers or LiteLLM versions -- that occurs on a meaningful
	// fraction of requests (see live_voice_integration_test.go). Retrying
	// almost always recovers a correct clip.
	var (
		lastStatus int
		lastCT     string
		lastBody   []byte
		success    bool
	)
	for attempt := 1; attempt <= maxSpeechAttempts; attempt++ {
		statusCode, ct, respBody, err := h.fetchSpeechOnce(ctx, rewrittenBody)
		if err != nil {
			h.releaseHold(auth.AccountID, reservationID, "upstream_error", "/v1/audio/speech")
			apierrors.WriteProviderBlindUpstreamError(w, speechReq.Model, http.StatusBadGateway, err.Error())
			return
		}
		lastStatus, lastCT, lastBody = statusCode, ct, respBody

		if statusCode < 200 || statusCode >= 300 {
			break
		}
		if data, ok := wavDataChunk(respBody); ok && isDegenerateSilence(data) {
			log.Printf("audio: speech synthesis attempt %d/%d for alias=%s returned silent WAV, retrying", attempt, maxSpeechAttempts, route.AliasID)
			continue
		}
		success = true
		break
	}

	if lastStatus < 200 || lastStatus >= 300 {
		h.releaseHold(auth.AccountID, reservationID, "upstream_error", "/v1/audio/speech")
		apierrors.WriteProviderBlindUpstreamError(w, speechReq.Model, lastStatus, string(lastBody))
		return
	}
	if !success {
		// Every attempt came back silent.
		h.releaseHold(auth.AccountID, reservationID, "upstream_error", "/v1/audio/speech")
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error", "Speech synthesis is temporarily unavailable. Please retry.", &code)
		return
	}

	// Finalize reservation on success; falls back to releasing the hold if
	// finalize itself fails, so it never strands (#616).
	h.settleReservation(ctx, auth.AccountID, reservationID, credits, "/v1/audio/speech")

	// Binary relay: copy Content-Type exactly from upstream.
	if lastCT != "" {
		w.Header().Set("Content-Type", lastCT)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(lastBody)))
	w.WriteHeader(http.StatusOK)
	w.Write(lastBody) //nolint:errcheck
}

// maxSpeechAttempts bounds retries against the silent-WAV defect described
// above handleSpeech's dispatch loop.
const maxSpeechAttempts = 3

// maxSpeechResponseBytes bounds how much of the upstream speech response
// this handler buffers in memory to inspect before relaying to the client.
// TTS clips for realistic request sizes are well under this.
const maxSpeechResponseBytes = 25 << 20

// fetchSpeechOnce makes one dispatch attempt to LiteLLM's /audio/speech and
// returns the upstream status, Content-Type, and buffered body. err is set
// only for request-construction or transport/read failures; a non-2xx
// upstream status is returned as a normal (status, body) pair, not an error.
func (h *Handler) fetchSpeechOnce(ctx context.Context, rewrittenBody []byte) (statusCode int, contentType string, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.litellmBaseURL+"/audio/speech", bytes.NewReader(rewrittenBody))
	if err != nil {
		return 0, "", nil, fmt.Errorf("build upstream speech request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.masterKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxSpeechResponseBytes))
	if err != nil {
		return 0, "", nil, fmt.Errorf("read upstream speech response: %w", err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), respBody, nil
}

// handleTranscription processes POST /v1/audio/transcriptions.
// When local STT backends are configured (h.stt != nil), requests are dispatched
// directly to the two-tier Parakeet/faster-whisper client — audio never leaves the
// box (sovereign edge deployments, PARAKEET_BASE_URL/FASTER_WHISPER_BASE_URL set).
// When no local backends are configured (serverless/cloud deployments), requests
// fall back to the same generic LiteLLM route selection handleTranslation already
// uses, so a serverless STT provider (e.g. Groq Whisper) can be wired purely
// through routing/catalog data without any further edge-api changes.
func (h *Handler) handleTranscription(w http.ResponseWriter, r *http.Request) {
	if h.stt == nil {
		h.handleMultipartAudio(w, r, "/audio/transcriptions", "/v1/audio/transcriptions")
		return
	}
	const endpoint = "/v1/audio/transcriptions"
	ctx := r.Context()

	auth, ok := h.authorize(w, r)
	if !ok {
		return
	}

	// The form is parsed here, before stt.TieredClient's own (idempotent) call,
	// so the requested model can be read for pricing and the requested
	// response_format can be rewritten to one that reports a duration.
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to parse multipart form.", &code)
		return
	}

	// Dispatch stays local -- the audio never leaves the box -- but the price
	// and the tenant entitlement still come from the catalog, exactly as they
	// do on the serverless path. This path used to consult routing for
	// neither, which is what let it charge a flat literal (#627) and, as a
	// side effect, skip the tenant-scoped entitlement check every other audio
	// endpoint performs (#623).
	modelAlias := r.FormValue("model")
	route, err := h.routing.SelectRoute(ctx, RouteInput{
		AliasID:   modelAlias,
		TenantID:  auth.TenantID,
		AccountID: auth.AccountID,
		APIKeyID:  auth.APIKeyID,
		NeedSTT:   true,
	})
	if err != nil {
		code := "model_not_found"
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "The requested model is not available for audio transcription.", &code)
		return
	}
	if !h.requirePriceUnit(w, route, PriceUnitSeconds, endpoint) {
		return
	}

	requestedFormat := requestedResponseFormat(r.FormValue("response_format"))
	// stt.TieredClient rebuilds its outgoing body from r.MultipartForm.Value,
	// so rewriting the field here is what reaches the sidecar.
	r.MultipartForm.Value["response_format"] = []string{upstreamResponseFormat(requestedFormat)}

	requestID := uuid.New().String()
	reservationID, err := h.accounting.CreateReservation(ctx, ReservationInput{
		AccountID:        auth.AccountID,
		APIKeyID:         auth.APIKeyID,
		RequestID:        requestID,
		Endpoint:         endpoint,
		ModelAlias:       route.AliasID,
		EstimatedCredits: creditsForQuantity(sttEstimatedSeconds, route),
	})
	if err != nil {
		writeReservationFailure(w, endpoint, route.AliasID, err)
		return
	}

	// Buffered, not streamed through: the response has to be read for its
	// duration before the request can be priced, and a request that turns out
	// to be unpriceable must still be refusable at that point.
	rec := &bufferedResponse{}
	h.stt.Transcribe(rec, r)

	if rec.status < 200 || rec.status >= 300 {
		h.releaseHold(auth.AccountID, reservationID, "upstream_error", endpoint)
		rec.flushTo(w)
		return
	}

	seconds, ok := billableSeconds(rec.body.Bytes(), requestedFormat)
	if !ok {
		h.refuseUnpriceableResponse(w, auth.AccountID, reservationID, endpoint, route.AliasID)
		return
	}

	// Finalize reservation on success; falls back to releasing the hold if
	// finalize itself fails, so it never strands (#616).
	h.settleReservation(ctx, auth.AccountID, reservationID, creditsForQuantity(billedSeconds(seconds), route), endpoint)

	contentType, out := reshapeTranscription(rec.body.Bytes(), requestedFormat)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// bufferedResponse captures everything an http.Handler writes so the caller can
// price the response before committing it to the client, and relay it verbatim
// when it is a failure. It replaces a passthrough recorder that captured the
// status only, which was enough to decide finalize-or-release but not enough to
// read the metered quantity out of the body.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header {
	if b.header == nil {
		b.header = http.Header{}
	}
	return b.header
}

func (b *bufferedResponse) WriteHeader(code int) {
	if b.status == 0 {
		b.status = code
	}
}

func (b *bufferedResponse) Write(p []byte) (int, error) {
	if b.status == 0 {
		b.status = http.StatusOK
	}
	return b.body.Write(p)
}

// flushTo copies the buffered headers, status, and body onto the real writer.
func (b *bufferedResponse) flushTo(w http.ResponseWriter) {
	for key, values := range b.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := b.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	w.Write(b.body.Bytes()) //nolint:errcheck
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
		AliasID:   modelAlias,
		TenantID:  auth.TenantID,
		AccountID: auth.AccountID,
		APIKeyID:  auth.APIKeyID,
		NeedSTT:   true,
	})
	if err != nil {
		code := "model_not_found"
		apierrors.WriteError(w, http.StatusNotFound, "invalid_request_error", "The requested model is not available for audio transcription.", &code)
		return
	}

	if !h.requirePriceUnit(w, route, PriceUnitSeconds, accountingEndpoint) {
		return
	}

	// Reserve credits before dispatch. The real charge is the transcribed
	// duration, which only the upstream can report, so this hold is an
	// estimate that settlement replaces with the metered figure.
	requestID := uuid.New().String()
	reservationID, err := h.accounting.CreateReservation(ctx, ReservationInput{
		AccountID:        auth.AccountID,
		APIKeyID:         auth.APIKeyID,
		RequestID:        requestID,
		Endpoint:         accountingEndpoint,
		ModelAlias:       route.AliasID,
		EstimatedCredits: creditsForQuantity(sttEstimatedSeconds, route),
	})
	if err != nil {
		writeReservationFailure(w, accountingEndpoint, route.AliasID, err)
		return
	}

	litellmModel := route.LiteLLMModelName
	requestedFormat := requestedResponseFormat(r.FormValue("response_format"))

	// Rebuild multipart body into an in-memory buffer — bounded by the 25MB
	// ParseMultipartForm cap above, so buffering the whole thing is safe.
	// This used to stream through io.Pipe instead, which forces chunked
	// transfer-encoding on the outgoing request (no Content-Length is known
	// up front). At least one real provider (Groq) accepts that request
	// with a 200 but silently truncates the multipart file part instead of
	// erroring, corrupting the audio the provider actually transcribes.
	// Buffering first gives the outgoing request a real Content-Length and
	// avoids that corruption.
	var multipartBuf bytes.Buffer
	mw := multipart.NewWriter(&multipartBuf)

	// Copy all text form fields, rewriting the model field. response_format is
	// dropped here and re-added below: the caller's choice governs what they
	// receive, not what this handler asks the upstream for.
	for key, values := range r.MultipartForm.Value {
		if key == "response_format" {
			continue
		}
		for _, val := range values {
			writeVal := val
			if key == "model" {
				writeVal = litellmModel
			}
			if err := mw.WriteField(key, writeVal); err != nil {
				h.releaseHold(auth.AccountID, reservationID, "request_error", accountingEndpoint)
				code := "invalid_request"
				apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to rebuild request field.", &code)
				return
			}
		}
	}
	if err := mw.WriteField("response_format", upstreamResponseFormat(requestedFormat)); err != nil {
		h.releaseHold(auth.AccountID, reservationID, "request_error", accountingEndpoint)
		code := "invalid_request"
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to rebuild request field.", &code)
		return
	}

	// Copy all file parts (audio data).
	for fieldName, fileHeaders := range r.MultipartForm.File {
		for _, fh := range fileHeaders {
			if err := copyMultipartFile(mw, fieldName, fh); err != nil {
				h.releaseHold(auth.AccountID, reservationID, "request_error", accountingEndpoint)
				code := "invalid_request"
				apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Failed to read uploaded audio file.", &code)
				return
			}
		}
	}

	if err := mw.Close(); err != nil {
		h.releaseHold(auth.AccountID, reservationID, "request_error", accountingEndpoint)
		code := "internal_error"
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error", "Failed to finalize request body.", &code)
		return
	}

	// Forward to LiteLLM.
	upstreamURL := h.litellmBaseURL + litellmPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, &multipartBuf)
	if err != nil {
		h.releaseHold(auth.AccountID, reservationID, "upstream_error", accountingEndpoint)
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to build upstream request.", &code)
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+h.masterKey)

	resp, err := h.httpClient.Do(req)
	if err != nil {
		h.releaseHold(auth.AccountID, reservationID, "upstream_error", accountingEndpoint)
		apierrors.WriteProviderBlindUpstreamError(w, modelAlias, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		h.releaseHold(auth.AccountID, reservationID, "upstream_error", accountingEndpoint)
		apierrors.WriteProviderBlindUpstreamError(w, modelAlias, resp.StatusCode, string(upstreamBody))
		return
	}

	// Read response and extract duration for metering (non-fatal if missing).
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		h.releaseHold(auth.AccountID, reservationID, "upstream_error", accountingEndpoint)
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response.", &code)
		return
	}

	// Meter the transcribed duration before settling. A 2xx that reports no
	// duration cannot be priced, so it is refused and the hold released rather
	// than charged at a guess or served free (#627, D-034).
	seconds, ok := billableSeconds(respBody, requestedFormat)
	if !ok {
		h.refuseUnpriceableResponse(w, auth.AccountID, reservationID, accountingEndpoint, route.AliasID)
		return
	}

	// Finalize reservation on success; falls back to releasing the hold if
	// finalize itself fails, so it never strands (#616).
	h.settleReservation(ctx, auth.AccountID, reservationID, creditsForQuantity(billedSeconds(seconds), route), accountingEndpoint)

	// Reduce the upstream body to the shape the caller asked for.
	contentType, out := reshapeTranscription(respBody, requestedFormat)
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	w.Write(out) //nolint:errcheck
}

// copyMultipartFile opens one uploaded file part and copies its bytes into a
// new form file field on mw, under the same field and file name.
func copyMultipartFile(mw *multipart.Writer, fieldName string, fh *multipart.FileHeader) error {
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer f.Close()

	fw, err := mw.CreateFormFile(fieldName, fh.Filename)
	if err != nil {
		return err
	}
	_, err = io.Copy(fw, f)
	return err
}
