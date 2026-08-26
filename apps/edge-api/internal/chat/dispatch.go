package chat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
)

type Deps struct {
	Pool *pgxpool.Pool
	// Routing resolves a Hive catalog alias (e.g. "hive-fast") to the
	// underlying LiteLLM route. Required: LiteLLM only knows route names
	// (e.g. "route-groq-fast"), never Hive aliases, so forwarding
	// parsed.Model unresolved 400s upstream on every real request (#269).
	Routing *inference.RoutingClient
	// Accounting and Billing are the money path for session chat (#746).
	// Without both, this handler refuses every request rather than serving
	// inference it cannot charge for: see startSettlement in billing.go.
	Accounting *inference.AccountingClient
	Billing    BillingResolver
	// Memories supplies the cross-chat recall block (issue #172, ruling
	// D-020). Optional: nil disables injection entirely, and a recall read
	// failure degrades to serving without the block, never fails the chat.
	Memories   MemorySource
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

type sseEnvelope struct {
	// Model is the upstream's own name for what served the request. It is
	// read for the cross-alias fallback check (#743) and logged, never
	// forwarded to audit_log, which fans out to third-party sinks.
	Model string `json:"model,omitempty"`
	// Usage reuses inference.UsageResponse (the same OpenAI-compatible shape
	// the API-key path decodes) rather than a second, narrower local type, so
	// this surface prices cache-read and cache-write tokens through the same
	// inference.NormalizeCacheUsage this package already imports for
	// everything else on this path, instead of growing a copy that could
	// drift out of sync with it (#688 cache-pricing follow-up: this local
	// type used to declare only prompt_tokens/completion_tokens/total_tokens,
	// which silently dropped every cache field at decode time, before any
	// downstream code had a chance to price them).
	Usage   *inference.UsageResponse `json:"usage,omitempty"`
	Choices []struct {
		FinishReason string `json:"finish_reason,omitempty"`
		Delta        struct {
			Content string `json:"content,omitempty"`
			// A refusal is delivered output like any other: the customer
			// received it, and it cost provider tokens to produce. The
			// API-key accumulator has always counted it
			// (inference.UsageAccumulator.Add), and leaving it out here made
			// a refusal-only answer with no terminal usage frame look like
			// nothing was produced, which released the hold and served it
			// free.
			Refusal string `json:"refusal,omitempty"`
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
	// Resolve the client-facing alias (e.g. "hive-fast") to the concrete
	// LiteLLM route (e.g. "route-groq-fast"). LiteLLM's model_list only
	// contains route names; sending the alias straight through 400s
	// upstream with "Invalid model name passed in model=<alias>" (#269).
	route, err := h.deps.Routing.SelectRoute(r.Context(), inference.SelectRouteInput{
		AliasID:             parsed.Model,
		NeedChatCompletions: true,
		NeedStreaming:       true,
	})
	if err != nil {
		slog.Warn("dispatch route selection failed", "err", err, "alias", parsed.Model)
		switch {
		case errors.Is(err, inference.ErrRouteNotFound):
			apierr.Write(w, http.StatusNotFound, apierr.CodeInvalidRequest, "model not found")
		case errors.Is(err, inference.ErrModelNotEntitled):
			// The tenant is not entitled to this model. This is an
			// administrative policy verdict, so it must not surface as the
			// transient 503 below. The message names only the model the caller
			// already asked for: it never enumerates what other tenants can see.
			apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden,
				"model not available for this workspace")
		default:
			// Transport failure or unexpected control-plane status --
			// not a verdict on the alias itself. Reporting 404 here
			// would misrepresent a transient routing outage as a
			// missing model (#289 review).
			apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeServiceUnavailable, "routing unavailable")
		}
		return
	}
	clientModel := parsed.Model
	requestID := uuid.New()

	// Cross-chat recall (issue #172, ruling D-020): prepend the user's most
	// recent memories as one system block before the body is rewritten and
	// forwarded. Absent memories leave the body untouched; a recall failure
	// logs a warning and serves without the block, chat never breaks on it.
	if h.deps.Memories != nil {
		contents, memErr := h.deps.Memories.Recent(r.Context(), user.TenantID, user.ID, memoryRecallLimit)
		if memErr != nil {
			slog.Warn("chat memory recall failed; serving without recall block", "err", memErr)
		} else if block := buildMemoryBlock(contents); block != "" {
			injected, injectErr := injectMemoryBlock(raw, block)
			if injectErr != nil {
				slog.Warn("chat memory injection failed; serving without recall block", "err", injectErr)
			} else {
				raw = injected
			}
		}
	}

	// Money path (#746): a session turn is served only once it can be
	// charged. Every refusal inside startSettlement is written before a
	// provider is reached, and the hold it takes reaches a terminal state
	// exactly once -- finalized on the success path below, released by this
	// deferred call on every other exit, never both and never neither.
	settle, refused := h.startSettlement(r.Context(), w, user.TenantID, route, clientModel, requestID)
	if refused {
		return
	}
	settled := false
	releaseReason := "interrupted"
	defer func() {
		if settle != nil && !settled {
			settle.release(releaseReason)
		}
	}()
	// Rewrite only the two fields this path owns (the resolved route name, and
	// streaming, which it always uses) and keep every other field the caller
	// sent. Re-marshalling the narrow chatRequest struct instead silently dropped
	// everything outside it: max_tokens, temperature, tools, stream_options. The
	// Anthropic Messages surface delegates here and depends on those surviving.
	body, err := rewriteDispatchBody(raw, route.LiteLLMModelName)
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
		releaseReason = "upstream_error"
		apierr.Write(w, http.StatusServiceUnavailable, apierr.CodeServiceUnavailable, "upstream unavailable")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		rawBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		releaseReason = "upstream_error"
		apierr.WriteProviderBlindUpstreamError(w, clientModel, resp.StatusCode, string(rawBody))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	var inTokens, outTokens int64
	var hasUsage bool
	var cacheUsage inference.CacheUsage
	var servedModel string
	// Verbatim bytes of the terminal usage frame, populated only for a
	// variable-price alias. See the capture site below.
	var rawUsagePayload []byte
	var finishReason string
	var completion strings.Builder

	// mintedID feeds SanitizeVariablePriceFrame below: on a variable-price
	// alias every frame is sanitized independently with no memory of frames
	// before it, so the id replacement must be minted once per stream here
	// and reused on every frame, matching the id-stability contract
	// inference.SanitizeVariablePriceFrame documents.
	mintedID := "chatcmpl-" + uuid.New().String()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			_, _ = w.Write([]byte("\n"))
			flush(flusher)
			continue
		}
		// This relay is verbatim by design, which is fine while the upstream
		// frame carries nothing we must not publish. Enabling usage accounting
		// for a variable-price alias changes that: the frame then carries our
		// cost, its breakdown and the model the router actually chose. So that
		// one route is sanitized before the write, and an unparseable frame is
		// dropped rather than forwarded, because an unparseable frame is
		// precisely the one whose contents are unknown.
		isData := bytes.HasPrefix(line, []byte("data: "))
		payload := bytes.TrimPrefix(line, []byte("data: "))
		isDone := isData && bytes.Equal(payload, []byte("[DONE]"))

		if isData && !isDone && route.Pricing.IsUpstreamActual() {
			sanitized, sanOK := inference.SanitizeVariablePriceFrame(payload, clientModel, mintedID)
			if !sanOK {
				slog.Warn("session chat: dropping an unparseable upstream frame on a variable-price alias",
					"request_id", requestID, "alias", clientModel)
				continue
			}
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(sanitized)
			_, _ = w.Write([]byte("\n"))
			flush(flusher)
		} else {
			_, _ = w.Write(line)
			_, _ = w.Write([]byte("\n"))
			flush(flusher)
		}

		if !isData {
			continue
		}
		if isDone {
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
			if choice.Delta.Refusal != "" {
				completion.WriteString(choice.Delta.Refusal)
			}
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
		if envelope.Model != "" {
			servedModel = envelope.Model
		}
		if envelope.Usage != nil {
			hasUsage = true
			inTokens = envelope.Usage.PromptTokens
			outTokens = envelope.Usage.CompletionTokens
			cacheUsage = inference.NormalizeCacheUsage(envelope.Usage, clientModel, route.Provider)
			// For a variable-price alias the charge comes from the cost the
			// upstream reports, and sseEnvelope does not declare that field, so
			// unmarshalling has already dropped it. Keep the untouched payload
			// so settlement can read it; nothing else looks at these bytes and
			// they never reach the client from here.
			if route.Pricing.IsUpstreamActual() {
				rawUsagePayload = append(rawUsagePayload[:0], payload...)
			}
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
			"err", streamErr, "request_id", requestID, "model", clientModel)
		finishReason = "stream_error"
	}

	latency := int(time.Since(started).Milliseconds())

	// The charge is the catalog price of the route that actually served the
	// request applied to the tokens the provider reported, the same conversion
	// the API-key path settles with (#688), so the two surfaces cannot report
	// different costs for identical usage. When no usage frame arrives at all,
	// the same helper falls back to a content estimate and flags it
	// unconfirmed, which is what tells control-plane to clamp the figure to the
	// hold and open a reconciliation job. It never settles a delivered response
	// at zero, and never bills a token count as though it were a credit amount.
	var costCredits int64
	var confirmed, delivered bool
	if route.Pricing.IsUpstreamActual() {
		// This alias has no catalog price. Its charge is the cost the upstream
		// reported for this generation, times the standard margin. A cost that
		// is missing, unreadable or a confident zero settles at the hold rather
		// than at nothing, which is the whole point: this is the streaming path
		// Open WebUI uses, so it is where a silent free-serve would do the most
		// damage.
		settled := inference.UpstreamActualSettlement(
			rawUsagePayload, settle.held(), hasUsage,
			inTokens, outTokens, completion.String())
		costCredits, confirmed, delivered = settled.Credits, settled.Confirmed, settled.Delivered
		if delivered {
			// generation_id is the audit handle for this charge. Operator log
			// only: an upstream identifier can carry a provider name and
			// audit_log fans out to third-party sinks.
			slog.Info("session chat: variable-price settlement",
				"request_id", requestID, "alias", clientModel, "reason", settled.Reason,
				"credits", settled.Credits, "confirmed", settled.Confirmed,
				"generation_id", settled.GenerationID, "held_credits", settle.held())
		}
	} else {
		costCredits, confirmed, delivered = inference.ChatSettlementCredits(
			route, hasUsage, cacheUsage.FreshInputTokens, cacheUsage.CacheReadTokens, cacheUsage.CacheWriteTokens, outTokens, raw, completion.String())
	}
	if servedModel != "" && servedModel != route.LiteLLMModelName {
		// An upstream fallback that crosses an alias boundary serves one model
		// and would be priced at another's rate (#743). The charge below still
		// uses the route this gateway dispatched to, which is the only price it
		// can defend, and the mismatch is recorded so #743 has evidence rather
		// than an assumption. Operator log only: an upstream model name can
		// carry a provider name, and audit_log fans out to third-party sinks.
		slog.Warn("session chat served by a different upstream model than dispatched",
			"request_id", requestID, "alias", clientModel,
			"dispatched", route.LiteLLMModelName, "served", servedModel)
	}
	switch {
	case settle == nil:
		// Enterprise posture (D-027): no prepaid relationship, so nothing is
		// charged. costCredits stays the priced figure for the trace row, which
		// is observability, not a ledger entry, and drops to zero for an alias
		// the catalog cannot price rather than reporting the never-free floor
		// as though a rate existed.
		if !inference.CanPriceTokens(route) {
			costCredits = 0
		}
	case !delivered:
		// Nothing was produced, so there is no quantity to charge. The deferred
		// release hands the hold back in full.
		releaseReason = "upstream_error"
		if r.Context().Err() != nil {
			releaseReason = "client_disconnect"
		}
		costCredits = 0
	case settle.finalize(costCredits, confirmed, inTokens, outTokens, cacheUsage.CacheReadTokens, cacheUsage.CacheWriteTokens):
		settled = true
	default:
		// The charge did not land. Leaving settled false hands the reservation
		// to the deferred release, so it still reaches a terminal state exactly
		// once rather than stranding the hold behind a lost charge (#616).
		releaseReason = "finalize_failed"
		costCredits = 0
	}
	if traceErr := InsertTrace(r.Context(), h.deps.Pool, TraceRow{
		TenantID:       user.TenantID,
		UserID:         user.ID,
		RequestID:      requestID,
		Model:          clientModel,
		Provider:       route.Provider,
		InTokens:       int(inTokens),
		OutTokens:      int(outTokens),
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
			"model":         clientModel,
			"in_tokens":     inTokens,
			"out_tokens":    outTokens,
			"latency_ms":    latency,
			"cost_credits":  costCredits,
			"charged":       settled,
			"finish_reason": finishReason,
		},
	}); auditErr != nil {
		slog.Warn("audit_log write failed", "err", auditErr, "request_id", requestID)
	}
}

// rewriteDispatchBody returns the caller's body with the model replaced by the
// resolved LiteLLM route name, streaming forced on and the terminal usage frame
// requested, leaving all other fields untouched.
func rewriteDispatchBody(raw []byte, litellmModel string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	model, err := json.Marshal(litellmModel)
	if err != nil {
		return nil, err
	}
	fields["model"] = model
	fields["stream"] = json.RawMessage("true")
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	// stream_options.include_usage is what makes the terminal usage frame
	// arrive at all. Without it the usage envelope is always nil, the token
	// counts stay zero, and a settlement would charge for a request it never
	// measured (#746). The single copy of that rewrite lives in
	// internal/metering, so this delegates rather than carrying a second.
	return metering.RewriteBody(out)
}

func flush(flusher http.Flusher) {
	if flusher != nil {
		flusher.Flush()
	}
}
