package metering

import (
	"context"
	"log/slog"
)

// DispatchFunc is the caller's actual upstream call (an HTTP round trip to
// LiteLLM, streamed or not). Execute wraps it without changing its
// signature or its result: shadow mode calls it exactly once, unconditionally,
// and returns whatever it returns, unmodified.
type DispatchFunc func(ctx context.Context) (DispatchResult, error)

// DispatchResult is what the wrapped dispatch reports back for verdict
// logging, once it has actually run. Confirmed=false means the caller could
// not determine a genuine terminal usage figure (falling back to a
// zero-clamp, say); Disconnected+Delivered cover the client-disconnect
// case (spec 8.2 item 11): Delivered is only meaningful when Disconnected
// is true.
type DispatchResult struct {
	PromptTokens     int64
	CompletionTokens int64
	Confirmed        bool
	Disconnected     bool
	Delivered        int64
}

// Gate resolves a shadow-mode verdict for a dispatch and logs it. It is a
// pure wrapper: in Step 2 it never refuses, delays, or alters what the
// wrapped dispatch returns.
type Gate struct {
	settings TenantSettingsCache
	billing  BillingAccountResolver
	log      VerdictLogger
}

// Deps are Gate's dependencies. All three are optional seams (nil-safe):
// a nil settings/billing resolver degrades to the resolved-default rule and
// billing_unavailable respectively (see precedence.go); a nil log simply
// skips the verdict write. Tests supply fakes; production wiring (a later
// Wave 3 PR) supplies PGBillingAccountResolver and PGVerdictLogger from this
// package. TenantSettingsCache has no concrete production implementation
// yet -- see its doc comment in precedence.go for why and what a follow-up
// PR needs to build.
type Deps struct {
	Settings TenantSettingsCache
	Billing  BillingAccountResolver
	Log      VerdictLogger
}

// New constructs a Gate from the supplied Deps.
func New(deps Deps) *Gate {
	return &Gate{settings: deps.Settings, billing: deps.Billing, log: deps.Log}
}

// Execute is a pure shadow-mode wrapper. It calls dispatch FIRST and always
// exactly once -- the customer's request is fully served before this
// package does any of its own work -- then resolves the precedence verdict,
// derives both credit estimates from whatever dispatch reported, and logs
// the result. The returned error is dispatch's own error, completely
// unmodified: a not_billable verdict, an unresolved billing account, or a
// verdict-log write failure NEVER turns into an error Execute hands back.
// That is the shadow-mode invariant this step's tests are built to prove;
// Step 4 is the step allowed to change it.
func (g *Gate) Execute(ctx context.Context, req Request, dispatch DispatchFunc) (Outcome, error) {
	result, dispatchErr := dispatch(ctx)

	v := g.resolvePrecedence(ctx, req)
	legacy, perModel := priceEstimate(req.Route, result.PromptTokens, result.CompletionTokens, v.Verdict)

	outcome := Outcome{
		Verdict:                  v.Verdict,
		PrecedenceRule:           v.PrecedenceRule,
		WouldRefuseCode:          v.WouldRefuseCode,
		EstimatedCreditsLegacy:   legacy,
		EstimatedCreditsPerModel: perModel,
	}

	g.writeVerdict(ctx, req, v, result, legacy, perModel)

	return outcome, dispatchErr
}

// writeVerdict writes the verdict-log row. This happens after dispatch has
// already returned -- i.e. after the response has already been fully
// relayed to the client in every real caller's shape (same point in the
// lifecycle as InsertTrace/insertAuditEvent, chat/trace.go, chat/audit.go)
// -- so it cannot add latency the customer can observe. A write failure is
// logged and swallowed, exactly like those two existing calls, never
// surfaced to the caller.
func (g *Gate) writeVerdict(ctx context.Context, req Request, v verdict, result DispatchResult, legacy, perModel int64) {
	if g.log == nil {
		return
	}
	var delivered *int64
	if result.Disconnected {
		d := result.Delivered
		delivered = &d
	}
	record := VerdictRecord{
		RequestID:                req.RequestID,
		TenantID:                 req.Principal.TenantID,
		AccountID:                v.AccountID,
		PrincipalType:            req.Principal.principalType(),
		Deployment:               req.Deployment,
		Endpoint:                 req.Endpoint,
		ModelAlias:               req.AliasID,
		PrecedenceRule:           v.PrecedenceRule,
		Verdict:                  v.Verdict,
		WouldRefuseCode:          v.WouldRefuseCode,
		PromptTokens:             result.PromptTokens,
		CompletionTokens:         result.CompletionTokens,
		TerminalUsageConfirmed:   result.Confirmed,
		EstimatedCreditsLegacy:   legacy,
		EstimatedCreditsPerModel: perModel,
		Disconnected:             result.Disconnected,
		DeliveredTokens:          delivered,
	}
	if err := g.log.LogVerdict(ctx, record); err != nil {
		slog.Warn("metering_shadow_verdicts write failed", "err", err, "request_id", req.RequestID)
	}
}
