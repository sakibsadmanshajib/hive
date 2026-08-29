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
// The lifecycle itself now lives in internal/sessionbilling, because RAG chat
// needed the identical thing and #669 is what happens when a session route
// does not get it: two surfaces served inference without ever asking whether
// the account could pay. This file is the chat-specific half, which is now
// only the endpoint, the hold floor and the log label. Everything the money
// path actually enforces, and the reasoning behind each rule, is documented on
// sessionbilling.Start.

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling"
)

// sessionHoldCredits is the flat pre-dispatch hold a session turn takes, the
// same figure the API-key chat path reserves. It is an authorization floor
// picked before the request runs, never a charge: settlement replaces it with
// the catalog price of the tokens actually metered.
const sessionHoldCredits = inference.DefaultHoldText

// BillingResolver answers what a session principal's tenant settles against:
// the billing account, and the deployment posture that decides whether it
// settles at all. metering.PGBillingAccountResolver is the production
// implementation; it reads both in one indexed lookup off the pool edge-api
// already holds open.
type BillingResolver = sessionbilling.Resolver

// settlement is one request's live accounting state. Aliased rather than
// wrapped so this package adds no second lifecycle of its own.
type settlement = sessionbilling.Settlement

// startSettlement takes the hold for a session chat turn, or writes the
// customer's refusal and reports that the request must not be dispatched.
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
	body []byte,
) (*settlement, bool) {
	return sessionbilling.Start(ctx, w, sessionbilling.Input{
		Accounting: h.deps.Accounting,
		Billing:    h.deps.Billing,
		TenantID:   tenantID,
		Route:      route,
		Alias:      alias,
		Endpoint:   inference.EndpointChatCompletions,
		RequestID:  requestID,
		Body:       body,
		HoldFloor:  sessionHoldCredits,
		Surface:    "session chat",
	})
}
