package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/httpx"
)

// chatCompletionsPath is the internal endpoint this surface delegates to.
const chatCompletionsPath = "/v1/chat/completions"

// readMessagesBody reads r.Body up to apierr.MaxRequestBodyBytes through
// httpx.ReadBody, which caps the read with http.MaxBytesReader (erroring
// instead of silently truncating an oversized body), refuses a
// declared-oversize body before reading it at all, and bounds how long the
// read may take. Before this, io.LimitReader truncated silently; the
// truncated bytes then failed json.Unmarshal and the caller saw a lying
// "invalid JSON body" with no mention of size anywhere (issue #1250). A
// too-large body now gets an honest 413 in Anthropic's own
// request_too_large error type, naming the limit; any other read failure
// keeps the prior generic message.
//
// Only ever called on the real client-facing read (handleMessages,
// handleCountTokens), never on the translated sub-request this surface
// delegates downstream, so it always enforces the cap -- no
// apierr.IsTrustedBody check needed here.
//
// handleMessages reads BEFORE the credential is validated for API-key
// traffic: its guard covers a session principal only, and an API-key
// principal is authorized later, inside the delegated chat chain. Per
// declared byte this is the most expensive pre-auth read in edge-api,
// because the body is read, unmarshalled, translated, re-marshalled, and
// then read and unmarshalled again downstream. The ordering is the defect
// and is not fixed here; see issue #1299.
func readMessagesBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := httpx.ReadBody(w, r, apierr.MaxRequestBodyBytes)
	if err != nil {
		if httpx.TooLarge(err) {
			writeAnthropicError(w, http.StatusRequestEntityTooLarge, apierr.RequestTooLargeMessage(), "")
			return nil, false
		}
		writeAnthropicError(w, http.StatusBadRequest, "body read error", "")
		return nil, false
	}
	return raw, true
}

// Deps holds the runtime dependencies for the Anthropic handler.
type Deps struct {
	// OpenAIChat is the wired POST /v1/chat/completions handler chain: the chat
	// dispatcher for a session principal, the inference orchestrator for an
	// API-key principal. Every control this surface needs already lives behind
	// it -- alias resolution through routing.SelectRoute (which enforces
	// per-tenant model entitlement, the API-key alias allowlist, capability
	// matching and provider selection), credit reservation and settlement,
	// bounded upstream retry, tracing and audit.
	//
	// /v1/messages therefore translates and delegates, and must never build its
	// own upstream dispatch. It used to: because a LiteLLM model name IS a route
	// id, that direct POST let a caller name a route instead of an alias and
	// skip entitlement and metering in one move.
	OpenAIChat http.Handler

	// AuthorizeAPIKey resolves a "Bearer hk_..." Authorization header to a Hive
	// API-key principal, returning the already-sanitized OpenAI-shaped refusal
	// (and any headers that must ride with it) when it cannot.
	//
	// It exists for POST /v1/messages/count_tokens alone. Every other route on
	// this surface delegates to OpenAIChat, which is itself the authority for
	// an API-key principal; count_tokens never dispatches anywhere, so without
	// this it could only see a session-cookie principal and 401'd every
	// programmatic caller -- which is to say, essentially every real Anthropic
	// SDK integration, since an API key is how they all authenticate (issue
	// #1261).
	//
	// Nil leaves count_tokens session-only and fail-closed, the pre-existing
	// behaviour.
	AuthorizeAPIKey func(ctx context.Context, authHeader string) (*apierr.OpenAIError, map[string]string)
}

// Handler accepts Anthropic Messages requests, translates them to the internal
// OpenAI-shaped dispatch, and maps responses back to Anthropic wire format.
type Handler struct {
	deps Deps
}

// NewHandler constructs a Handler with the given dependencies.
func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// ServeHTTP handles both POST /v1/messages and POST /v1/messages/count_tokens.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "Method not allowed", "")
		return
	}

	if strings.HasSuffix(r.URL.Path, "/count_tokens") {
		h.handleCountTokens(w, r)
		return
	}

	h.handleMessages(w, r)
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	// Fail fast for a session principal that cannot invoke chat at all, so such
	// a request never costs a routing round trip. This is a guard clause, not the
	// enforcement: the delegated chain re-checks it, and is the only authority
	// for an API-key principal, which carries no session user.
	if user, ok := auth.UserFrom(r.Context()); ok && user != nil {
		if user.TenantID == uuid.Nil {
			writeAnthropicError(w, http.StatusForbidden, "no tenant for user", "")
			return
		}
		if !authz.RoleHas(authz.Role(user.Role), authz.PermChatInvoke) {
			writeAnthropicError(w, http.StatusForbidden, "chat not allowed", "")
			return
		}
	}
	if h.deps.OpenAIChat == nil {
		// Fail closed. Without the delegated chain there is no route resolution
		// and no metering, and this surface must never dispatch without both.
		writeAnthropicError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	raw, ok := readMessagesBody(w, r)
	if !ok {
		return
	}

	var req MessagesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid JSON body", "")
		return
	}

	if req.Model == "" {
		writeAnthropicError(w, http.StatusBadRequest, "model is required", "")
		return
	}
	if len(req.Messages) == 0 {
		writeAnthropicError(w, http.StatusBadRequest, "messages is required and must be non-empty", "")
		return
	}
	if req.MaxTokens <= 0 {
		writeAnthropicError(w, http.StatusBadRequest, "max_tokens is required and must be greater than 0", "")
		return
	}
	if err := validateCacheControl(req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, err.Error(), "")
		return
	}

	// Capture the client alias before translation so we can echo it back
	// in the response. We must never return an upstream route identifier.
	clientAlias := req.Model

	oaiReq, err := ToOAIRequest(req)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "request translation failed", "")
		return
	}

	// Dispatch downstream as a stream whatever the caller asked for. The chat
	// dispatcher streams unconditionally, and streaming is the only mode that
	// reports usage per chunk, which is what settles a credit reservation at
	// real token counts instead of the flat pre-dispatch estimate. A caller that
	// did not ask to stream is served by folding the stream back into a single
	// message (see translatingWriter).
	oaiReq.Stream = true
	oaiReq.StreamOptions = &OAIStreamOptions{IncludeUsage: true}

	body, err := json.Marshal(oaiReq)
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "internal error", "")
		return
	}

	// Hand the lowered request to the OpenAI chat-completions chain. The alias
	// travels unresolved on purpose: resolving it is the routing layer's job, and
	// this surface must never name a route itself.
	// Translation can grow the body past what the client sent (Stream and
	// StreamOptions are always added above; a string system prompt becomes a
	// message object; text content becomes array form for vision or cache
	// breakpoints), so a client body the inbound readMessagesBody check just
	// cleared can land here over apierr.MaxRequestBodyBytes. Marking this
	// server-constructed, already-in-memory body as trusted stops the
	// delegated chain's own body-size cap from re-rejecting it with an
	// honest-looking but wrong "exceeds the limit" error for a client body
	// that never exceeded anything (#1273 review finding 2).
	sub := r.Clone(apierr.WithTrustedBody(r.Context()))
	sub.URL.Path = chatCompletionsPath
	sub.URL.RawPath = ""
	sub.RequestURI = chatCompletionsPath
	sub.Body = io.NopCloser(bytes.NewReader(body))
	sub.ContentLength = int64(len(body))
	sub.Header.Set("Content-Type", "application/json")
	sub.Header.Del("Content-Length")

	translator := newTranslatingWriter(w, clientAlias, req.Stream)
	h.deps.OpenAIChat.ServeHTTP(translator, sub)
	if err := translator.finish(); err != nil {
		slog.Warn("anthropic response translation error", "err", err, "model", clientAlias)
	}
}

