// Package sessionbilling is the money path for JWT-session inference.
//
// It exists because the reservation lifecycle in inference.Orchestrator is
// bound to an API-key principal: Orchestrator.Authorize resolves an "hk_..."
// key out of the Authorization header, and a Supabase JWT is not one. Every
// session-authenticated inference route therefore had to either reimplement
// the hold-and-settle lifecycle or skip it, and skipping it is what #669
// records: RAG chat and agent tasks served inference without ever asking
// whether the account could pay, so an insufficient-credit refusal could not
// fire on those surfaces at all.
//
// The lifecycle here is not new. It was written for session chat (#746,
// apps/edge-api/internal/chat/billing.go) and is moved here verbatim so there
// is ONE mechanism with two callers rather than two that drift. The rules it
// enforces, all of them before a provider is ever reached:
//
//  1. A tenant with no billing account is refused, never served free (#721
//     tracks the provisioning gap that makes this reachable).
//  2. An alias whose catalog row cannot be priced in tokens is refused (D-034),
//     rather than served as an unpriced request.
//  3. A credit hold is taken before dispatch, so an account with no credits is
//     refused by the reservation itself.
//  4. Every reservation reaches a terminal state exactly once, finalized at the
//     served route's catalog price or released in full, never both and never
//     neither.
//
// The one exception is deployment posture: an ENTERPRISE_EDGE tenant has no
// prepaid relationship with Hive, so it is explicitly not billable (D-027) and
// takes no hold. That is a recorded non-billable verdict, not silence.
package sessionbilling

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
)

// settlementTimeout bounds one control-plane accounting call: the hold taken
// before dispatch, and the single terminal call made after the response has
// already been sent. Unexported and constant on purpose. It was briefly an
// exported var "so tests can shrink it" and no test ever did, which is the
// weakest available shape: exported mutable package state that any caller can
// reassign under every other caller.
const settlementTimeout = 30 * time.Second

// Resolver answers what a session principal's tenant settles against: the
// billing account, and the deployment posture that decides whether it settles
// at all. metering.PGBillingAccountResolver is the production implementation;
// it reads both in one indexed lookup off the pool edge-api already holds open.
type Resolver interface {
	ResolveState(ctx context.Context, tenantID uuid.UUID) (metering.TenantBillingState, error)
}

// Settlement is one request's live accounting state, from the hold taken
// before dispatch to the single terminal call that ends it.
type Settlement struct {
	accounting    *inference.AccountingClient
	accountID     string
	reservationID string
	requestID     string
	surface       string
	// heldCredits is the size of the hold actually taken. Recorded because a
	// variable-price alias settles at the hold when it cannot read an upstream
	// cost, and it can no longer assume that figure is the endpoint floor.
	heldCredits int64
}

// Held returns the hold size, or zero when there is no settlement at all (the
// Enterprise posture, which charges nothing).
func (s *Settlement) Held() int64 {
	if s == nil {
		return 0
	}
	return s.heldCredits
}

// Input is everything Start needs to take a hold for one session request.
type Input struct {
	// Accounting and Billing are the money path. Without both, Start refuses
	// every request rather than serving inference it cannot charge for.
	Accounting *inference.AccountingClient
	Billing    Resolver
	TenantID   uuid.UUID
	// Route must be the route this request will actually dispatch to: its
	// pricing is what both the hold and the later charge are derived from.
	Route inference.SelectRouteResult
	// Alias is the catalog alias the caller asked for, used for the charge and
	// for operator logs. Never a route or provider name in a customer response.
	Alias string
	// Endpoint is an inference.Endpoint* constant, recorded on the attempt and
	// the reservation.
	Endpoint  string
	RequestID uuid.UUID
	// Body must be the BOUNDED body that will go upstream, after
	// inference.EnforceVariablePriceBounds. Sizing a hold from an unbounded
	// body is a number with nothing behind it (issue #1372).
	Body []byte
	// HoldFloor is the flat pre-dispatch hold for a fixed-price alias, raised
	// per request for a variable-price one by inference.ReservationCredits.
	HoldFloor int64
	// Surface labels operator logs ("session chat", "rag chat"). Never
	// customer-visible.
	Surface string
}

