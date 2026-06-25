package anthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

const maxBodyBytes = 4 << 20 // 4 MiB

// Deps holds the runtime dependencies for the Anthropic handler.
type Deps struct {
	LiteLLMURL string
	LiteLLMKey string
	HTTP       *http.Client
}

// Handler accepts Anthropic Messages requests, translates them to the internal
// OpenAI-shaped dispatch, and maps responses back to Anthropic wire format.
type Handler struct {
	deps Deps
}

// NewHandler constructs a Handler with the given dependencies.
func NewHandler(deps Deps) *Handler {
	if deps.HTTP == nil {
		deps.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Handler{deps: deps}
}

// ServeHTTP handles both POST /v1/messages and POST /v1/messages/count_tokens.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apierr.WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed", nil)
		return
	}

	// Route count_tokens to the local estimator.
	if strings.HasSuffix(r.URL.Path, "/count_tokens") {
		h.handleCountTokens(w, r)
		return
	}

	h.handleMessages(w, r)
}

func (h *Handler) handleMessages(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthenticated, "missing user")
		return
	}
	if user.TenantID == uuid.Nil {
		apierr.Write(w, http.StatusForbidden, apierr.CodeNoTenant, "no tenant for user")
		return
	}
	if !authz.RoleHas(authz.Role(user.Role), authz.PermChatInvoke) {
		apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "chat not allowed")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		apierr.WriteError(w, http.StatusBadRequest, "invalid_request_error", "body read error", nil)
		return
	}

	var req MessagesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		apierr.WriteError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body", nil)
		return
	}

	if req.Model == "" {
		apierr.WriteError(w, http.StatusBadRequest, "invalid_request_error", "model is required", nil)
		return
	}
	if len(req.Messages) == 0 {
		apierr.WriteError(w, http.StatusBadRequest, "invalid_request_error", "messages is required and must be non-empty", nil)
		return
	}

	oaiReq, err := ToOAIRequest(req)
	if err != nil {
		apierr.WriteError(w, http.StatusBadRequest, "invalid_request_error", "request translation failed", nil)
		return
	}

	body, err := json.Marshal(oaiReq)
	if err != nil {
		apierr.WriteError(w, http.StatusInternalServerError, "api_error", "internal error", nil)
		return
	}

	requestID := uuid.New()
	upstream, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodPost,
		strings.TrimRight(h.deps.LiteLLMURL, "/")+"/v1/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		apierr.WriteError(w, http.StatusInternalServerError, "api_error", "internal error", nil)
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Request-Id", requestID.String())
	if h.deps.LiteLLMKey != "" {
		upstream.Header.Set("Authorization", "Bearer "+h.deps.LiteLLMKey)
	}

	resp, err := h.deps.HTTP.Do(upstream)
	if err != nil {
		apierr.WriteError(w, http.StatusServiceUnavailable, "api_error", "upstream unavailable", nil)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		apierr.WriteProviderBlindUpstreamError(w, req.Model, resp.StatusCode, string(rawBody))
		return
	}

	if oaiReq.Stream {
		t := NewSSETranslator(w)
		if err := t.Translate(resp.Body); err != nil {
			slog.Warn("anthropic SSE translate error", "err", err, "request_id", requestID)
		}
		return
	}

	// Non-streaming: read the full OpenAI response and lift to Anthropic shape.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		apierr.WriteError(w, http.StatusInternalServerError, "api_error", "response read error", nil)
		return
	}

	var oaiResp OAIResponse
	if err := json.Unmarshal(respBody, &oaiResp); err != nil {
		apierr.WriteError(w, http.StatusInternalServerError, "api_error", "response parse error", nil)
		return
	}

	anthResp := FromOAIResponse(oaiResp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if encErr := json.NewEncoder(w).Encode(anthResp); encErr != nil {
		slog.Warn("anthropic response encode error", "err", encErr, "request_id", requestID)
	}
}

// handleCountTokens returns a local token count estimate for the request body.
// This satisfies the Anthropic SDK probe without a real LLM call.
func (h *Handler) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		apierr.WriteError(w, http.StatusBadRequest, "invalid_request_error", "body read error", nil)
		return
	}

	var req MessagesRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		apierr.WriteError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body", nil)
		return
	}

	// Rough estimate: 1 token per 4 UTF-8 chars across all text content.
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
	json.NewEncoder(w).Encode(CountTokensResponse{InputTokens: estimated})
}

// normalizeAPIKeyHeader rewrites an Anthropic x-api-key header to a standard
// Authorization: Bearer header so downstream auth middleware works uniformly.
// Called by the route wrapper before the Handler runs.
func normalizeAPIKeyHeader(r *http.Request) *http.Request {
	if r.Header.Get("Authorization") != "" {
		return r
	}
	key := strings.TrimSpace(r.Header.Get("x-api-key"))
	if key == "" {
		key = strings.TrimSpace(r.Header.Get("X-Api-Key"))
	}
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
