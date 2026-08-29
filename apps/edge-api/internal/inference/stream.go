package inference

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
)

// sseScanLineMaxBytes is the maximum single SSE line either relay loop in
// this package (executeStreaming, executeResponsesStreaming) accepts from
// upstream before bufio.Scanner reports bufio.ErrTooLong. Both loops pass it
// as scanner.Buffer's max argument. Named rather than a bare literal so the
// oversized-line regression tests (stream_scanner_err_test.go,
// stream_responses_scanner_err_test.go) stay correct if this value ever
// changes, instead of quietly testing under the real limit.
const sseScanLineMaxBytes = 512 * 1024

// UsageAccumulator tracks token usage and output text across SSE streaming chunks.
// Content is recorded so the usage clamp can recompute completion_tokens when
// the upstream terminal usage chunk reports 0 on a non-empty response.
type UsageAccumulator struct {
	// InputTokens is the RAW prompt_tokens figure the upstream reported,
	// unchanged meaning from before this feature: the total prompt tokens
	// delivered, cache subset included, for the ledger and for display. It is
	// NOT what billing prices -- see FreshInputTokens for that.
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	// CachedTokens is the cache-READ subset of InputTokens.
	CachedTokens int64
	// CacheWriteTokens is the cache-WRITE quantity this turn created. Never
	// populated before this feature (usage_events.cache_write_tokens always
	// stored 0); now carries the real figure NormalizeCacheUsage derives.
	CacheWriteTokens int64
	// FreshInputTokens is InputTokens with both cache components removed
	// (INCLUSIVE shape) or InputTokens unchanged (EXCLUSIVE shape) -- see
	// NormalizeCacheUsage. This, not InputTokens, is what CreditsForTokens
	// prices as the input component.
	FreshInputTokens int64
	TotalTokens      int64
	HasUsage         bool
	// HasForwardedChunk records whether at least one upstream data chunk was
	// relayed to the caller through any relay branch -- typed, sanitized, or
	// verbatim pass-through. It is the delivery signal that does not depend on
	// anything accumulating: a frame whose JSON fails to parse into
	// ChatCompletionChunk still reaches the customer through the pass-through,
	// and a tool-call-only turn forwards real work while accumulating no
	// visible content. Without it both shapes settle at zero and get filed as
	// upstream_error over a fully delivered response (#1215).
	HasForwardedChunk bool
	Content           strings.Builder
	// RawUsageChunk is the VERBATIM bytes of the last streamed chunk that
	// carried a usage object. It exists because ChatCompletionChunk is a typed
	// struct and unmarshalling into it silently discards every field we did
	// not declare, which includes the upstream's reported cost. That discard
	// is exactly what keeps our cost from leaking to the customer, so the fix
	// is not to widen the struct but to keep the original bytes here, where
	// only settlement reads them.
	//
	// Empty for a fixed-price alias: nothing reads it there, and holding the
	// bytes for every stream on every alias would be pure overhead.
	RawUsageChunk []byte
}

// Accumulate copies usage fields from a chunk if present. aliasID is passed
// through to NormalizeCacheUsage for its WARN log line only.
func (a *UsageAccumulator) Accumulate(chunk ChatCompletionChunk, aliasID string) {
	if chunk.Usage == nil {
		return
	}
	a.HasUsage = true
	a.InputTokens = chunk.Usage.PromptTokens
	a.OutputTokens = chunk.Usage.CompletionTokens
	a.TotalTokens = chunk.Usage.TotalTokens
	if chunk.Usage.CompletionTokensDetails != nil {
		a.ReasoningTokens = chunk.Usage.CompletionTokensDetails.ReasoningTokens
	}
	cache := NormalizeCacheUsage(chunk.Usage, aliasID, "")
	a.FreshInputTokens = cache.FreshInputTokens
	a.CachedTokens = cache.CacheReadTokens
	a.CacheWriteTokens = cache.CacheWriteTokens
}

// AccumulateContent appends all delta content + refusal text carried by a
// streaming chunk. Tool-call deltas are ignored — they do not consume
// completion tokens in the same way as visible output text.
func (a *UsageAccumulator) AccumulateContent(chunk ChatCompletionChunk) {
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != nil {
			a.Content.WriteString(*choice.Delta.Content)
		}
		if choice.Delta.Refusal != nil {
			a.Content.WriteString(*choice.Delta.Refusal)
		}
	}
}

// ClampUsage applies the zero-completion-tokens clamp against the accumulated
// output text. Safe to call with nil chunk.Usage — it's a no-op.
func (a *UsageAccumulator) ClampUsage(u *UsageResponse, upstreamID, aliasID, endpoint string) {
	if u == nil {
		return
	}
	clampZeroCompletionUsage(u, []string{a.Content.String()}, upstreamID, aliasID, endpoint)
}

