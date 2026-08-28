package inference

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// Orchestrator coordinates the inference request lifecycle.
type Orchestrator struct {
	authorizer *authz.Authorizer
	routing    *RoutingClient
	accounting *AccountingClient
	litellm    *LiteLLMClient

	// metrics is optional; a nil value records nothing. See StageMetrics.
	metrics *StageMetrics
}

// WithStageMetrics attaches per-stage timing to this Orchestrator and returns
// it, mirroring accounting.Service.WithAccountLocker rather than widening
// NewOrchestrator's signature, so the many call sites that do not want metrics
// (every test) are untouched.
func (o *Orchestrator) WithStageMetrics(m *StageMetrics) *Orchestrator {
	o.metrics = m
	return o
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(authorizer *authz.Authorizer, routing *RoutingClient, accounting *AccountingClient, litellm *LiteLLMClient) *Orchestrator {
	return &Orchestrator{
		authorizer: authorizer,
		routing:    routing,
		accounting: accounting,
		litellm:    litellm,
	}
}

// dispatchFunc dispatches a request to LiteLLM and returns the raw response.
type dispatchFunc func(ctx context.Context, litellmModel string, body []byte) (*http.Response, error)

// selectRoute resolves a route for an API-key-authenticated request. It binds
// snapshot.TenantID (D-030: resolved by control-plane's
// apikeys.Service.ResolveSnapshot from public.tenant_billing_accounts) onto
// ctx before delegating to the routing client, so the tenant-scoped
// entitlement check inside routing.Service.SelectRoute -- previously
// unreachable for API-key traffic, since RoutingClient.SelectRoute only ever
// read auth.TenantID(ctx), which JWT middleware populates but the API-key
// path never touches -- runs the same way it already does for JWT sessions.
//
// A key whose account has no resolvable tenant fails closed (returns
// ErrAccountNotProvisioned before ever calling the routing client) rather
// than falling back to the pre-D-030 unfiltered behavior: without a tenant
// the entitlement check cannot run at all, so admitting the request would
// silently reopen the exact gap this exists to close. All three inference
// entry points (executeSync, executeStreaming, executeResponsesStreaming)
// route through here rather than calling o.routing.SelectRoute directly, so
// tenant binding and the fail-closed check happen exactly once.
func (o *Orchestrator) selectRoute(ctx context.Context, snapshot authz.AuthSnapshot, input SelectRouteInput) (SelectRouteResult, error) {
	// Same predicate the /v1/models path and the OWUI shim-key probe use, by
	// construction rather than by convention (issue #717).
	tenantID, err := snapshot.TenantUUID()
	if err != nil {
		return SelectRouteResult{}, ErrAccountNotProvisioned
	}
	return o.routing.SelectRoute(withAPIKeyTenant(ctx, tenantID), input)
}

// normalizeFunc normalizes a LiteLLM response: strips provider fields, extracts usage.
type normalizeFunc func(respBody []byte, aliasID string) ([]byte, *UsageResponse, error)

// executeSync runs the full non-streaming inference lifecycle:
// authorize -> route -> attempt -> reserve -> dispatch -> normalize -> finalize -> respond.
func (o *Orchestrator) executeSync(
	ctx context.Context,
	w http.ResponseWriter,
	r *http.Request,
	endpoint string,
	body []byte,
	model string,
	needFlags NeedFlags,
	estimatedCredits int64,
	dispatch dispatchFunc,
	normalize normalizeFunc,
) {
	// Deferred, unlike every other stage below: this one spans the whole
	// function, and executeSync returns early on authorization, routing,
	// reservation, upstream and normalization failures. Recording it at the
	// bottom instead would mean the total only ever counts requests that
	// succeeded, which is a metric that cannot go red on exactly the outcomes
	// worth alerting on.
	defer o.stage(endpoint, StageTotal)()

	// 1. Authorize
	authHeader := r.Header.Get("Authorization")
	endAuthorize := o.stage(endpoint, StageAuthorize)
	snapshot, headers, authErr := o.authorizer.Authorize(ctx, authHeader, model, estimatedCredits, 0, 0)
	endAuthorize()
	if authErr != nil {
		// apierrors.WriteAuthFailure is the single source of truth for
		// mapping an authz failure to a wire response (rate-limit 429,
		// quota 429, model-not-found 404, upstream-unavailable 503,
		// default 401); duplicating that switch here let it drift out of
		// sync (it never gained the upstream_unavailable case, so a cold
		// control-plane container fell through to 401 here even after
		// Authorizer started returning 503 -- fixed by routing through the
		// shared helper instead of a second copy of its logic).
		apierrors.WriteAuthFailure(w, authErr, headers)
		return
	}

	// 2. Select route
	endSelectRoute := o.stage(endpoint, StageSelectRoute)
	route, err := o.selectRoute(ctx, snapshot, SelectRouteInput{
		AliasID:             model,
		NeedChatCompletions: needFlags.NeedChatCompletions,
		NeedResponses:       needFlags.NeedResponses,
		NeedEmbeddings:      needFlags.NeedEmbeddings,
		NeedStreaming:       needFlags.NeedStreaming,
		NeedReasoning:       needFlags.NeedReasoning,
		RequireToolCapable:  needFlags.RequireToolCapable,
	})
	endSelectRoute()
	if err != nil {
		if errors.Is(err, ErrAccountNotProvisioned) {
			writeAccountNotProvisionedError(w)
			return
		}
		if errors.Is(err, ErrModelNotEntitled) {
			writeModelNotEntitledError(w, model)
			return
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "not found") {
			writeModelNotFoundError(w, model)
			return
		}
		if strings.Contains(errMsg, "no eligible") || strings.Contains(errMsg, "capability") {
			code := "capability_mismatch"
			apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("No route supports the requested capabilities for model '%s'.", model), &code)
			return
		}
		code := "routing_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error",
			"Failed to select a route for this request.", &code)
		return
	}

	// 2b. Refuse an alias this endpoint cannot price, before any hold is taken
	// and before a provider is ever reached (#688, D-034).
	if !requireTokenPricing(w, route, endpoint, model) {
		return
	}

	// 2c. Read the caller's own completion ceiling BEFORE any outbound rewrite
	// (issue #1283). EnforceVariablePriceBounds below forces a ceiling Hive
	// chose rather than one the caller asked for, so reading it afterwards
	// would bound the charge by our own number and guarantee the caller
	// nothing. 0 means they set none.
	ceiling := requestedCompletionCeiling(endpoint, body)

	// Then send the provider that same number. A body carrying two
	// contradictory ceilings used to be forwarded verbatim while settlement
	// held it to the smaller one, which let a caller pair max_tokens 1 with
	// max_completion_tokens 100000 and buy a full-size generation for the price
	// of one completion token. See pinCompletionCeiling; it only ever narrows.
	body = pinCompletionCeiling(body, endpoint, ceiling)

	// 2d. Bound the request for a variable-price alias, before dispatch. Its
	// hold is only provably sufficient below a known request size and a known
	// completion ceiling; see EnforceVariablePriceBounds. A pass-through for
	// every fixed-price alias.
	boundedBody, withinBounds := EnforceVariablePriceBounds(w, route, endpoint, model, body)
	if !withinBounds {
		return
	}
	body = boundedBody

	// 3. Start attempt
	requestID := uuid.New().String()
	endStartAttempt := o.stage(endpoint, StageStartAttempt)
	attempt, err := o.accounting.StartAttempt(ctx, StartAttemptInput{
		AccountID:     snapshot.AccountID,
		RequestID:     requestID,
		AttemptNumber: 1,
		Endpoint:      endpoint,
		ModelAlias:    model,
		Status:        "dispatching",
		APIKeyID:      snapshot.KeyID,
	})
	endStartAttempt()
	if err != nil {
		log.Printf("inference: start attempt failed (non-fatal): %v", err)
	}

	// 4. Create reservation
	endCreateReservation := o.stage(endpoint, StageCreateReservation)
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
	endCreateReservation()
	if err != nil && refuseOnReservationFailure(w, endpoint, model, err) {
		return
	}

	// Ensure reservation cleanup if we return without finalizing. Every exit
	// path below settles through releaseReservationBackground, the same helper
	// the streaming path uses: a fresh bounded background context, never the
	// request context (a client disconnect is exactly what cancels that), and
	// `finalized` gated on the release actually reaching the control plane
	// rather than assumed. A failed attempt leaves `finalized` false so this
	// defer gets one more shot before the request returns, which is what keeps
	// a hold from being stranded (issue #616).
	finalized := false
	releaseReason := "interrupted"
	defer func() {
		if !finalized && reservation.ID != "" {
			o.releaseReservationBackground(snapshot, reservation.ID, requestID, releaseReason)
		}
	}()

	// 5. Dispatch to LiteLLM with bounded retry on 429/5xx.
	endDispatch := o.stage(endpoint, StageDispatch)
	resp, err := dispatchWithRetry(ctx, route.LiteLLMModelName, body, dispatch)
	endDispatch()
	if err != nil {
		if reservation.ID != "" {
			releaseReason = "upstream_error"
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, releaseReason)
		}
		apierrors.WriteProviderBlindUpstreamError(w, model, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		upstreamBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if reservation.ID != "" {
			releaseReason = "upstream_error"
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, releaseReason)
		}
		o.recordErrorEvent(ctx, snapshot, attempt, requestID, endpoint, model, resp.StatusCode, string(upstreamBody))
		apierrors.WriteProviderBlindUpstreamError(w, model, resp.StatusCode, string(upstreamBody))
		return
	}

	// 6. Read response body
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		if reservation.ID != "" {
			releaseReason = "read_error"
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, releaseReason)
		}
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to read upstream response.", &code)
		return
	}

	// 7. Normalize
	normalized, usage, err := normalize(respBody, model)
	if err != nil {
		if reservation.ID != "" {
			releaseReason = "normalize_error"
			finalized = o.releaseReservationBackground(snapshot, reservation.ID, requestID, releaseReason)
		}
		code := "upstream_error"
		apierrors.WriteError(w, http.StatusBadGateway, "api_error", "Failed to process upstream response.", &code)
		return
	}

	// 7b. Zero-content guard (issue #1171). A chat completion whose every
	// choice finished with finish_reason=length and no visible output is the
	// reasoning-burn signature: a member spent the whole ceiling on hidden
	// reasoning. Retry once against the same pool; if the retry is empty too,
	// or fails, keep the original response and settle fail-closed below.
	zeroContentCaptured := false
	if endpoint == EndpointChatCompletions && route.ReasoningReserveTokens > 0 && isEmptyLengthCompletion(normalized) {
		log.Printf("inference: zero-content length completion on a reserving pool, retrying once request_id=%s alias=%s", requestID, model)
		retried, rerr := dispatch(ctx, route.LiteLLMModelName, body)
		if rerr != nil {
			zeroContentCaptured = true
			log.Printf("inference: zero-content retry transport error request_id=%s alias=%s: %v", requestID, model, rerr)
		} else {
			defer retried.Body.Close()
			if retried.StatusCode < 200 || retried.StatusCode >= 300 {
				drainAndClose(retried)
				zeroContentCaptured = true
				log.Printf("inference: zero-content retry answered %d request_id=%s alias=%s", retried.StatusCode, requestID, model)
			} else {
				retryBody, rerr2 := io.ReadAll(io.LimitReader(retried.Body, 10*1024*1024))
				if rerr2 != nil {
					zeroContentCaptured = true
					log.Printf("inference: zero-content retry body read failed request_id=%s alias=%s: %v", requestID, model, rerr2)
				} else {
					rn, ru, nerr := normalize(retryBody, model)
					switch {
					case nerr != nil:
						zeroContentCaptured = true
						log.Printf("inference: zero-content retry normalize failed request_id=%s alias=%s: %v", requestID, model, nerr)
					case isEmptyLengthCompletion(rn):
						zeroContentCaptured = true
					default:
						respBody, normalized, usage = retryBody, rn, ru
					}
				}
			}
		}
		if zeroContentCaptured {
			log.Printf("inference: zero_content_captured request_id=%s reservation_id=%s endpoint=%s alias=%s: empty visible content after retry, capturing hold instead of settling full price",
				requestID, reservation.ID, endpoint, model)
			zeroContentCaptureTrips.WithLabelValues(model, endpoint).Inc()
		}
	}

	// 7c. Cap the metered completion count at the caller's own ceiling (issue
	// #1283). This is the settlement half of the invariant: a request that
	// specifies max_tokens: N is never billed for more than N completion
	// tokens, whatever the provider reported. It clamps the ONE usage object
	// both the customer response and the charge are derived from, and rewrites
	// the already-marshalled body to match, so the number the caller reads and
	// the number they pay for cannot diverge. A no-op when no ceiling was set
	// or the response stayed inside it, which is the overwhelming majority.
	//
	// After 7b, not before: the retry above replaces normalized and usage
	// wholesale, so a clamp applied first would be discarded on exactly the
	// path the reserve was built for.
	if clampUsageToCeiling(usage, route, ceiling, endpoint, model) {
		normalized = rewriteNormalizedUsage(normalized, endpoint, usage)
	}

	// 8. Finalize reservation and record usage
	if reservation.ID != "" {
		// What the provider actually reported, or an estimate from the bytes
		// actually exchanged when it reported nothing -- never the flat
		// reservation estimate (issue #636). estimatedCredits is a hold size,
		// an authorization floor picked before the request ran; billing it as
		// though it were a measurement charged a three-token reply 10,000
		// pre-rescale credits, and did so under TerminalUsageConfirmed = true, which tells
		// control-plane the figure is a fact and so skips both the hold clamp
		// and the reconciliation job that exist to correct estimates. Same
		// settlementCredits helper the streaming path uses, so the two paths
		// agree on what an absent usage block means and price the result the
		// same way: the alias's catalog row, never the raw token count (#688).
		//
		// The prompt and response text are extracted only when there is no
		// usable token count to price: the normal path pays for neither parse.
		hasUsage := usage != nil
		var inputTokens, outputTokens int64
		cache := CacheUsage{}
		if hasUsage {
			inputTokens, outputTokens = usage.PromptTokens, usage.CompletionTokens
			cache = NormalizeCacheUsage(usage, model, route.Provider)
		}
		var prompt, content string
		if inputTokens+outputTokens <= 0 {
			prompt, content = promptText(endpoint, body), responseText(endpoint, normalized)
		}
		var actualCredits int64
		var confirmed, billable bool
		if route.Pricing.IsUpstreamActual() {
			// respBody, not `normalized`: normalize re-marshals a typed struct
			// and drops every field it does not declare, the upstream's
			// reported cost among them. The raw bytes are the only place that
			// figure still exists.
			settled := UpstreamActualSettlement(
				respBody, reservation.Held(),
				hasUsage, inputTokens, outputTokens, responseText(endpoint, normalized))
			actualCredits, confirmed, billable = settled.Credits, settled.Confirmed, settled.Delivered
			if billable {
				// generation_id is the audit handle for this charge; see the
				// same line on the streaming path.
				log.Printf("inference: variable-price settlement request_id=%s reservation_id=%s endpoint=%s model=%s reason=%s credits=%d confirmed=%v generation_id=%s held_credits=%d",
					requestID, reservation.ID, endpoint, model, settled.Reason,
					settled.Credits, settled.Confirmed, settled.GenerationID, reservation.Held())
			}
		} else {
			actualCredits, confirmed, billable = settlementCredits(route, hasUsage,
				cache.FreshInputTokens, cache.CacheReadTokens, cache.CacheWriteTokens, outputTokens, prompt, content, ceiling)
		}

		// Zero-content capture (issue #1171): a completion that returned no
		// visible output even after its one retry must never settle as an
		// ordinary full-price success. Capture the hold instead: charge at the
		// hold's size with terminal_usage_confirmed=false, so control-plane's
		// finalize clamp keeps it inside what was authorized and
		// reconciliation still sees it. The upstream did consume real tokens
		// (hidden reasoning is real inference we paid for), which is why this
		// captures rather than releases; what it may never do is bill the
		// alias's service price for content that does not exist.
		//
		// The capture is bounded by the caller's own ceiling (#1283). The hold
		// is a flat authorization floor, so capturing it whole against a
		// request capped at a handful of completion tokens breaches the same
		// invariant by a far wider margin than the overrun that reported it.
		// capCaptureAtCeiling only ever lowers the figure and never to zero, so
		// the fail-closed property this branch exists for is untouched.
		if zeroContentCaptured {
			actualCredits = capCaptureAtCeiling(route, ceiling,
				cache.FreshInputTokens, cache.CacheReadTokens, cache.CacheWriteTokens, reservation.Held())
			confirmed = false
			billable = true
		}

		if !billable {
			// Nothing measured and nothing produced: there is no quantity to
			// charge, so leave finalized false and let the deferred release
			// hand the hold back in full. That keeps the single-terminal-state
			// invariant intact -- one release site, its own fresh background
			// context -- rather than adding a second settlement call here.
			releaseReason = "unmeasured_usage"
			log.Printf("inference: settling unconfirmed with nothing billable, releasing hold request_id=%s reservation_id=%s endpoint=%s: upstream returned no usage and no output",
				requestID, reservation.ID, endpoint)
		} else {
			if !confirmed {
				log.Printf("inference: settling unconfirmed usage estimate request_id=%s reservation_id=%s endpoint=%s estimated_credits=%d: upstream returned no usable usage block",
					requestID, reservation.ID, endpoint, actualCredits)
			}
			// Do not discard this error. A failed finalize used to set finalized
			// anyway, which skipped the deferred release and so lost the charge
			// and stranded the hold in the same step (issue #616). Leaving
			// finalized false hands the reservation to the deferred release
			// instead, so it still reaches a terminal state exactly once:
			// charged here on success, released there on failure, never both.
			//
			// finalizeCtx, not ctx (#637): by this point the client can already
			// have disconnected, cancelling ctx. Finalizing on ctx then fails
			// not because the ledger rejected the charge but because the HTTP
			// call itself aborts on the cancelled context, which fell through
			// to the releaseReason = "finalize_failed" branch below and
			// released a hold for work that was genuinely delivered --
			// converting a chargeable request into a free one for any client
			// that disconnects at the right moment. Same fresh background
			// context + accountingTimeout as releaseReservationBackground and
			// settleStream (PR #602's pattern).
			finalizeCtx, cancel := context.WithTimeout(context.Background(), accountingTimeout)
			endFinalize := o.stage(endpoint, StageFinalizeReservation)
			err := o.accounting.FinalizeReservation(finalizeCtx, FinalizeReservationInput{
				AccountID:     snapshot.AccountID,
				ReservationID: reservation.ID,
				ActualCredits: actualCredits,
				// settlementCredits' own verdict, never a bare true (issue
				// #636): this flag is control-plane's instruction to treat the
				// figure as measured truth, bill it in full even past the hold,
				// and skip reconciliation entirely. Only a provider usage block
				// carrying real token counts earns it.
				TerminalUsageConfirmed: confirmed,
				Status:                 "completed",
				// Forward every metered count the settlement priced from
				// (#856, #1174): control-plane writes them onto the completed
				// usage event and the api_key_usage_rollups row, so both
				// surfaces carry real figures instead of zeroes. The sync path
				// never sent even input/output before, so the rollup stayed
				// zero across all four token columns on this route.
				InputTokens:      inputTokens,
				OutputTokens:     outputTokens,
				CacheReadTokens:  cache.CacheReadTokens,
				CacheWriteTokens: cache.CacheWriteTokens,
			})
			endFinalize()
			cancel()
			if err != nil {
				log.Printf("inference: finalize reservation failed, releasing hold instead request_id=%s reservation_id=%s: %v", requestID, reservation.ID, err)
				releaseReason = "finalize_failed"
			} else {
				finalized = true
			}
		}
	}

	// The gap this measures is the sharpest evidence the 2026-08-16 latency
	// investigation produced: the client's body arrived 190 ms after the last
	// ledger row was written, three times running, which is what showed the
	// wait was Hive's own accounting rather than the agent or the provider.
	// Keeping it as a metric is what makes a regression in that gap visible
	// without repeating the investigation.
	endRecordUsage := o.stage(endpoint, StageRecordUsage)
	o.recordCompletedEvent(ctx, snapshot, attempt, requestID, endpoint, model, usage)
	endRecordUsage()

	// 9. Write response
	endResponseWrite := o.stage(endpoint, StageResponseWrite)
	w.Header().Set("Content-Type", "application/json")
	if zeroContentCaptured {
		// The honest flag promised in the guard's contract: the caller gets
		// the upstream body (finish_reason=length already tells an SDK why
		// content is empty) plus this header so no client is left guessing.
		w.Header().Set(emptyContentHeader, emptyContentHeaderValue)
	}
	w.WriteHeader(http.StatusOK)
	w.Write(normalized)
	endResponseWrite()
}

