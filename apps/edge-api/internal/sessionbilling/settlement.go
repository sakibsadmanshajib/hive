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

// Timeout bounds one control-plane accounting call made after the response has
// already been streamed. A var so tests can shrink it.
var Timeout = 30 * time.Second

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

// Start takes the hold for a session request, or writes the customer's refusal
// and reports that the request must not be dispatched.
//
// A nil Settlement with refused=false is the Enterprise posture: nothing to
// charge, so the request proceeds unheld and unbilled by decision rather than
// by omission.
func Start(ctx context.Context, w http.ResponseWriter, in Input) (*Settlement, bool) {
	if in.Accounting == nil || in.Billing == nil {
		// A gateway that cannot charge must not serve. Reaching here means
		// this handler was constructed without its accounting seam, which is
		// the #746 failure mode itself, so it refuses rather than repeat it.
		slog.Error(in.Surface+" accounting not wired, refusing request",
			"request_id", in.RequestID, "alias", in.Alias)
		WriteBillingUnavailable(w)
		return nil, true
	}

	state, err := in.Billing.ResolveState(ctx, in.TenantID)
	if err != nil {
		// The billing position is unknown, not known-absent. Serving anyway is
		// how free traffic happens, so this refuses and asks for a retry.
		slog.Error(in.Surface+" billing lookup failed", "err", err, "request_id", in.RequestID)
		WriteBillingUnavailable(w)
		return nil, true
	}
	// The posture check comes before the pricing check on purpose: a tenant
	// nobody bills has no charge to fail closed on, so an Enterprise box
	// running a locally hosted model that carries no catalog price keeps
	// serving instead of being refused for a price it never needed.
	if state.Deployment == metering.DeploymentEnterpriseEdge {
		return nil, false
	}
	// An alias that cannot be priced in tokens has no charge this endpoint can
	// derive. Inventing a rate and serving it free are both worse than
	// refusing, so this fails closed (D-034).
	if !inference.CanPriceTokens(in.Route) {
		slog.Warn(in.Surface+" refused, alias not priced in tokens",
			"request_id", in.RequestID, "alias", in.Alias, "price_unit", in.Route.PriceUnit)
		WriteUnpriceableModel(w)
		return nil, true
	}
	if !state.Found {
		slog.Warn(in.Surface+" refused, tenant has no billing account",
			"request_id", in.RequestID, "tenant_id", in.TenantID, "alias", in.Alias)
		WriteBillingNotConfigured(w)
		return nil, true
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
	reservation, err := in.Accounting.CreateReservation(ctx, inference.CreateReservationInput{
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
			WriteInsufficientQuota(w)
			return nil, true
		}
		slog.Error(in.Surface+" reservation failed", "err", err, "request_id", in.RequestID, "alias", in.Alias)
		WriteReservationUnavailable(w)
		return nil, true
	}
	if reservation.ID == "" {
		// A 2xx with no reservation id is a control-plane contract violation.
		// Nothing could be settled against it, so it fails closed too.
		slog.Error(in.Surface+" reservation returned no id", "request_id", in.RequestID, "alias", in.Alias)
		WriteReservationUnavailable(w)
		return nil, true
	}

	return &Settlement{
		accounting:    in.Accounting,
		accountID:     accountID,
		reservationID: reservation.ID,
		requestID:     in.RequestID.String(),
		surface:       in.Surface,
		heldCredits:   held,
	}, false
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
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
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