// ToUsageResponse constructs a UsageResponse from accumulated values, for
// this gateway's own customer-facing response (the synthesized terminal
// chunk, and recordCompletedEvent's usage argument). It deliberately emits
// only the INCLUSIVE-shape fields (PromptTokensDetails): the two
// Anthropic-native pointer fields on UsageResponse must never be set here,
// per the echo contract -- see the comment on UsageResponse itself.
func (a *UsageAccumulator) ToUsageResponse() *UsageResponse {
	u := &UsageResponse{
		PromptTokens:     a.InputTokens,
		CompletionTokens: a.OutputTokens,
		TotalTokens:      a.TotalTokens,
		CompletionTokensDetails: &CompletionTokensDetails{
			ReasoningTokens: a.ReasoningTokens,
		},
		PromptTokensDetails: &PromptTokensDetails{
			CachedTokens:     a.CachedTokens,
			CacheWriteTokens: a.CacheWriteTokens,
		},
	}
	return u
}

// executeStreaming runs the full streaming inference lifecycle:
// authorize -> route -> validate -> attempt -> reserve -> dispatch -> relay SSE -> finalize.
func (o *Orchestrator) executeStreaming(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	endpoint string,
	body []byte,
	model string,
	aliasID string,
	needFlags NeedFlags,
	estimatedCredits int64,
	includeUsage bool,
	reasoningEffort *string,
	dispatch dispatchFunc,
) error {
	// 1. Authorize
	authHeader := r.Header.Get("Authorization")
	snapshot, headers, authErr := o.authorizer.Authorize(ctx, authHeader, model, estimatedCredits, 0, 0)
	if authErr != nil {
		// See the matching comment in orchestrator.go's executeSync: route
		// through the shared apierrors.WriteAuthFailure mapper rather than a
		// second copy of its status-code switch.
		apierrors.WriteAuthFailure(w, authErr, headers)
		return nil
	}

	// 2. Select route
	route, err := o.selectRoute(ctx, snapshot, SelectRouteInput{
		AliasID:             model,
		NeedChatCompletions: needFlags.NeedChatCompletions,
		NeedResponses:       needFlags.NeedResponses,
		NeedEmbeddings:      needFlags.NeedEmbeddings,
		NeedStreaming:       needFlags.NeedStreaming,
		NeedReasoning:       needFlags.NeedReasoning,
		RequireToolCapable:  needFlags.RequireToolCapable,
	})
	if err != nil {
		if errors.Is(err, ErrAccountNotProvisioned) {
			writeAccountNotProvisionedError(w)
			return nil
		}
		if errors.Is(err, ErrModelNotEntitled) {
			writeModelNotEntitledError(w, model)
			return nil
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "not found") {
			writeModelNotFoundError(w, model)
			return nil
		}
		if strings.Contains(errMsg, "no eligible") || strings.Contains(errMsg, "capability") {
			code := "capability_mismatch"
			apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("No route supports the requested capabilities for model '%s'.", model), &code)
			return nil
		}
		code := "routing_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error",
			"Failed to select a route for this request.", &code)
		return nil
	}

	// 3. Validate reasoning capability
	// If the route was selected without NeedReasoning, it may not support reasoning.
	routeSupportsReasoning := needFlags.NeedReasoning // route was selected with this requirement
	if !validateReasoningCapability(w, model, reasoningEffort, routeSupportsReasoning) {
		return nil
	}

	// 3b. Refuse an alias this endpoint cannot price, before any hold is taken
	// and before a provider is ever reached (#688, D-034).
	if !requireTokenPricing(w, route, endpoint, model) {
		return nil
	}

	// 3c. Read the caller's own completion ceiling BEFORE any outbound rewrite,
	// same contract as the sync path's step 2c (issue #1283).
	ceiling := requestedCompletionCeiling(endpoint, body)

	// And pin the outbound body to it, same contract as the sync path: two
	// contradictory ceilings must not reach the provider as the larger one
	// while settlement holds the request to the smaller. See
	// pinCompletionCeiling.
	body = pinCompletionCeiling(body, endpoint, ceiling)

	// 3d. Bound the request for a variable-price alias, before dispatch. Its
	// hold is only provably sufficient below a known request size and a known
	// completion ceiling; see EnforceVariablePriceBounds. A pass-through for
	// every fixed-price alias.
	boundedBody, withinBounds := EnforceVariablePriceBounds(w, route, endpoint, model, body)
	if !withinBounds {
		return nil
	}
	body = boundedBody

	// 4. Start attempt
	requestID := uuid.New().String()
	attempt, err := o.accounting.StartAttempt(ctx, StartAttemptInput{
		AccountID:     snapshot.AccountID,
		RequestID:     requestID,
		AttemptNumber: 1,
		Endpoint:      endpoint,
		ModelAlias:    model,
		Status:        "streaming",
		APIKeyID:      snapshot.KeyID,
	})
	if err != nil {
		log.Printf("inference: start attempt failed (non-fatal): %v", err)
	}

	// 5. Create reservation
	reservation, err := o.accounting.CreateReservation(ctx, CreateReservationInput{
		AccountID:     snapshot.AccountID,
		RequestID:     requestID,
		AttemptNumber: 1,
		APIKeyID:      snapshot.KeyID,
		Endpoint:      endpoint,
		ModelAlias:    model,
		// A variable-price alias raises this from its catalog row; a fixed
		// one keeps the flat endpoint default. See ReservationCredits.
		EstimatedCredits: ReservationCredits(route, estimatedCredits),
		PolicyMode:       "strict",
	})
	if err != nil && refuseOnReservationFailure(w, endpoint, model, err) {
		return nil
	}

	// Set up defer for reservation settlement. This is the single settlement
	// site for a streaming request: it always runs, whether the stream ends
	// normally or the client disconnects mid-stream, and it always dispatches
	// on its own background context (see settleStream) rather than the ctx
	// this function was called with. A client disconnect is exactly what
	// cancels that ctx, so settling on it (the old behavior) let a dead
	// context silently swallow both the charge and the release.
	finalized := false
	accumulator := &UsageAccumulator{}
	defer func() {
		if finalized {
			return
		}
		finalized = o.settleStream(ctx, snapshot, attempt, reservation, route, requestID, endpoint, model, ceiling, accumulator, string(body), accumulator.Content.String())
	}()

	// Force upstream usage accounting on a provider verified to honor it
	// (#1226): without this, a caller that never set stream_options.
	// include_usage itself gets no terminal usage frame from a provider that
	// requires the flag, and settlement (#1215) falls back to capturing the
	// full reservation hold instead of the real charge -- fail-closed and
	// correct, but a hold is provably wrong far more often than it needs to
	// be. Reuses the single metering.RewriteBody implementation the
	// chat-orchestrator path already forces unconditionally
	// (apps/edge-api/internal/chat/dispatch.go) rather than a second copy;
	// gated per provider here because, unlike that internal-only surface,
	// this path can reach an upstream the flag has never been verified
	// against. This does not change what Hive bills for -- see the
	// includeUsage-gated strip below, which keeps the flag's effect
	// internal to settlement for a caller that never asked for it.
	if metering.SupportsIncludeUsage(route.Provider) {
		if rewritten, rewriteErr := metering.RewriteBody(body); rewriteErr == nil {
			body = rewritten
		} else {
			log.Printf("inference: include_usage rewrite failed request_id=%s provider=%s: %v", requestID, route.Provider, rewriteErr)
		}
	}

	// 6. Dispatch to LiteLLM with bounded retry on 429/5xx (safe: no bytes
	// have been written to the client yet at this point).
	resp, err := dispatchWithRetry(ctx, route.LiteLLMModelName, body, dispatch)
	if err != nil {
		if reservation.ID != "" {
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, "upstream_error")
		}
		apierrors.WriteProviderBlindUpstreamError(w, model, http.StatusBadGateway, err.Error())
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if reservation.ID != "" {
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, "upstream_error")
		}
		o.recordErrorEvent(ctx, snapshot, attempt, requestID, endpoint, model, resp.StatusCode, string(upstreamBody))
		apierrors.WriteProviderBlindUpstreamError(w, model, resp.StatusCode, string(upstreamBody))
		return nil
	}

	// 7. Assert Flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		if reservation.ID != "" {
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, "internal_error")
		}
		code := "internal_error"
		apierrors.WriteError(w, http.StatusInternalServerError, "api_error",
			"Streaming not supported by server.", &code)
		return nil
	}

	// 8. Set SSE headers and commit response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 9. Relay SSE chunks
	scanner := bufio.NewScanner(resp.Body)
	// Increase buffer for large chunks
	scanner.Buffer(make([]byte, 64*1024), sseScanLineMaxBytes)

	// mintedID is reused for every chunk of this stream: a client-visible id
	// must be stable within one response, and must match the shape
	// normalizeChatCompletion/normalizeCompletion mint for the same
	// endpoint's non-streaming twin. See mintCompletionID.
	mintedID := mintCompletionID(idPrefixForEndpoint(endpoint))
	// finishSeen gates the DeepSeek-family post-finish chunk fix below.
	finishSeen := false

	for scanner.Scan() {
		line := scanner.Text()

		if line == "data: [DONE]" {
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
			break
		}

		if strings.HasPrefix(line, "data: ") {
			jsonData := line[6:]
			var chunk ChatCompletionChunk
			if err := json.Unmarshal([]byte(jsonData), &chunk); err == nil {
				// Rewrite model to alias ID, mint a gateway-owned id and drop
				// system_fingerprint -- upstream identity leaks exactly like
				// normalizeChatCompletion's non-streaming case; see
				// mintCompletionID. upstreamChunkID is kept only for the
				// usage-clamp log line below.
				upstreamChunkID := chunk.ID
				chunk.Model = aliasID
				chunk.ID = mintedID
				chunk.SystemFingerprint = nil

				// Suppress any chunk that arrives after a terminal
				// finish_reason has already been relayed, UNLESS it is a
				// genuine usage-only terminal frame (stream_options.
				// include_usage legitimately delivers cost data in its own
				// frame after finish_reason). Observed live on DeepSeek-family
				// streams via OpenRouter: one extra empty role/content chunk
				// arrives after finish_reason=stop, before [DONE], and a
				// strict SSE client that already closed the message on the
				// real finish frame chokes on anything more (parity finding,
				// 2026-08-26). Still folded into the accumulator below so
				// billing never silently drops content -- only the write to
				// the client is skipped.
				suppressPostFinish := ShouldSuppressPostFinishChunk(finishSeen, chunk)
				if ChunkFinished(chunk) {
					finishSeen = true
				}

				// Track output content so the usage clamp has ground truth.
				accumulator.AccumulateContent(chunk)
				// Clamp upstream-zero completion_tokens against the
				// content streamed so far. Usage typically arrives in
				// the terminal chunk once all deltas are flushed.
				if chunk.Usage != nil {
					accumulator.ClampUsage(chunk.Usage, upstreamChunkID, aliasID, endpoint)
					// Cap the metered completion count at the caller's own
					// ceiling (#1283). Applied to the chunk BEFORE Accumulate
					// copies it and before the frame is re-marshalled, so the
					// ledger charge and the usage frame the caller receives are
					// the same number by construction rather than by two
					// clamps agreeing.
					clampUsageToCeiling(chunk.Usage, route, ceiling, endpoint, aliasID)
					// Keep the untyped bytes only for a route that settles
					// against the upstream's reported cost; see RawUsageChunk.
					if route.Pricing.IsUpstreamActual() {
						accumulator.RawUsageChunk = append(accumulator.RawUsageChunk[:0], jsonData...)
					}
				}
				// Accumulate usage if present
				accumulator.Accumulate(chunk, aliasID)

				// The caller's OWN request decides whether a usage-bearing
				// frame is contract-visible (#1226), never the include_usage
				// this gateway may have just forced upstream for billing
				// alone: accumulation above already ran unconditionally, so
				// billing sees every frame regardless. A caller who never
				// set stream_options.include_usage itself must see the same
				// stream shape it always has. A usage-only frame (no delta,
				// no finish_reason -- the dedicated terminal chunk an
				// OpenAI-compatible upstream emits for this flag) is dropped
				// outright rather than forwarded empty; a frame that also
				// carries real content is forwarded with usage stripped.
				if !includeUsage {
					chunk.Usage = nil
					if len(chunk.Choices) == 0 {
						continue
					}
				}

				if suppressPostFinish {
					continue
				}

				// Re-marshal sanitized chunk
				sanitized, marshalErr := json.Marshal(chunk)
				if marshalErr == nil {
					accumulator.HasForwardedChunk = true
					fmt.Fprintf(w, "data: %s\n\n", sanitized)
					flusher.Flush()
					continue
				}
			}
			// Fallback: the typed decode above failed (an upstream frame our
			// struct can't parse -- the DeepSeek surprise-frame class of
			// input is exactly what drives a chunk here for real, not a
			// theoretical case). A fallback that cannot parse a chunk must
			// not become a fallback that leaks it: every route, fixed-price
			// included, sanitizes through the SAME map-based path the
			// variable-price case always used, id-minted and
			// system_fingerprint-stripped like every other chunk of this
			// stream. Cost-field stripping is a no-op on a frame that has
			// none, so one path serves both pricing models rather than
			// carrying a second, unsanitized one. An unparseable frame is
			// dropped rather than forwarded, because an unparseable frame is
			// exactly the one whose contents are unknown -- and unlike the
			// silent version this replaces, every drop is logged, so an
			// unsanitized-fallback regression is visible in production
			// rather than invisible.
			if sanitized, sanOK := SanitizeVariablePriceFrame([]byte(jsonData), aliasID, mintedID); sanOK {
				accumulator.HasForwardedChunk = true
				fmt.Fprintf(w, "data: %s\n\n", sanitized)
				flusher.Flush()
			} else {
				log.Printf("inference: dropping an unparseable upstream frame endpoint=%s alias=%s: forwarding it verbatim would leak upstream identity and, on a variable-price alias, our cost",
					endpoint, aliasID)
			}
			continue
		}

		if line == "" {
			fmt.Fprint(w, "\n")
			flusher.Flush()
			continue
		}

		// Pass through event: lines and other SSE fields
		fmt.Fprintf(w, "%s\n", line)
		flusher.Flush()
	}

	// 9b. A read/token error ended the relay before [DONE] arrived -- most
	// commonly bufio.ErrTooLong (the 512 KiB scanner.Buffer limit set above),
	// occasionally a genuine upstream connection drop. Previously this was
	// never checked at all (issue #1255 HIGH #1): the client just saw the
	// connection stop, with no error frame, no [DONE], and nothing in the
	// server log. err.Error() is never put in the client-facing message --
	// a net/http read error can carry the upstream's own address, exactly
	// the leak apierrors.WriteProviderBlindUpstreamError exists to prevent
	// everywhere else -- full detail goes to the operator log only. Settlement
	// (the deferred settleStream call) is untouched by this branch: it reads
	// accumulator state that is already final by this point, the same state
	// it would have read without this check.
	//
	// ctx.Err() == nil is required, not optional: r.Context() cancellation
	// (a client hitting stop, or just navigating away) tears down the
	// in-flight upstream body read the exact same way a real relay failure
	// does, so scanner.Err() is context.Canceled on a routine disconnect too.
	// Without this guard every ordinary cancellation would log "SSE relay
	// aborted" and write a stream_interrupted frame to an already-dead
	// socket, burying the ErrTooLong signal this PR exists to surface under
	// the far more common disconnect case. Same distinction settleStream
	// already draws via reqCtx.Err() a few dozen lines below (client
	// disconnect vs. upstream_error) -- settlement still logs and accounts
	// for a disconnect on its own path regardless of this branch, so nothing
	// goes unrecorded by skipping it here.
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		log.Printf("inference: chat completions SSE relay aborted request_id=%s alias=%s endpoint=%s err=%v",
			requestID, aliasID, endpoint, err)
		streamRelayAborted.WithLabelValues(aliasID, endpoint).Inc()
		code := "stream_interrupted"
		if errPayload, marshalErr := json.Marshal(apierrors.NewError("api_error",
			"The response stream ended unexpectedly.", &code)); marshalErr == nil {
			fmt.Fprintf(w, "data: %s\n\n", errPayload)
			flusher.Flush()
		}
		return nil
	}

	// 10. Synthesize terminal usage chunk if requested but upstream didn't send one
	if includeUsage && !accumulator.HasUsage {
		synth := ChatCompletionChunk{
			// Same mintedID as every other chunk of this stream -- a
			// synthesized terminal frame is still part of the one response,
			// and requirement #4 (id stability within a response) applies to
			// it too.
			ID:      mintedID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   aliasID,
			Choices: []ChunkChoice{},
			Usage:   accumulator.ToUsageResponse(),
		}
		synthJSON, err := json.Marshal(synth)
		if err == nil {
			fmt.Fprintf(w, "data: %s\n\n", synthJSON)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}

	return nil
}