func (o *Orchestrator) recordErrorEvent(ctx context.Context, snapshot authz.AuthSnapshot, attempt AttemptResult, requestID, endpoint, model string, statusCode int, errBody string) {
	// Logged, not discarded: usage recording is best effort for the request
	// itself, but a silent drop is how the streaming usage_events rows vanished
	// unnoticed against an outdated CHECK constraint. Metering Step 2 shadow
	// mode reads exactly this data.
	if err := o.accounting.RecordUsageEvent(ctx, RecordEventInput{
		AccountID:        snapshot.AccountID,
		RequestAttemptID: attempt.ID,
		APIKeyID:         snapshot.KeyID,
		RequestID:        requestID,
		EventType:        "error",
		Endpoint:         endpoint,
		ModelAlias:       model,
		Status:           fmt.Sprintf("upstream_%d", statusCode),
		ErrorCode:        fmt.Sprintf("%d", statusCode),
		ErrorType:        "upstream_error",
	}); err != nil {
		log.Printf("inference: record error event failed request_id=%s status=%d: %v", requestID, statusCode, err)
	}
}

func (o *Orchestrator) recordCompletedEvent(ctx context.Context, snapshot authz.AuthSnapshot, attempt AttemptResult, requestID, endpoint, model string, usage *UsageResponse) {
	input := RecordEventInput{
		AccountID:        snapshot.AccountID,
		RequestAttemptID: attempt.ID,
		APIKeyID:         snapshot.KeyID,
		RequestID:        requestID,
		EventType:        "completed",
		Endpoint:         endpoint,
		ModelAlias:       model,
		Status:           "completed",
	}
	if usage != nil {
		input.InputTokens = usage.PromptTokens
		input.OutputTokens = usage.CompletionTokens
		cache := NormalizeCacheUsage(usage, model, "")
		input.CacheReadTokens = cache.CacheReadTokens
		input.CacheWriteTokens = cache.CacheWriteTokens
		input.HiveCreditDelta = usage.TotalTokens
	}
	// Logged, not discarded, for the same reason as recordErrorEvent above.
	if err := o.accounting.RecordUsageEvent(ctx, input); err != nil {
		log.Printf("inference: record completed event failed request_id=%s: %v", requestID, err)
	}
}