// Refusal is a money-path verdict that a request must not be served, kept
// separate from the rendering of it.
//
// Start used to write the customer's refusal itself, which hard-bound the
// reservation lifecycle to an HTTP handler holding a ResponseWriter. That is
// why the agent-task submit gate could not reuse this lifecycle at all and had
// to be written separately. The verdict is now a value the caller acts on:
// an HTTP handler calls Write, anything else reads Reason.
type Refusal struct {
	// Reason is an operator-facing token, never customer-visible.
	Reason string
	write  func(http.ResponseWriter)
}

// Write renders the customer-facing refusal. Provider-blind, and it never
// names a route, an amount or a balance.
func (r *Refusal) Write(w http.ResponseWriter) {
	if r == nil || r.write == nil {
		return
	}
	r.write(w)
}

// Start takes the hold for a session request, or writes the customer's refusal
// and reports that the request must not be dispatched. It is Reserve plus the
// rendering, for the two HTTP inference handlers.
//
// A nil Settlement with refused=false is the Enterprise posture: nothing to
// charge, so the request proceeds unheld and unbilled by decision rather than
// by omission.
func Start(ctx context.Context, w http.ResponseWriter, in Input) (*Settlement, bool) {
	settle, refusal := Reserve(ctx, in)
	if refusal != nil {
		refusal.Write(w)
		return nil, true
	}
	return settle, false
}

// Reserve is Start's verdict half: it takes the hold, or returns the refusal
// the caller must render. Every rule it enforces runs before a provider is
// reached.
func Reserve(ctx context.Context, in Input) (*Settlement, *Refusal) {
	return reserve(ctx, in, true)
}

// ProbeInput is everything Probe needs to answer the solvency question for a
// unit of work that is not an inference request.
type ProbeInput struct {
	Accounting *inference.AccountingClient
	Billing    Resolver
	TenantID   uuid.UUID
	// Endpoint is recorded on the attempt and the reservation, e.g.
	// inference.EndpointAgentTasks.
	Endpoint string
	// Label is what the reservation records where an inference request records
	// its model alias. Never customer-visible.
	Label     string
	RequestID uuid.UUID
	// HoldCredits is the size of the launch floor this work must be able to
	// cover. It is an authorization floor, never a charge, and it is handed
	// straight back.
	HoldCredits int64
	Surface     string
}

// Probe answers one question: can this tenant pay for the unit of work about
// to be launched? It takes the same hold an inference request would, reads the
// control-plane's verdict, and hands the hold straight back.
//
// It exists because a sandbox launch has no alias, no token count and no
// settlement point inside edge-api: control-plane owns the task lifecycle, so
// a hold taken here would have nothing to finalize it and would strand
// permanently (there is no expires_at and no reaper, #600). A probe that
// releases in the same call cannot strand, and it still refuses a tenant who
// cannot pay, which is the gap #669 records on this path.
//
// It does NOT meter what the task then spends. The agent's own inference is
// billed where it is dispatched; this is the solvency gate in front of the
// launch, and nothing more.
func Probe(ctx context.Context, in ProbeInput) *Refusal {
	settle, refusal := reserve(ctx, Input{
		Accounting: in.Accounting,
		Billing:    in.Billing,
		TenantID:   in.TenantID,
		Endpoint:   in.Endpoint,
		Alias:      in.Label,
		RequestID:  in.RequestID,
		HoldFloor:  in.HoldCredits,
		Surface:    in.Surface,
	}, false)
	if refusal != nil {
		return refusal
	}
	if settle == nil {
		// Enterprise posture (D-027): no prepaid relationship, so there is
		// nothing to be solvent against and the launch proceeds.
		return nil
	}
	settle.Release("solvency_probe")
	return nil
}