// releaseReservationBackground releases a reservation on a fresh bounded
// background context, never the caller's request context: a client
// disconnect is exactly what cancels that context, so releasing on it (the
// original bug this PR fixes) lets a dead context silently swallow the
// release, same as it used to swallow the finalize. Returns true only when
// the release actually reached the control plane, so callers gate
// `finalized` on real settlement instead of assuming success -- a failed
// attempt here still leaves `finalized` false, so the deferred settleStream
// gets one more shot at it before the request returns.
func (o *Orchestrator) releaseReservationBackground(snapshot authz.AuthSnapshot, reservationID, requestID, reason string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), accountingTimeout)
	defer cancel()
	if err := o.accounting.ReleaseReservation(ctx, ReleaseReservationInput{
		AccountID:     snapshot.AccountID,
		ReservationID: reservationID,
		Reason:        reason,
	}); err != nil {
		log.Printf("inference: release reservation failed request_id=%s reservation_id=%s reason=%s: %v", requestID, reservationID, reason, err)
		return false
	}
	return true
}

// settlementCredits derives what a settlement should charge: the resolved
// alias's catalog price applied to real usage tokens when the upstream
// confirmed them, otherwise to a content-based token estimate. There is no
// estimatedCredits fallback here on purpose -- an unconfirmed usage block must
// never turn into a flat, hardcoded overcharge AT THIS FUNCTION'S LEVEL: the
// estimate it returns on an unconfirmed stream is superseded by settleStream,
// which since #1215 captures the reservation hold instead of settling the
// undercharge. delivered is false only when nothing was produced at all; the
// caller must release the reservation in full rather than charge in that case.
//
// Both branches go through the same catalog conversion (#688), so an
// unconfirmed estimate is priced exactly like a confirmed measurement and only
// the CONFIDENCE differs: confirmed is returned false for it, which is what
// tells control-plane to clamp the charge to the hold and open a reconciliation
// job instead of treating the figure as measured truth.
//
// confirmed is not simply hasUsage: a usage block that reports neither prompt
// nor completion tokens carries no quantity to price, so it is treated as
// absent rather than as a measured zero, which would otherwise settle a
// delivered response for nothing.
//
// That makes the prompt and completion SPLIT load-bearing where this branch
// previously keyed on total_tokens: a provider reporting only a total, with no
// split, now falls through to the content estimate and stays flagged
// unconfirmed permanently, since the reconciliation job that would resolve it
// has a writer and no reader (#600). Neither OpenRouter nor Groq produces that
// shape and the estimate errs in the customer's favour, so it is recorded here
// rather than guarded against.
//
// The unconfirmed estimate bills prompt tokens too, not completion bytes
// alone: a client that aborts right after the first output token (e.g. after a
// long prompt finishes prefill) must not settle for ~1 credit just because only
// one token of *output* existed to estimate from. Prompt is the parsed
// message/input text (see promptText in usage_clamp.go), not the raw request
// body -- the raw body also carries field names, sampling params, tool schemas,
// and base64 image data, and counting those as tokens is issue #602's
// over-charge root cause (a request body under the 10MiB limit could estimate
// ~2.6M pre-rescale credits against the then-10000-credit hold). The control-plane hard clamp in
// finalizeLocked is the backstop that keeps any residual overcount here from
// ever exceeding the reserved hold, but this function should still return a
// realistic number.
//
// The token estimate feeding that unconfirmed branch is itself calibrated to
// land BELOW real tokenization for every writing system rather than near its
// average, so the direction of its error favours the customer instead of
// depending on the script they write in (issue #673). See bytesPerToken in
// usage_clamp.go for the measurements; the clamp is the backstop, not the
// reason the figure is defensible. The catalog conversion here does not change
// that: it only turns the estimated token count into the alias's price, so an
// under-counted estimate stays under-counted in credits too.
func settlementCredits(route SelectRouteResult, hasUsage bool, freshInputTokens, cacheReadTokens, cacheWriteTokens, outputTokens int64, prompt, content string, ceiling int64) (credits int64, confirmed bool, delivered bool) {
	if hasUsage && freshInputTokens+cacheReadTokens+cacheWriteTokens+outputTokens > 0 {
		return CreditsForTokens(route, freshInputTokens, cacheReadTokens, cacheWriteTokens, outputTokens), true, true
	}
	completion := estimateCompletionTokens(content)
	if completion == 0 {
		return 0, false, false
	}
	// Bound the estimate by the ceiling the caller set (#1283). This branch is
	// reached exactly when the upstream sent no usable usage block, which is
	// also exactly when clampUsageToCeiling had no usage object to cap: without
	// this, the one settlement path that prices from content length was the one
	// path with no ceiling on it at all, and a 200 carrying content and no
	// usage block billed 41 times a max_tokens of 8. The streaming caller
	// survived that only because its unconfirmed branch discards this figure
	// for a hold capture that is already bounded; the synchronous caller has no
	// such override. A guess about how many tokens some text came to may not
	// exceed the number the caller authorized.
	if ceiling > 0 && completion > ceiling {
		log.Printf("inference: bounding a content-length completion estimate at the requested ceiling alias=%s estimated_completion_tokens=%d requested_max_tokens=%d",
			route.AliasID, completion, ceiling)
		completion = ceiling
	}
	// A content-based estimate carries no cache breakdown at all: nothing
	// downstream of a byte-length guess can tell a cache token apart from a
	// fresh one, so it prices every estimated prompt byte as fresh input,
	// same as before this feature existed.
	return CreditsForTokens(route, estimateCompletionTokens(prompt), 0, 0, completion), false, true
}