// handleCountTokens returns a local token count estimate for the request body.
func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	if !h.authorizeCountTokens(w, r) {
		return
	}

	raw, ok := readMessagesBody(w, r)
	if !ok {
		return
	}

	var req MessagesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid JSON body", "")
		return
	}

	var totalChars int
	if req.System.Text != "" {
		totalChars += utf8.RuneCountInString(req.System.Text)
	}
	for _, m := range req.Messages {
		if m.Content.Text != "" {
			totalChars += utf8.RuneCountInString(m.Content.Text)
		}
		for _, bl := range m.Content.Blocks {
			totalChars += utf8.RuneCountInString(bl.Text)
		}
	}
	estimated := totalChars/4 + 1

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encErr := json.NewEncoder(w).Encode(CountTokensResponse{InputTokens: estimated}); encErr != nil {
		slog.Warn("anthropic count_tokens encode error", "err", encErr)
	}
}

// authorizeCountTokens accepts either principal type this surface serves: a
// JWT session user (checked for tenant and chat permission, as before) or a
// Hive API key resolved through Deps.AuthorizeAPIKey. It writes the refusal
// itself and reports whether the request may proceed.
//
// The two are checked in that order because the JWT middleware is what
// populates auth.UserFrom; an "hk_" request is routed past it by auth.Selector
// and therefore carries no session user at all, which is exactly why the
// session-only guard this replaces rejected every API-key caller.
func (h *Handler) authorizeCountTokens(w http.ResponseWriter, r *http.Request) bool {
	if user, ok := auth.UserFrom(r.Context()); ok && user != nil {
		if user.TenantID == uuid.Nil {
			writeAnthropicError(w, http.StatusForbidden, "no tenant for user", "")
			return false
		}
		if !authz.RoleHas(authz.Role(user.Role), authz.PermChatInvoke) {
			writeAnthropicError(w, http.StatusForbidden, "chat not allowed", "")
			return false
		}
		return true
	}

	if h.deps.AuthorizeAPIKey == nil {
		writeAnthropicError(w, http.StatusUnauthorized, "missing user", "")
		return false
	}
	authErr, headers := h.deps.AuthorizeAPIKey(r.Context(), r.Header.Get("Authorization"))
	if authErr == nil {
		return true
	}
	// Round-trip through the shared OpenAI writer so the status mapping
	// (401 vs 403 vs 429 vs 503) stays the single implementation the rest of
	// edge-api uses, then reshape the envelope for an Anthropic client. This
	// never re-sanitizes: the authorizer's refusals are already customer-safe.
	// reshapeInto, not a bare reshape, so the retry metadata WriteAuthFailure
	// sets on a 429 or a 503 reaches the client instead of dying in the recorder.
	rec := &headerlessRecorder{}
	apierr.WriteAuthFailure(rec, authErr, headers)
	rec.reshapeInto(w)
	return false
}

// normalizeAPIKeyHeader rewrites an Anthropic x-api-key header to a standard
// Authorization: Bearer header so downstream auth middleware works uniformly.
func normalizeAPIKeyHeader(r *http.Request) *http.Request {
	if r.Header.Get("Authorization") != "" {
		return r
	}
	// ponytail: http.Header.Get canonicalises the key; one lookup is sufficient.
	key := strings.TrimSpace(r.Header.Get("x-api-key"))
	if key == "" {
		return r
	}
	r2 := r.Clone(r.Context())
	r2.Header.Set("Authorization", "Bearer "+key)
	return r2
}

// APIKeyNormalizer wraps an http.Handler, normalising Anthropic x-api-key
// credentials to Authorization: Bearer before dispatching.
func APIKeyNormalizer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, normalizeAPIKeyHeader(r))
	})
}