// reserve is the one reservation lifecycle both entry points run.
//
// requirePricedRoute is what separates them. An inference request must have a
// route whose catalog row can price the charge, or it is refused rather than
// served unpriced (D-034). A solvency probe has no route and no charge to
// derive, so that check would refuse every caller. The flag is unexported and
// both exported entry points set it explicitly, so a future caller cannot skip
// the check by leaving a field at its zero value.
func reserve(ctx context.Context, in Input, requirePricedRoute bool) (*Settlement, *Refusal) {
	if in.Endpoint == "" || in.Alias == "" {
		// Neither is optional: both are recorded on the attempt and the
		// reservation, and control-plane rejects an empty model_alias outright.
		// An empty one here is a wiring mistake, and the first place anyone
		// would otherwise notice is a control-plane row.
		slog.Error(in.Surface+" money path called without an endpoint or label",
			"request_id", in.RequestID, "endpoint", in.Endpoint, "alias", in.Alias)
		return nil, &Refusal{Reason: "not_wired", write: WriteBillingUnavailable}
	}
	if in.Accounting == nil || in.Billing == nil {
		// A gateway that cannot charge must not serve. Reaching here means
		// this handler was constructed without its accounting seam, which is
		// the #746 failure mode itself, so it refuses rather than repeat it.
		slog.Error(in.Surface+" accounting not wired, refusing request",
			"request_id", in.RequestID, "alias", in.Alias)
		return nil, &Refusal{Reason: "not_wired", write: WriteBillingUnavailable}
	}

	state, err := in.Billing.ResolveState(ctx, in.TenantID)
	if err != nil {
		// The billing position is unknown, not known-absent. Serving anyway is
		// how free traffic happens, so this refuses and asks for a retry.
		slog.Error(in.Surface+" billing lookup failed", "err", err, "request_id", in.RequestID)
		return nil, &Refusal{Reason: "billing_lookup_failed", write: WriteBillingUnavailable}
	}
	// The posture check comes before the pricing check on purpose: a tenant
	// nobody bills has no charge to fail closed on, so an Enterprise box
	// running a locally hosted model that carries no catalog price keeps
	// serving instead of being refused for a price it never needed.
	if state.Deployment == metering.DeploymentEnterpriseEdge {
		return nil, nil
	}
	// An alias that cannot be priced in tokens has no charge this endpoint can
	// derive. Inventing a rate and serving it free are both worse than
	// refusing, so this fails closed (D-034).
	if requirePricedRoute && !inference.CanPriceTokens(in.Route) {
		slog.Warn(in.Surface+" refused, alias not priced in tokens",
			"request_id", in.RequestID, "alias", in.Alias, "price_unit", in.Route.PriceUnit)
		return nil, &Refusal{Reason: "unpriceable_model", write: WriteUnpriceableModel}
	}
	if !state.Found {
		slog.Warn(in.Surface+" refused, tenant has no billing account",
			"request_id", in.RequestID, "tenant_id", in.TenantID, "alias", in.Alias)
		return nil, &Refusal{Reason: "no_billing_account", write: WriteBillingNotConfigured}
	}

	accountID := state.AccountID.String()
	// Attempt creation is telemetry, so a failure here is logged and the
	// request continues: the charge below does not depend on it.
	if _, err := in.Accounting.StartAttempt(ctx, inference.StartAttemptInput{
		AccountID:     accountID,
		RequestID:     in.RequestID.String(),
		AttemptNumber: 1,
		Endpoint:      in.Endpoint,
		ModelAlias:    in.Alias,
		Status:        "dispatching",
	}); err != nil {
		slog.Warn(in.Surface+" start attempt failed", "err", err, "request_id", in.RequestID)
	}

	// A variable-price alias raises the flat hold: a router can resolve to a
	// model far dearer than the flat figure assumes, and the hold is the only
	// solvency gate in front of it. How far it raises it is sized from this
	// request's own bounded body rather than from the largest request the
	// bounds allow, which is what made the alias unusable below 2.00 USD of
	// credit (issue #1372).
	held := inference.ReservationCredits(in.Route, in.HoldFloor, in.Endpoint, in.Body)
	// The hold is taken on a context the CLIENT cannot cancel. Cancelling
	// midway is the one failure this call must not have: control-plane can
	// commit the reservation row and then lose the answer, which returns an
	// error here, refuses, and returns before the caller has installed its
	// deferred release. Nothing else ever releases it (no expires_at, no
	// reaper, #600), so the customer's credits are locked until someone
	// intervenes by hand, which is the stranded-hold family behind the 409
	// cascade in #626. A refusal written to a ResponseWriter nobody is reading
	// is harmless by comparison. The timeout is what bounds it instead.
	holdCtx, cancelHold := context.WithTimeout(context.WithoutCancel(ctx), settlementTimeout)
	defer cancelHold()
	reservation, err := in.Accounting.CreateReservation(holdCtx, inference.CreateReservationInput{
		AccountID:        accountID,
		RequestID:        in.RequestID.String(),
		AttemptNumber:    1,
		Endpoint:         in.Endpoint,
		ModelAlias:       in.Alias,
		EstimatedCredits: held,
		PolicyMode:       "strict",
	})
	if err != nil {
		// Every reservation failure refuses on this path, including a
		// control-plane fault. The API-key path rides one out unreserved, but
		// there is nothing to ride out here: route selection is itself a
		// control-plane call one step earlier, so a control-plane outage has
		// already refused the request before this line is reached. Classifying
		// on the returned status, never on message text (D-034).
		var statusErr *inference.StatusError
		if errors.As(err, &statusErr) &&
			(statusErr.StatusCode == http.StatusConflict || statusErr.StatusCode == http.StatusTooManyRequests) {
			// 409 is the control-plane's credit-policy rejection, 429 a rate
			// limit on the reservation call. Both are quota verdicts.
			slog.Info(in.Surface+" refused on quota", "request_id", in.RequestID, "alias", in.Alias)
			// Same machine-readable verdict either way; only the human sentence
			// differs, because "send a shorter request" is not an action a task
			// submission has available.
			write := WriteInsufficientQuota
			if !requirePricedRoute {
				write = WriteInsufficientQuotaForLaunch
			}
			return nil, &Refusal{Reason: "insufficient_quota", write: write}
		}
		slog.Error(in.Surface+" reservation failed", "err", err, "request_id", in.RequestID, "alias", in.Alias)
		return nil, &Refusal{Reason: "reservation_failed", write: WriteReservationUnavailable}
	}
	if reservation.ID == "" {
		// A 2xx with no reservation id is a control-plane contract violation.
		// Nothing could be settled against it, so it fails closed too.
		slog.Error(in.Surface+" reservation returned no id", "request_id", in.RequestID, "alias", in.Alias)
		return nil, &Refusal{Reason: "reservation_without_id", write: WriteReservationUnavailable}
	}

	return &Settlement{
		accounting:    in.Accounting,
		accountID:     accountID,
		reservationID: reservation.ID,
		requestID:     in.RequestID.String(),
		surface:       in.Surface,
		heldCredits:   held,
	}, nil
}