// settleStream is the single settlement point for an ended streaming
// request (chat completions or the Responses API): it finalizes a charge
// for delivered tokens, or releases the reservation hold in full when
// nothing reached the accumulator. It always dispatches on its own bounded
// background context, independent of the caller's request context, because
// the caller's context is exactly what a client disconnect cancels.
// TerminalUsageConfirmed comes from settlementCredits: it is only ever true
// when the upstream itself sent a usage block carrying real token counts, so
// anything settled from a content estimate is flagged for reconciliation rather
// than treated as a confirmed final charge. reqCtx is the caller's original request context,
// consulted only to tell apart the two ways delivered can end up false: a
// cancelled reqCtx means the client hung up; a live reqCtx with nothing
// delivered means the upstream provider ended or errored the stream while
// the client was still there.
//
// reservation.ID may be empty: CreateReservation is allowed to fail
// non-fatally (a transient control-plane error should not abort a request
// that could otherwise complete), in which case there is nothing to release
// or finalize. That must not also drop the usage telemetry for the request --
// see the reservation.ID == "" branches below.
// route carries the alias's catalog price, which is what the charge is derived
// from (#688); it is the same route the request was dispatched to, so a charge
// can never be priced off a different alias than the one that served it.
func (o *Orchestrator) settleStream(reqCtx context.Context, snapshot authz.AuthSnapshot, attempt AttemptResult, reservation ReservationResult, route SelectRouteResult, requestID, endpoint, model string, ceiling int64, acc *UsageAccumulator, promptBody, content string) bool {
	// Parse promptBody (the raw request bytes) down to just the message/input
	// text before estimating -- see promptText in usage_clamp.go for why the
	// raw bytes themselves must never be counted directly (issue #602).
	var credits int64
	var confirmed, delivered bool
	if route.Pricing.IsUpstreamActual() {
		// No catalog price exists for this alias, since the charge comes from the
		// cost the upstream reported in its terminal usage chunk. A failed
		// read settles at the hold rather than at zero; see
		// UpstreamActualSettlement.
		settled := UpstreamActualSettlement(
			acc.RawUsageChunk, reservation.Held(),
			acc.HasUsage, acc.InputTokens, acc.OutputTokens, content)
		credits, confirmed, delivered = settled.Credits, settled.Confirmed, settled.Delivered
		if delivered {
			// generation_id is the audit handle for this charge: it is what
			// recovers the model the router actually chose, which the response
			// itself no longer names. Operator log only, never audit_log.
			log.Printf("inference: variable-price settlement request_id=%s reservation_id=%s endpoint=%s model=%s reason=%s credits=%d confirmed=%v generation_id=%s held_credits=%d",
				requestID, reservation.ID, endpoint, model, settled.Reason,
				settled.Credits, settled.Confirmed, settled.GenerationID, reservation.Held())
		}
	} else {
		credits, confirmed, delivered = settlementCredits(route, acc.HasUsage, acc.FreshInputTokens, acc.CachedTokens, acc.CacheWriteTokens, acc.OutputTokens, promptText(endpoint, []byte(promptBody)), content, ceiling)
	}

	// A frame reached the caller even though nothing accumulated: an
	// unparseable chunk forwarded through the verbatim pass-through, or a
	// tool-call-only turn whose deltas AccumulateContent ignores. The response
	// was served, so it bills (#1215); with no accumulated quantity to price,
	// the hold capture below is the only billable figure left.
	if !delivered && acc.HasForwardedChunk {
		delivered = true
	}

	if !delivered {
		reason, eventType := "upstream_error", "upstream_error"
		if reqCtx.Err() != nil {
			reason, eventType = "client_disconnect", "interrupted"
		}
		// Say so. The synchronous path already logs this case; the streaming
		// path released the hold in silence, and a request that was served for
		// nothing is exactly the shape that went unnoticed for three days
		// (issue #626). It matters more now that agent traffic streams: a turn
		// that only called tools accumulates no content, because
		// AccumulateContent ignores tool-call deltas, so a real billable turn
		// can land here and be filed as an upstream error with nothing said.
		log.Printf("inference: settle stream delivered nothing, releasing hold request_id=%s reservation_id=%s endpoint=%s model=%s reason=%s: upstream returned no usage and no output",
			requestID, reservation.ID, endpoint, model, reason)
		if reservation.ID != "" {
			releaseCtx, cancelRelease := freshSettlementCtx()
			err := o.accounting.ReleaseReservation(releaseCtx, ReleaseReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				Reason:        reason,
			})
			cancelRelease()
			if err != nil {
				log.Printf("inference: settle release failed request_id=%s reservation_id=%s: %v", requestID, reservation.ID, err)
				return false
			}
		}
		eventCtx, cancelEvent := freshSettlementCtx()
		defer cancelEvent()
		o.recordInterruptedEvent(eventCtx, snapshot, attempt, requestID, endpoint, model, acc, eventType)
		return true
	}

	// Fail-closed money capture (#1215): actuals were unavailable on a stream
	// that delivered output, so the charge is the full reservation hold -- the
	// same rule UpstreamActualSettlement has always applied to variable-price
	// aliases, extended here to every catalog-priced stream. Control-plane
	// clamps at the hold and files a reconciliation job; TerminalUsageConfirmed
	// stays false so nothing records an estimate as measured truth. The alarm
	// below is the only standing signal that a provider stopped honouring
	// stream_options.include_usage or shipped unparseable usage frames.
	if !confirmed {
		if reservation.ID != "" {
			// Bounded by the caller's own completion ceiling (#1283), for the
			// same reason the sync path's zero-content capture is: the hold is
			// a flat authorization floor, so capturing it whole against a
			// request capped at a handful of completion tokens breaches the
			// never-bill-past-the-ceiling invariant far harder than the
			// undercharge this capture exists to prevent. It only ever lowers
			// the figure, and never to zero, so the fail-closed property is
			// untouched.
			credits = capCaptureAtCeiling(route, ceiling,
				captureInputTokens(acc.HasUsage, acc.FreshInputTokens, endpoint, []byte(promptBody)),
				acc.CachedTokens, acc.CacheWriteTokens, reservation.Held())
		}
		streamUsageBlockMissing.WithLabelValues(model, endpoint).Inc()
		log.Printf("inference: ERROR stream_usage_block_missing request_id=%s reservation_id=%s endpoint=%s model=%s captured_reservation_credits=%d content_bytes=%d: upstream sent no usable usage block; settled at the reservation hold per D-034 (#1215)",
			requestID, reservation.ID, endpoint, model, credits, len(content))
	}

	if reservation.ID == "" {
		// No reservation ever existed to finalize, but the request still
		// delivered billable content -- record it so the telemetry isn't
		// silently dropped just because CreateReservation failed earlier.
		eventCtx, cancelEvent := freshSettlementCtx()
		defer cancelEvent()
		o.recordCompletedEvent(eventCtx, snapshot, attempt, requestID, endpoint, model, acc.ToUsageResponse())
		return true
	}

	finalizeCtx, cancelFinalize := freshSettlementCtx()
	err := o.accounting.FinalizeReservation(finalizeCtx, FinalizeReservationInput{
		AccountID:              snapshot.AccountID,
		ReservationID:          reservation.ID,
		ActualCredits:          credits,
		TerminalUsageConfirmed: confirmed,
		Status:                 "completed",
		// Forward every metered count the settlement priced from (#856,
		// #1174), same as the sync path in orchestrator.go: control-plane
		// writes them onto the completed usage event and the
		// api_key_usage_rollups row. This route never sent any token counts
		// on finalize before, so all four rollup columns stayed zero here.
		InputTokens:      acc.InputTokens,
		OutputTokens:     acc.OutputTokens,
		CacheReadTokens:  acc.CachedTokens,
		CacheWriteTokens: acc.CacheWriteTokens,
	})
	// Cancelled the moment finalize returns, never before: the call has
	// already completed, so this can only release the timer, never abort a
	// charge in flight. Nothing below reads finalizeCtx.
	cancelFinalize()
	if err != nil {
		log.Printf("inference: settle finalize failed request_id=%s reservation_id=%s: %v", requestID, reservation.ID, err)
		// A failed finalize must not leave the hold stranded: fall back to a
		// full release so the customer's credits are freed rather than
		// locked forever behind a charge that never landed.
		releaseCtx, cancelRelease := freshSettlementCtx()
		relErr := o.accounting.ReleaseReservation(releaseCtx, ReleaseReservationInput{
			AccountID:     snapshot.AccountID,
			ReservationID: reservation.ID,
			Reason:        "finalize_failed",
		})
		cancelRelease()
		if relErr != nil {
			log.Printf("inference: settle finalize-fallback release failed request_id=%s reservation_id=%s: %v", requestID, reservation.ID, relErr)
			return false
		}
		log.Printf("inference: settle finalize failed, released reservation instead request_id=%s reservation_id=%s", requestID, reservation.ID)
		eventCtx, cancelEvent := freshSettlementCtx()
		defer cancelEvent()
		o.recordInterruptedEvent(eventCtx, snapshot, attempt, requestID, endpoint, model, acc, "finalize_failed")
		return true
	}
	eventCtx, cancelEvent := freshSettlementCtx()
	defer cancelEvent()
	o.recordCompletedEvent(eventCtx, snapshot, attempt, requestID, endpoint, model, acc.ToUsageResponse())
	return true
}

