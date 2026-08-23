package chat

// Settlement for JWT-session chat (#746).
//
// Session-authenticated chat used to serve inference and record nothing: no
// hold, no charge, no verdict, only an llm_traces row that always read zero
// because the usage envelope was never requested. Once PR #735 gave Open WebUI
// a per-user token, every chat turn on the box took that branch, so roughly
// five days of production traffic was served free and invisible to the console
// (#856 is the reader-side view of the same hole).
//
// This file is the accounting half of the fix. The rules it enforces, all of
// them before a provider is ever reached:
//
//  1. A tenant with no billing account is refused, never served free (#721
//     tracks the provisioning gap that makes this reachable).
//  2. An alias whose catalog row cannot be priced in tokens is refused (D-034),
//     rather than recorded as an unpriced trace the way this path used to.
//  3. A credit hold is taken before dispatch, so an account with no credits is
//     refused by the reservation itself. The budget middleware never covered
//     this path: it resolves workspace identity from an hk_ bearer only.
//  4. Every reservation reaches a terminal state exactly once, finalized at the
//     served route's catalog price or released in full, never both and never
//     neither.
//
// The one exception is deployment posture: an ENTERPRISE_EDGE tenant has no
// prepaid relationship with Hive, so it is explicitly not billable (D-027) and
// takes no hold. That is a recorded non-billable verdict, not silence: the
// trace and audit rows still carry the real token counts, which this path now
// obtains by forcing stream_options.include_usage.

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

// sessionHoldCredits is the flat pre-dispatch hold a session turn takes, the
// same figure the API-key chat path reserves. It is an authorization floor
// picked before the request runs, never a charge: settlement replaces it with
// the catalog price of the tokens actually metered.
const sessionHoldCredits = 10_000

// settlementTimeout bounds one control-plane accounting call made after the
// response has already been streamed. A var so tests can shrink it.
var settlementTimeout = 30 * time.Second

// BillingResolver answers what a session principal's tenant settles against:
// the billing account, and the deployment posture that decides whether it
// settles at all. metering.PGBillingAccountResolver is the production
// implementation; it reads both in one indexed lookup off the pool edge-api
// already holds open.
type BillingResolver interface {
	ResolveState(ctx context.Context, tenantID uuid.UUID) (metering.TenantBillingState, error)
}

// settlement is one request's live accounting state, from the hold taken
// before dispatch to the single terminal call that ends it.
type settlement struct {
	accounting    *inference.AccountingClient
	accountID     string
	reservationID string
	requestID     string
	// heldCredits is the size of the hold actually taken. Recorded because a
	// variable-price alias settles at the hold when it cannot read an upstream
	// cost, and it can no longer assume that figure is sessionHoldCredits.
	heldCredits int64
}

// held returns the hold size, or zero when there is no settlement at all (the
// Enterprise posture, which charges nothing).
func (s *settlement) held() int64 {
	if s == nil {
		return 0
	}
	return s.heldCredits
}