// Finalize charges the reservation and reports whether the charge actually
// landed. A false return leaves the reservation open on purpose, so the
// caller's deferred release still hands the hold back: charged here or
// released there, exactly once either way, never both (#616).
//
// Every settlement call runs on its own fresh background context, never the
// request context: a client disconnect is exactly what cancels that one, and
// settling on it converts a delivered response into a free one.
func (s *Settlement) Finalize(credits int64, confirmed bool, inTokens, outTokens, cacheReadTokens, cacheWriteTokens int64) bool {
	err := s.finalizeOnce(credits, confirmed, inTokens, outTokens, cacheReadTokens, cacheWriteTokens)
	if err == nil {
		return true
	}
	// A transport error or a timeout does not say the charge failed, only that
	// its answer was lost. Treating that as uncharged releases a hold the
	// control-plane has already settled, which the control-plane then rejects,
	// leaving the caller claiming charged=false and cost 0 for a request the
	// ledger did charge. So try once more: FinalizeReservation is idempotent by
	// construction (finalizeLocked replays the stored row for an already
	// finalized or needs_reconciliation reservation instead of charging again,
	// which TestFinalizeReservationTwiceChargesOnce holds in place), so the
	// retry either confirms the settled charge or lands the one that never did.
	// It cannot produce a second charge.
	slog.Warn(s.surface+" finalize failed, retrying once before release",
		"err", err, "request_id", s.requestID, "reservation_id", s.reservationID)
	if retryErr := s.finalizeOnce(credits, confirmed, inTokens, outTokens, cacheReadTokens, cacheWriteTokens); retryErr != nil {
		slog.Error(s.surface+" finalize failed twice, releasing hold instead",
			"err", retryErr, "request_id", s.requestID, "reservation_id", s.reservationID)
		return false
	}
	return true
}

