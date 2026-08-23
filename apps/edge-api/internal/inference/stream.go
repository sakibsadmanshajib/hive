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
)

// UsageAccumulator tracks token usage and output text across SSE streaming chunks.
// Content is recorded so the usage clamp can recompute completion_tokens when
// the upstream terminal usage chunk reports 0 on a non-empty response.
type UsageAccumulator struct {
	InputTokens     int64
	OutputTokens    int64
	ReasoningTokens int64
	CachedTokens    int64
	TotalTokens     int64
	HasUsage        bool
	Content         strings.Builder
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

// Accumulate copies usage fields from a chunk if present.
func (a *UsageAccumulator) Accumulate(chunk ChatCompletionChunk) {
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
	if chunk.Usage.PromptTokensDetails != nil {
		a.CachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
	}
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

// ToUsageResponse constructs a UsageResponse from accumulated values.
func (a *UsageAccumulator) ToUsageResponse() *UsageResponse {
	u := &UsageResponse{
		PromptTokens:     a.InputTokens,
		CompletionTokens: a.OutputTokens,
		TotalTokens:      a.TotalTokens,
		CompletionTokensDetails: &CompletionTokensDetails{
			ReasoningTokens: a.ReasoningTokens,
		},
		PromptTokensDetails: &PromptTokensDetails{
			CachedTokens: a.CachedTokens,
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

	// 2c. Bound the request for a variable-price alias, before dispatch. Its
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
		finalized = o.settleStream(ctx, snapshot, attempt, reservation, route, requestID, endpoint, model, accumulator, string(body), accumulator.Content.String())
	}()

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
	scanner.Buffer(make([]byte, 64*1024), 512*1024)

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
				// Rewrite model to alias ID
				chunk.Model = aliasID
				// Track output content so the usage clamp has ground truth.
				accumulator.AccumulateContent(chunk)
				// Clamp upstream-zero completion_tokens against the
				// content streamed so far. Usage typically arrives in
				// the terminal chunk once all deltas are flushed.
				if chunk.Usage != nil {
					accumulator.ClampUsage(chunk.Usage, chunk.ID, aliasID, endpoint)
					// Keep the untyped bytes only for a route that settles
					// against the upstream's reported cost; see RawUsageChunk.
					if route.Pricing.IsUpstreamActual() {
						accumulator.RawUsageChunk = append(accumulator.RawUsageChunk[:0], jsonData...)
					}
				}
				// Accumulate usage if present
				accumulator.Accumulate(chunk)
				// Re-marshal sanitized chunk
				sanitized, marshalErr := json.Marshal(chunk)
				if marshalErr == nil {
					fmt.Fprintf(w, "data: %s\n\n", sanitized)
					flusher.Flush()
					continue
				}
			}
			// Fallback: pass through the original line
			fmt.Fprintf(w, "%s\n\n", line)
			flusher.Flush()
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

	// 10. Synthesize terminal usage chunk if requested but upstream didn't send one
	if includeUsage && !accumulator.HasUsage {
		synth := ChatCompletionChunk{
			ID:      "chatcmpl-" + uuid.New().String(),
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
// never turn into a flat, hardcoded overcharge. delivered is false only when
// nothing was produced at all; the caller must release the reservation in full
// rather than charge in that case.
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
// ~2.6M credits against a 10000 credit hold). The control-plane hard clamp in
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
func settlementCredits(route SelectRouteResult, hasUsage bool, inputTokens, outputTokens int64, prompt, content string) (credits int64, confirmed bool, delivered bool) {
	if hasUsage && inputTokens+outputTokens > 0 {
		return CreditsForTokens(route, inputTokens, outputTokens), true, true
	}
	completion := estimateCompletionTokens(content)
	if completion == 0 {
		return 0, false, false
	}
	return CreditsForTokens(route, estimateCompletionTokens(prompt), completion), false, true
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
func (o *Orchestrator) settleStream(reqCtx context.Context, snapshot authz.AuthSnapshot, attempt AttemptResult, reservation ReservationResult, route SelectRouteResult, requestID, endpoint, model string, acc *UsageAccumulator, promptBody, content string) bool {
	// Parse promptBody (the raw request bytes) down to just the message/input
	// text before estimating -- see promptText in usage_clamp.go for why the
	// raw bytes themselves must never be counted directly (issue #602).
	var credits int64
	var confirmed, delivered bool
	if route.Pricing.IsUpstreamActual() {
		// No catalog price exists for this alias, so the charge comes from the
		// cost the upstream reported in its terminal usage chunk. A failed
		// read settles at the hold rather than at zero; see
		// UpstreamActualSettlement.
		var reason string
		credits, confirmed, delivered, reason = UpstreamActualSettlement(
			acc.RawUsageChunk, reservation.EstimatedCredits,
			acc.HasUsage, acc.InputTokens, acc.OutputTokens, content)
		if delivered && !confirmed {
			log.Printf("inference: upstream cost unavailable, settling at the hold request_id=%s reservation_id=%s endpoint=%s model=%s reason=%s held_credits=%d: a variable-price alias could not read a reported cost, charging the hold rather than serving free",
				requestID, reservation.ID, endpoint, model, reason, reservation.EstimatedCredits)
		}
	} else {
		credits, confirmed, delivered = settlementCredits(route, acc.HasUsage, acc.InputTokens, acc.OutputTokens, promptText(endpoint, []byte(promptBody)), content)
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

	// Mirrors the synchronous path's own line in orchestrator.go. The charge
	// still lands, priced from a content estimate rather than measured
	// tokens, and control-plane clamps it at the hold and files a
	// reconciliation job that nothing drains today (issue #925). This line
	// is the only signal that a provider stopped honouring
	// stream_options.include_usage.
	if !confirmed {
		log.Printf("inference: settling unconfirmed usage estimate request_id=%s reservation_id=%s endpoint=%s model=%s estimated_credits=%d: upstream returned no usable usage block",
			requestID, reservation.ID, endpoint, model, credits)
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