// freshSettlementCtx returns a new background context carrying a full
// accountingTimeout budget, never the caller's request context: a client
// disconnect is exactly what cancels that one.
//
// Every settlement call takes its own rather than sharing one across the
// sequence. settleStream used to build a single context and reuse it for
// finalize and for the fallback release that follows a failed finalize, so a
// finalize that was merely SLOW rather than instantly failing consumed the
// whole window and the release then ran on an already-expired context, never
// left the gateway, and stranded the hold: #616's failure mode reintroduced
// on the highest-traffic path (#657). The same starvation applied to the
// usage events recorded after settlement, which would silently vanish.
//
// PR #650 established this shape for the audio and images settlement paths;
// this is the same fix on the streaming path.
//
// The cost is that the worst case settlement window is now the sum of each
// call's budget rather than one shared budget, so it roughly doubles on the
// finalize-then-release path. That is the correct trade against stranding a
// customer's credits, and it widens #649 for streaming in the same way #650
// widened it for media.
func freshSettlementCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), accountingTimeout)
}

func (o *Orchestrator) recordInterruptedEvent(ctx context.Context, snapshot authz.AuthSnapshot, attempt AttemptResult, requestID, endpoint, model string, acc *UsageAccumulator, eventType string) {
	input := RecordEventInput{
		AccountID:        snapshot.AccountID,
		RequestAttemptID: attempt.ID,
		APIKeyID:         snapshot.KeyID,
		RequestID:        requestID,
		EventType:        eventType,
		Endpoint:         endpoint,
		ModelAlias:       model,
		Status:           eventType,
	}
	if acc != nil && acc.HasUsage {
		input.InputTokens = acc.InputTokens
		input.OutputTokens = acc.OutputTokens
		input.HiveCreditDelta = acc.TotalTokens
		input.CacheReadTokens = acc.CachedTokens
		input.CacheWriteTokens = acc.CacheWriteTokens
	}
	// Do not discard this error: event_type values here (upstream_error,
	// finalize_failed, interrupted) previously were not in the usage_events
	// CHECK constraint (see the accompanying migration), so this insert was
	// failing on every call and vanishing silently. Metering Step 2 shadow
	// mode is recording exactly this data for Step 4's future enforcement
	// thresholds, so a silent drop here biases that decision.
	if err := o.accounting.RecordUsageEvent(ctx, input); err != nil {
		log.Printf("inference: record interrupted event failed request_id=%s event_type=%s: %v", requestID, eventType, err)
	}
}