// finalizeOnce is one attempt, on its own fresh context so a retry is never
// handed the timed-out context of the attempt it is retrying. The cache
// components ride along with the input/output counts they split (#1174).
func (s *Settlement) finalizeOnce(credits int64, confirmed bool, inTokens, outTokens, cacheReadTokens, cacheWriteTokens int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), settlementTimeout)
	defer cancel()
	return s.accounting.FinalizeReservation(ctx, inference.FinalizeReservationInput{
		AccountID:     s.accountID,
		ReservationID: s.reservationID,
		ActualCredits: credits,
		// Only a provider usage block carrying real token counts earns this.
		// It tells control-plane the figure is measured truth, so it bills in
		// full past the hold and skips reconciliation.
		TerminalUsageConfirmed: confirmed,
		InputTokens:            inTokens,
		OutputTokens:           outTokens,
		CacheReadTokens:        cacheReadTokens,
		CacheWriteTokens:       cacheWriteTokens,
		Status:                 "completed",
	})
}

// Release hands the whole hold back. The control-plane records the release as
// a usage event of its own, so a refused or failed request leaves an explicit
// zero-charge verdict rather than nothing at all.
func (s *Settlement) Release(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), settlementTimeout)
	defer cancel()
	if err := s.accounting.ReleaseReservation(ctx, inference.ReleaseReservationInput{
		AccountID:     s.accountID,
		ReservationID: s.reservationID,
		Reason:        reason,
	}); err != nil {
		// A stranded hold locks a customer's credits behind a charge that
		// never landed, so this is an error, not a warning.
		slog.Error(s.surface+" release failed, hold may be stranded",
			"err", err, "request_id", s.requestID, "reservation_id", s.reservationID, "reason", reason)
	}
}

// The refusal bodies below all use the OpenAI error shape, the one every chat
// client on these surfaces already parses. None of them names a provider, a
// route, an amount or a balance.

// WriteBillingUnavailable reports that the accounting seam itself is unusable.
func WriteBillingUnavailable(w http.ResponseWriter) {
	code := "billing_unavailable"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error",
		"Usage accounting is temporarily unavailable. Please retry.", &code)
}

// WriteBillingNotConfigured reports a tenant with no billing account.
func WriteBillingNotConfigured(w http.ResponseWriter) {
	code := "billing_not_configured"
	apierrors.WriteError(w, http.StatusForbidden, "invalid_request_error",
		"This workspace is not set up for usage yet. Contact your administrator to complete workspace setup.", &code)
}

// WriteInsufficientQuota is the canonical credit refusal for these surfaces.
//
// The wording says what happened and what the customer can do about it.
// OpenAI's canonical insufficient_quota sentence ("You exceeded your current
// quota, please check your plan and billing details") was wrong twice over on
// this path: the customer has credit, and there is no plan to check. What
// actually happened is that the up-front hold for this request did not fit
// inside the available balance, and a smaller one will (issue #1372).
//
// The machine-readable half, status 429 with type and code insufficient_quota,
// is deliberately unchanged: SDKs branch on those, and this is still a quota
// verdict. Only the human sentence moves.
func WriteInsufficientQuota(w http.ResponseWriter) {
	code := "insufficient_quota"
	apierrors.WriteError(w, http.StatusTooManyRequests, "insufficient_quota",
		"Your available credit does not cover this request. Add credits, or send a shorter request, and try again.", &code)
}

// WriteInsufficientQuotaForLaunch is the credit refusal for a unit of work that
// has no length the customer could shorten, which is what the inference wording
// asks for. Status, type and code are deliberately identical to
// WriteInsufficientQuota, since SDKs branch on those and this is still the same
// quota verdict.
func WriteInsufficientQuotaForLaunch(w http.ResponseWriter) {
	code := "insufficient_quota"
	apierrors.WriteError(w, http.StatusTooManyRequests, "insufficient_quota",
		"Your available credit does not cover this task. Add credits and try again.", &code)
}

// WriteReservationUnavailable reports a reservation failure that is not a
// quota verdict.
func WriteReservationUnavailable(w http.ResponseWriter) {
	code := "reservation_unavailable"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error",
		"Credit reservation is temporarily unavailable. Please retry.", &code)
}

// WriteUnpriceableModel reports an alias with no token price.
//
// That is a stable property of its catalog row, not a passing fault, so this
// is a 4xx: a 503 with type api_error tells every SDK retry layer to send the
// same request again, and it can never succeed. The wording asks for a
// different model rather than a retry, matching the status.
func WriteUnpriceableModel(w http.ResponseWriter) {
	code := "model_not_supported"
	apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
		"The requested model is not available for this endpoint. Choose a supported model.", &code)
}
