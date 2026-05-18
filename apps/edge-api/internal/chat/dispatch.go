package chat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct {
	Pool       *pgxpool.Pool
	LiteLLMURL string
	LiteLLMKey string
	DeploySHA  string
	Env        string
	HTTP       *http.Client
}

type Handler struct {
	deps Deps
}

func NewDispatch(deps Deps) *Handler {
	if deps.HTTP == nil {
		deps.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	return &Handler{deps: deps}
}

type chatRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Stream   bool             `json:"stream,omitempty"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type sseEnvelope struct {
	Usage   *usage `json:"usage,omitempty"`
	Choices []struct {
		FinishReason string `json:"finish_reason,omitempty"`
		Delta        struct {
			Content string `json:"content,omitempty"`
		} `json:"delta,omitempty"`
	} `json:"choices,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "body read")
		return
	}
	var parsed chatRequest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "bad json")
		return
	}
	if parsed.Model == "" || len(parsed.Messages) == 0 {
		apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "missing model or messages")
		return
	}
	requestID := uuid.New()
	parsed.Stream = true
	body, err := json.Marshal(parsed)
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "bad request")
		return
	}

	upstream, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodPost,
		strings.TrimRight(h.deps.LiteLLMURL, "/")+"/v1/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "build request")
		return
	}
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("X-Request-Id", requestID.String())
	if h.deps.LiteLLMKey != "" {
		upstream.Header.Set("Authorization", "Bearer "+h.deps.LiteLLMKey)
	}

	started := time.Now()
	resp, err := h.deps.HTTP.Do(upstream)
	if err != nil {
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeServiceUnavailable, "upstream unavailable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		apierr.WriteProviderBlindUpstreamError(w, parsed.Model, resp.StatusCode, string(rawBody))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	var totalTokens, inTokens, outTokens int
	var finishReason string
	var completion strings.Builder

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			_, _ = w.Write([]byte("\n"))
			flush(flusher)
			continue
		}
		_, _ = w.Write(line)
		_, _ = w.Write([]byte("\n"))
		flush(flusher)

		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data: "))
		if bytes.Equal(payload, []byte("[DONE]")) {
			break
		}
		var envelope sseEnvelope
		if err := json.Unmarshal(payload, &envelope); err != nil {
			continue
		}
		for _, choice := range envelope.Choices {
			if choice.Delta.Content != "" {
				completion.WriteString(choice.Delta.Content)
			}
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
		if envelope.Usage != nil {
			inTokens = envelope.Usage.PromptTokens
			outTokens = envelope.Usage.CompletionTokens
			totalTokens = envelope.Usage.TotalTokens
		}
	}

	// If the SSE scanner errored mid-stream (upstream drop, token larger
	// than the 4 MiB buffer, etc.) we have already shipped a partial
	// response to the client. The HTTP status is committed at the
	// StatusOK above, so we cannot rewrite it — but the trace and audit
	// rows must reflect the abort instead of claiming a normal
	// completion. The finish_reason becomes "stream_error" and the warning
	// log preserves the underlying cause for operators.
	streamErr := scanner.Err()
	if streamErr != nil {
		slog.Warn("dispatch SSE stream aborted",
			"err", streamErr, "request_id", requestID, "model", parsed.Model)
		finishReason = "stream_error"
	}

	latency := int(time.Since(started).Milliseconds())
	provider := guessProvider(parsed.Model)
	costCredits := int64(totalTokens)
	if traceErr := InsertTrace(r.Context(), h.deps.Pool, TraceRow{
		TenantID:       user.TenantID,
		UserID:         user.ID,
		RequestID:      requestID,
		Model:          parsed.Model,
		Provider:       provider,
		InTokens:       inTokens,
		OutTokens:      outTokens,
		LatencyMs:      latency,
		CostCredits:    costCredits,
		FinishReason:   finishReason,
		PromptHash:     hashString(string(raw)),
		CompletionHash: hashString(completion.String()),
	}); traceErr != nil {
		slog.Warn("llm_traces write failed", "err", traceErr, "request_id", requestID)
	}
	// Provider name is internal only — never written to audit_log.after_json,
	// which fans out to third-party sinks (Datadog, Sentry, ELK, etc.).
	if auditErr := insertAuditEvent(r.Context(), h.deps.Pool, auditEvent{
		TenantID:    user.TenantID,
		ActorID:     user.ID,
		Action:      "CHAT_REQUEST",
		Severity:    "INFO",
		RequestID:   requestID,
		UserAgent:   r.UserAgent(),
		DeploySHA:   h.deps.DeploySHA,
		Environment: h.deps.Env,
		After: map[string]any{
			"model":         parsed.Model,
			"in_tokens":     inTokens,
			"out_tokens":    outTokens,
			"latency_ms":    latency,
			"cost_credits":  costCredits,
			"finish_reason": finishReason,
		},
	}); auditErr != nil {
		slog.Warn("audit_log write failed", "err", auditErr, "request_id", requestID)
	}
}

func flush(flusher http.Flusher) {
	if flusher != nil {
		flusher.Flush()
	}
}

func guessProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "openrouter/"):
		return "openrouter"
	case strings.HasPrefix(model, "groq/"):
		return "groq"
	case strings.HasPrefix(model, "gpt-"):
		return "openai"
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	default:
		return "unknown"
	}
}
