package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/httpx"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
	"github.com/sakibsadmanshajib/hive/packages/sanitize"
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
	Memories MemorySource
	// Instructions supplies the user's own standing custom instructions
	// (issue #1363). Optional on exactly the same terms as Memories: nil
	// disables injection, and a read failure serves the turn without the
	// block rather than failing the chat. Someone's preferred tone is not
	// worth refusing their message over.
	Instructions InstructionSource
	LiteLLMURL   string
	LiteLLMKey   string
	DeploySHA    string
	Env          string
	HTTP         *http.Client
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
			// A tool-call-only turn is a complete, billable response that
			// carries no text at all, so the zero-content guard has to be able
			// to see one (issue #1526). Left undeclared, a tool call truncated
			// at the caller's ceiling would present as a reasoning burn --
			// empty, finished on "length" -- and be released free. Raw, because
			// nothing here reads inside them: presence is the whole question.
			ToolCalls    json.RawMessage `json:"tool_calls,omitempty"`
			FunctionCall json.RawMessage `json:"function_call,omitempty"`
		} `json:"delta,omitempty"`
	} `json:"choices,omitempty"`
}

// rawPresent reports whether an undecoded JSON field was actually sent, as
// opposed to absent or an explicit null. Providers spell "no tool call on this
// delta" both ways.
func rawPresent(f json.RawMessage) bool {
	return len(f) > 0 && !bytes.Equal(f, []byte("null"))
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

	// Honest read instead of silent truncation (issue #1250), through the same
	// httpx.ReadBody the other body-reading surfaces use: http.MaxBytesReader
	// errors when the body exceeds the cap rather than quietly cutting it off,
	// which used to make an oversized-but-valid body fail json.Unmarshal below
	// and get reported as "bad json" with no mention of size anywhere. It also
	// refuses a declared-oversize body before reading it, and bounds how long
	// the read may take, so a client that opens a connection and dribbles a
	// body cannot hold this handler open indefinitely (issue #1299).
	//
	// apierr.IsTrustedBody(r.Context()) skips all of that: /v1/messages
	// delegates to this handler for a session principal with a translated
	// body that is already fully in memory and was already validated at its
	// own ingress boundary, so re-capping it can only wrongly reject a
	// client body that never exceeded anything (#1273 review finding 2), and
	// a bytes.Reader has no connection to time out.
	var raw []byte
	if apierr.IsTrustedBody(r.Context()) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "body read")
			return
		}
		raw = body
	} else {
		body, err := httpx.ReadBody(w, r, apierr.MaxRequestBodyBytes)
		if err != nil {
			if httpx.TooLarge(err) {
				apierr.Write(w, http.StatusRequestEntityTooLarge, apierr.CodeRequestTooLarge, apierr.RequestTooLargeMessage())
				return
			}
			apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "body read")
			return
		}
		raw = body
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
	// RequireToolCapable, set here for the first time on the session chat path.
	//
	// It was never set before, and the consequence was the inverse of the fear
	// that kept it unset: a tool payload from the chat surface was dispatched
	// with NO capability check at all, which is how one reached a member that
	// answered 404 "No endpoints found that support tool use" (issue #1561).
	//
	// Setting it is only safe because advertisement is decided upstream of the
	// request. hive_capabilities.tools on GET /v1/models reports true only for
	// an alias whose every enabled route is tool capable
	// (catalog.ToolCapableAliases), and the chat surface attaches a tools block
	// only for such an alias. Filtering an all-capable candidate set is the
	// identity, so this flag removes no candidate and route selection is
	// unchanged for every turn, tool-carrying or not. That equality is held
	// against the real catalog by
	// TestAdvertisingToolsNeverNarrowsTheCandidateSet in the control-plane
	// routing package, not asserted here.
	//
	// The definition of "tool-carrying" is inference.ToolParamInBody, the same
	// field list the API-key surface gates on, so the two surfaces cannot come
	// to different verdicts about the same body.
	//
	// ponytail: this is a second shallow pass over the body, on a path that
	// already decodes and re-encodes it once in rewriteDispatchBody. Cheap next
	// to the upstream call it precedes. If it ever shows up in a profile, the
	// upgrade path is to read the tool fields out of the field map
	// rewriteDispatchBody already builds, rather than to duplicate the field
	// list here.
	toolParam := inference.ToolParamInBody(raw)

	route, err := h.deps.Routing.SelectRoute(r.Context(), inference.SelectRouteInput{
		AliasID:             parsed.Model,
		NeedChatCompletions: true,
		NeedStreaming:       true,
		RequireToolCapable:  toolParam != "",
	})
	if err != nil {
		slog.Warn("dispatch route selection failed", "err", err, "alias", parsed.Model)
		switch {
		case errors.Is(err, inference.ErrRouteNotFound):
			apierr.Write(w, http.StatusNotFound, apierr.CodeInvalidRequest, "model not found")
		case errors.Is(err, inference.ErrNoToolCapableRoute):
			// A tool block reached an alias that cannot serve one. Reported as
			// what it is rather than folded into the transient 503 below: the
			// alias resolves and its routes are healthy, the request simply
			// asked for something they cannot do.
			//
			// This should not be reachable from the chat surface, which reads
			// hive_capabilities.tools before attaching a tools block, so seeing
			// it means the surface advertised on an alias the catalog says is
			// incapable. That is worth a specific message: a 503 here would
			// send the next reader to look for a routing outage that is not
			// happening.
			apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest,
				"this model cannot use tools; retry without "+toolParam)
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

	// Custom instructions (issue #1363): the standing "how should you respond"
	// text this person wrote, prepended as its own system block. Runs AFTER
	// the recall block above so it ends up FIRST in the messages array, which
	// is the order it has to be in: instructions are what the user asked for,
	// recall is background the system supplied, and a model reading the two
	// should meet the request before the context.
	//
	// Same degradation contract as recall: a read or injection failure logs
	// and serves the turn unshaped.
	if h.deps.Instructions != nil {
		text, insErr := h.deps.Instructions.Instructions(r.Context(), user.TenantID, user.ID)
		if insErr != nil {
			slog.Warn("custom instructions read failed; serving without them", "err", insErr)
		} else if block := buildInstructionBlock(text); block != "" {
			injected, injectErr := injectMemoryBlock(raw, block)
			if injectErr != nil {
				slog.Warn("custom instructions injection failed; serving without them", "err", injectErr)
			} else {
				raw = injected
			}
		}
	}

	// Bound the request for a variable-price alias, before anything is held or
	// dispatched, exactly as the API path does (inference.dispatch step 2d).
	// This path had no bound at all: a session turn on a variable-price alias
	// went upstream with no size cap and no completion ceiling, so the only
	// thing standing between it and an arbitrarily large charge was a hold
	// sized for a request nobody had checked (issue #1372). The hold below is
	// sized from THIS body, which is only honest once the body is the bounded
	// one. A pass-through, and one comparison, for every fixed-price alias.
	bounded, withinBounds := inference.EnforceVariablePriceBounds(w, route, inference.EndpointChatCompletions, clientModel, raw)
	if !withinBounds {
		return
	}
	raw = bounded

	// Money path (#746): a session turn is served only once it can be
	// charged. Every refusal inside startSettlement is written before a
	// provider is reached, and the hold it takes reaches a terminal state
	// exactly once -- finalized on the success path below, released by this
	// deferred call on every other exit, never both and never neither.
	settle, refused := h.startSettlement(r.Context(), w, user.TenantID, route, clientModel, requestID, raw)
	if refused {
		return
	}
	settled := false
	releaseReason := "interrupted"
	defer func() {
		if settle != nil && !settled {
			settle.Release(releaseReason)
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

	// Dispatch to LiteLLM with the same bounded retry on 429/5xx the API-key
	// surface uses (inference.DispatchWithRetry, backed by
	// internal/inference/retry.go's dispatchWithRetry). Before this fix this
	// surface made a single bare h.deps.HTTP.Do call with no retry at all,
	// which is what let a JWT chat send die on the first exhausted free-pool
	// member instead of trying one of the pool's other healthy members
	// (issue #1564). A fresh *http.Request is built on every attempt because
	// an http.Request's body can only be read once.
	dispatchToLiteLLM := func(ctx context.Context, litellmModel string, reqBody []byte) (*http.Response, error) {
		upstream, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			strings.TrimRight(h.deps.LiteLLMURL, "/")+"/v1/chat/completions",
			bytes.NewReader(reqBody),
		)
		if err != nil {
			return nil, err
		}
		upstream.Header.Set("Content-Type", "application/json")
		upstream.Header.Set("X-Request-Id", requestID.String())
		if h.deps.LiteLLMKey != "" {
			upstream.Header.Set("Authorization", "Bearer "+h.deps.LiteLLMKey)
		}
		return h.deps.HTTP.Do(upstream)
	}

	// Bound the WHOLE ladder to the same budget a single unretried call
	// already had, rather than inventing a new number. h.deps.HTTP.Timeout
	// applies per Do call, so without this a wedged upstream costs up to
	// len(retryDelays) times that timeout instead of once (review finding on
	// PR #1568: up to ~20 minutes on the 5-minute default instead of ~5).
	// Reusing the existing timeout as the ladder's total deadline means the
	// common, fast-failing case this PR exists for -- a 429/5xx that answers
	// in milliseconds -- is untouched (nearly the whole budget survives for
	// the eventual successful attempt), while a genuinely hung upstream now
	// gets effectively one attempt's worth of patience, not four. A zero
	// Timeout (an explicitly unbounded test client) is left unbounded here
	// too, matching what an unretried call would have done.
	dispatchCtx := r.Context()
	if timeout := h.deps.HTTP.Timeout; timeout > 0 {
		var cancel context.CancelFunc
		dispatchCtx, cancel = context.WithTimeout(dispatchCtx, timeout)
		defer cancel()
	}

	started := time.Now()
	resp, err := inference.DispatchWithRetry(dispatchCtx, route.LiteLLMModelName, body, dispatchToLiteLLM)
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
	// shape is the evidence the zero-content guard decides on (issue #1526):
	// what the relay delivered, and whether the upstream ended its own stream.
	// Never the caller's socket state, which a blank answer is exactly what
	// provokes -- see inference.DeliveryShape and the guard beside it for why an
	// earlier revision keyed on that and suppressed the guard in the population
	// it protects.
	// Refusals need no flag here: this relay folds delta.refusal into
	// completion, so a refusal-only answer already carries visible content.
	shape := inference.DeliveryShape{Surface: inference.ZeroContentSurfaceSessionChat}
	// sawDone records the upstream's own [DONE] sentinel, which stands however
	// the caller's socket behaved afterwards. A client that closes the instant
	// it receives [DONE] is the NORMAL ending of a blank stream, so a
	// completion test that consulted only the live request context below would
	// go false in exactly the population this guard exists for.
	sawDone := false

	// mintedID feeds SanitizeVariablePriceFrame below, which now sanitizes
	// every frame of every alias: it has no memory of frames before it, so
	// the id replacement must be minted once per stream here and reused on
	// every frame, matching the id-stability contract
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
		// Every data frame is sanitized before the write, on every alias,
		// fixed-price included. This used to sanitize the variable-price
		// case only, on the reasoning that a fixed-price frame carries no
		// cost data to leak -- true for cost, but every frame still carries
		// the upstream's own id and, on some providers, system_fingerprint,
		// which is exactly the same provider-identity leak
		// inference.SanitizeVariablePriceFrame exists to strip, and
		// fixed-price is the D-032 norm so this is most of this relay's
		// traffic (security review finding, PR #1222). An unparseable frame
		// is dropped rather than forwarded, because an unparseable frame is
		// precisely the one whose contents are unknown; only [DONE] and
		// non-data SSE lines (blank lines, event: fields) pass through
		// untouched, since they carry nothing provider-identifying.
		isData := bytes.HasPrefix(line, []byte("data: "))
		payload := bytes.TrimPrefix(line, []byte("data: "))
		isDone := isData && bytes.Equal(payload, []byte("[DONE]"))

		if isData && !isDone {
			sanitized, sanOK := inference.SanitizeVariablePriceFrame(payload, clientModel, mintedID)
			if !sanOK {
				// An upstream failure delivered inside a body whose 200 was
				// already committed. The sanitizer refuses such a frame, and
				// dropping it silently truncates the answer mid-sentence with
				// nothing to render and nothing to retry on: the customer sees
				// a half-finished reply and a normal end of stream. Replace it
				// with a gateway-owned error the client can display instead,
				// carrying no upstream text at all. Anything else the
				// sanitizer refused is still unknown content and is still
				// dropped.
				replacement, upstream, isErrorFrame := sanitize.ReplaceErrorFrame(
					payload, apierr.UpstreamUnavailableMessage(clientModel))
				if !isErrorFrame {
					slog.Warn("session chat: dropping an unparseable upstream frame",
						"request_id", requestID, "alias", clientModel)
					continue
				}
				// This log is now the ONLY place the upstream text survives,
				// so it carries the request id the trace rows are keyed on.
				// %.512s truncates on a rune boundary, unlike a byte slice,
				// which mangles the tail of any non-ASCII upstream message.
				slog.Warn("session chat: replaced an upstream error frame",
					"request_id", requestID, "alias", clientModel,
					"upstream_error", fmt.Sprintf("%.512s", upstream))
				sanitized = replacement
			}
			// Hold the frame's own usage block to total_tokens equal to
			// prompt plus completion, on the bytes the customer actually
			// receives, for the same reason the four API-key endpoints hold
			// it at their clamp boundary (issue #1472). This relay never
			// builds a typed usage object -- the sseEnvelope decode below
			// happens after the write and exists for billing -- so before
			// this line a violating total reached the Open WebUI front end
			// verbatim while every API-key surface was corrected. The charge
			// is untouched: it prices prompt and completion, which this never
			// rewrites. Frames carrying no usage member, which is every frame
			// of a stream but the last, come back byte-identical.
			sanitized = inference.EnforceUsageIdentityInFrame(sanitized, requestID.String(), clientModel, inference.EndpointChatCompletions)
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
			sawDone = true
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
			if rawPresent(choice.Delta.ToolCalls) || rawPresent(choice.Delta.FunctionCall) {
				shape.HasToolCall = true
			}
			shape.ObserveFinishReason(choice.FinishReason)
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

	// Did the relay reach the upstream's own end of stream (#1526)? Either the
	// sentinel arrived, or the body closed cleanly on a request that was never
	// cancelled, which is how some providers end a stream with no sentinel at
	// all. A scanner error, or a read that failed because the request context
	// was cancelled, is a truncation rather than a completion and leaves this
	// false, so the turn bills (D-034, fail closed). Same rule as inference's
	// own relay, and deliberately not a test of whether the caller is still
	// connected: see inference.DeliveryShape.
	shape.Completed = sawDone || (streamErr == nil && r.Context().Err() == nil)

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
	var confirmed, delivered, zeroContent bool
	// Hoisted out of the branch so the settlement can be logged AFTER the
	// zero-content guard below has had its say (#1538). Logging it inside the
	// branch printed the priced figure for a burn that was then absorbed, which
	// reads as a charge that happened.
	var variableSettled inference.VariableSettlement
	if route.Pricing.IsUpstreamActual() {
		// This alias has no catalog price. Its charge is the cost the upstream
		// reported for this generation, at the credit peg and with no margin
		// factor (D-064). A cost that
		// is missing, unreadable or a confident zero settles at the hold rather
		// than at nothing, which is the whole point: this is the streaming path
		// Open WebUI uses, so it is where a silent free-serve would do the most
		// damage.
		variableSettled = inference.UpstreamActualSettlement(
			rawUsagePayload, settle.Held(), hasUsage,
			inTokens, outTokens, completion.String())
		costCredits, confirmed, delivered = variableSettled.Credits, variableSettled.Confirmed, variableSettled.Delivered
	} else {
		costCredits, confirmed, delivered, zeroContent = inference.ChatSettlementCredits(
			route, hasUsage, cacheUsage.FreshInputTokens, cacheUsage.CacheReadTokens, cacheUsage.CacheWriteTokens, outTokens, raw, completion.String(), shape)
	}
	if !zeroContent {
		// Zero-content guard over the OTHER arm of the branch above (#1538).
		// ChatSettlementCredits already applied it to a catalog-priced turn, so
		// this reaches the variable-price arm, where UpstreamActualSettlement
		// reports Delivered on any successful cost read and a reasoning burn on
		// hive-auto was therefore charged the cost the upstream reported for
		// tokens the customer never saw. Applied last, to whatever the branch
		// settled at, exactly as settleStream applies it on the API-key path.
		//
		// Skipped when the guard already fired, and the reason is exactly one
		// thing: a second pass returns zeroContent FALSE, because the first
		// firing set delivered false and the guard early-returns on that. The
		// release reason below is read from that flag, so an unconditional call
		// downgrades every catalog-priced burn to "upstream_error" and loses
		// the ledger signal this whole change exists to produce. Measured, not
		// assumed: removing this skip and the identical one in rag/billing.go
		// turns four existing tests red, two of them stating the mechanism
		// outright as release reason = "upstream_error", want "zero_content".
		//
		// It is NOT protection against double counting. The same early return
		// means a second pass can never reach the absorbed-credits counter, so
		// nothing here is at risk of being counted twice.
		costCredits, delivered, zeroContent = inference.ApplyZeroContentGuard(
			route.AliasID, shape, completion.String(), costCredits, delivered, inTokens, outTokens)
	}
	if route.Pricing.IsUpstreamActual() && (delivered || zeroContent) {
		// generation_id is the audit handle for this charge, and on a
		// variable-price alias it is the only thing that recovers WHICH pool
		// member served the request. Operator log only: an upstream identifier
		// can carry a provider name and audit_log fans out to third-party
		// sinks.
		//
		// An absorbed burn is logged too, for the same reason (#1538): a rise
		// in absorbed credits is a routing signal, and a routing signal nobody
		// can attribute to a member is not actionable. credits reads zero after
		// the guard, which is what the customer was charged; what Hive absorbed
		// is on hive_chat_zero_content_absorbed_credits_total and on the
		// guard's own line.
		slog.Info("session chat: variable-price settlement",
			"request_id", requestID, "alias", clientModel, "reason", variableSettled.Reason,
			"credits", costCredits, "confirmed", confirmed,
			"generation_id", variableSettled.GenerationID, "held_credits", settle.Held(),
			"absorbed_zero_content", zeroContent)
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
		switch {
		case zeroContent:
			// Ahead of the disconnect arm, not behind it, for the reason
			// settleStream states at the same fork: the guard already requires
			// a completed stream, and a blank answer is exactly what makes a
			// customer close the tab, so labelling this a disconnect would file
			// the commonest ending of a burn as an abandonment and lose the
			// absorbed cost with it (#1526).
			releaseReason = "zero_content"
		case r.Context().Err() != nil:
			releaseReason = "client_disconnect"
		}
		costCredits = 0
	case settle.Finalize(costCredits, confirmed, inTokens, outTokens, cacheUsage.CacheReadTokens, cacheUsage.CacheWriteTokens):
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