// startSettlement takes the hold for a session turn, or writes the customer's
// refusal and reports that the request must not be dispatched.
//
// A nil settlement with refused=false is the Enterprise posture: nothing to
// charge, so the request proceeds unheld and unbilled by decision rather than
// by omission.
func (h *Handler) startSettlement(
	ctx context.Context,
	w http.ResponseWriter,
	tenantID uuid.UUID,
	route inference.SelectRouteResult,
	alias string,
	requestID uuid.UUID,
) (*settlement, bool) {
	if h.deps.Accounting == nil || h.deps.Billing == nil {
		// A gateway that cannot charge must not serve. Reaching here means
		// this handler was constructed without its accounting seam, which is
		// the #746 failure mode itself, so it refuses rather than repeat it.
		slog.Error("session chat accounting not wired, refusing request", "request_id", requestID, "alias", alias)
		writeBillingUnavailable(w)
		return nil, true
	}

	state, err := h.deps.Billing.ResolveState(ctx, tenantID)
	if err != nil {
		// The billing position is unknown, not known-absent. Serving anyway is
		// how free traffic happens, so this refuses and asks for a retry.
		slog.Error("session chat billing lookup failed", "err", err, "request_id", requestID)
		writeBillingUnavailable(w)
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
	if !inference.CanPriceTokens(route) {
		slog.Warn("session chat refused, alias not priced in tokens",
			"request_id", requestID, "alias", alias, "price_unit", route.PriceUnit)
		writeUnpriceableModel(w)
		return nil, true
	}
	if !state.Found {
		slog.Warn("session chat refused, tenant has no billing account",
			"request_id", requestID, "tenant_id", tenantID, "alias", alias)
		writeBillingNotConfigured(w)
		return nil, true
	}

	accountID := state.AccountID.String()
	// Attempt creation is telemetry, so a failure here is logged and the
	// request continues: the charge below does not depend on it.
	if _, err := h.deps.Accounting.StartAttempt(ctx, inference.StartAttemptInput{
		AccountID:     accountID,
		RequestID:     requestID.String(),
		AttemptNumber: 1,
		Endpoint:      inference.EndpointChatCompletions,
		ModelAlias:    alias,
		Status:        "dispatching",
	}); err != nil {
		slog.Warn("session chat start attempt failed", "err", err, "request_id", requestID)
	}

	// A variable-price alias raises the flat session hold from its catalog
	// row: a router can resolve to a model far dearer than the flat figure
	// assumes, and the hold is the only solvency gate in front of it.
	held := inference.ReservationCredits(route, sessionHoldCredits)
	reservation, err := h.deps.Accounting.CreateReservation(ctx, inference.CreateReservationInput{
		AccountID:        accountID,
		RequestID:        requestID.String(),
		AttemptNumber:    1,
		Endpoint:         inference.EndpointChatCompletions,
		ModelAlias:       alias,
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
			slog.Info("session chat refused on quota", "request_id", requestID, "alias", alias)
			writeInsufficientQuota(w)
			return nil, true
		}
		slog.Error("session chat reservation failed", "err", err, "request_id", requestID, "alias", alias)
		writeReservationUnavailable(w)
		return nil, true
	}
	if reservation.ID == "" {
		// A 2xx with no reservation id is a control-plane contract violation.
		// Nothing could be settled against it, so it fails closed too.
		slog.Error("session chat reservation returned no id", "request_id", requestID, "alias", alias)
		writeReservationUnavailable(w)
		return nil, true
	}

	return &settlement{
		accounting:    h.deps.Accounting,
		accountID:     accountID,
		reservationID: reservation.ID,
		requestID:     requestID.String(),
		heldCredits:   held,
	}, false
}

// finalize charges the reservation and reports whether the charge actually
// landed. A false return leaves the reservation open on purpose, so the
// caller's deferred release still hands the hold back: charged here or
// released there, exactly once either way, never both (#616).
//
// Every settlement call runs on its own fresh background context, never the
// request context: a client disconnect is exactly what cancels that one, and
// settling on it converts a delivered response into a free one.
func (s *settlement) finalize(credits int64, confirmed bool, inTokens, outTokens int64) bool {
	err := s.finalizeOnce(credits, confirmed, inTokens, outTokens)
	if err == nil {
		return true
	}
	// A transport error or a timeout does not say the charge failed, only that
	// its answer was lost. Treating that as uncharged releases a hold the
	// control-plane has already settled, which the control-plane then rejects,
	// leaving the trace claiming charged=false and cost 0 for a request the
	// ledger did charge. So try once more: FinalizeReservation is idempotent by
	// construction (finalizeLocked replays the stored row for an already
	// finalized or needs_reconciliation reservation instead of charging again,
	// which TestFinalizeReservationTwiceChargesOnce holds in place), so the
	// retry either confirms the settled charge or lands the one that never did.
	// It cannot produce a second charge.
	slog.Warn("session chat finalize failed, retrying once before release",
		"err", err, "request_id", s.requestID, "reservation_id", s.reservationID)
	if retryErr := s.finalizeOnce(credits, confirmed, inTokens, outTokens); retryErr != nil {
		slog.Error("session chat finalize failed twice, releasing hold instead",
			"err", retryErr, "request_id", s.requestID, "reservation_id", s.reservationID)
		return false
	}
	return true
}

// finalizeOnce is one attempt, on its own fresh context so a retry is never
// handed the timed-out context of the attempt it is retrying.
func (s *settlement) finalizeOnce(credits int64, confirmed bool, inTokens, outTokens int64) error {
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
		Status:                 "completed",
	})
}

// release hands the whole hold back. The control-plane records the release as
// a usage event of its own, so a refused or failed request leaves an explicit
// zero-charge verdict rather than nothing at all.
func (s *settlement) release(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), settlementTimeout)
	defer cancel()
	if err := s.accounting.ReleaseReservation(ctx, inference.ReleaseReservationInput{
		AccountID:     s.accountID,
		ReservationID: s.reservationID,
		Reason:        reason,
	}); err != nil {
		// A stranded hold locks a customer's credits behind a charge that
		// never landed, so this is an error, not a warning.
		slog.Error("session chat release failed, hold may be stranded",
			"err", err, "request_id", s.requestID, "reservation_id", s.reservationID, "reason", reason)
	}
}

// The refusal bodies below all use the OpenAI error shape, the one every chat
// client on this surface already parses. None of them names a provider, a
// route, an amount or a balance.

func writeBillingUnavailable(w http.ResponseWriter) {
	code := "billing_unavailable"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error",
		"Usage accounting is temporarily unavailable. Please retry.", &code)
}

func writeBillingNotConfigured(w http.ResponseWriter) {
	code := "billing_not_configured"
	apierrors.WriteError(w, http.StatusForbidden, "invalid_request_error",
		"This workspace is not set up for usage yet. Contact your administrator to complete workspace setup.", &code)
}

func writeInsufficientQuota(w http.ResponseWriter) {
	code := "insufficient_quota"
	apierrors.WriteError(w, http.StatusTooManyRequests, "insufficient_quota",
		"You exceeded your current quota, please check your plan and billing details.", &code)
}

func writeReservationUnavailable(w http.ResponseWriter) {
	code := "reservation_unavailable"
	apierrors.WriteError(w, http.StatusServiceUnavailable, "api_error",
		"Credit reservation is temporarily unavailable. Please retry.", &code)
}

// An alias with no token price is a stable property of its catalog row, not a
// passing fault, so this is a 4xx: a 503 with type api_error tells every SDK
// retry layer to send the same request again, and it can never succeed. The
// wording asks for a different model rather than a retry, matching the status.
func writeUnpriceableModel(w http.ResponseWriter) {
	code := "model_not_supported"
	apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error",
		"The requested model is not available for this endpoint. Choose a supported model.", &code)
}
