// Package metering implements the Metering Step 2 shadow-mode gate: for
// every provider-reaching dispatch through edge-api it resolves a
// precedence-ordered verdict (billable or not, and which rule fired), a
// provisional per-model credit estimate, and writes both to a log-only
// table. It never refuses, delays, or charges a request -- Execute always
// calls the caller's dispatch func exactly once and returns whatever it
// returns, unmodified. See the design brief (vault
// spec-2026-07-28-backend-metering.md section 9.2 Step 2) for the full
// rationale; enforcement is Step 4, not this package.
//
// This package is wired to nothing as of this PR. Wave 2 (chat/dispatch.go,
// inference/orchestrator.go, rag/chat_handler.go, rag/embed.go) and Wave 3
// (cmd/server/main.go) adopt it in later, separate PRs.
package metering

import "github.com/google/uuid"

// Verdict values. Only two: shadow mode never has a third state.
const (
	VerdictBillable    = "billable"
	VerdictNotBillable = "not_billable"
)

// PrecedenceRule values, one per rule in the precedence order this package
// implements (precedence.go). Exactly these six -- see resolvePrecedence's
// doc comment for the order they are checked in.
const (
	RuleNoCostBasis         = "no_cost_basis"
	RuleEnterpriseShadow    = "enterprise_shadow"
	RuleTenantSetting       = "tenant_setting"
	RuleAPIKeyDefault       = "api_key_default"
	RuleSessionCloudDefault = "session_cloud_default"
	RuleResolvedDefault     = "resolved_default"
)

// WouldRefuseCode values. Computed for grading only -- Step 2 never acts on
// any of these. RefuseInsufficientQuota is reserved: this package has no
// budget context (that lives in authz.CheckAccess, called separately
// upstream of this gate), so Execute never sets it; a later step that does
// have budget context may.
const (
	RefuseNone                 = ""
	RefuseInsufficientQuota    = "insufficient_quota"
	RefuseBillingNotConfigured = "billing_not_configured"
	RefuseBillingUnavailable   = "billing_unavailable"
)

// Deployment posture values, mirroring public.tenants.deployment's CHECK
// constraint (supabase/migrations/20260516_01_phase19_tenants.sql).
const (
	DeploymentHiveCloud      = "HIVE_CLOUD"
	DeploymentEnterpriseEdge = "ENTERPRISE_EDGE"
)

// Principal identifies who is making a request. Exactly one of the two
// shapes is populated by a real caller: TenantID+UserID for a session (JWT)
// principal (apps/edge-api/internal/auth.UserFrom, used at
// chat/dispatch.go:70), or AccountID+KeyID for an API-key principal
// (apps/edge-api/internal/authz.AuthSnapshot). This package never resolves
// auth itself; every caller builds a Principal from what it already has.
type Principal struct {
	TenantID  uuid.UUID
	UserID    uuid.UUID
	AccountID uuid.UUID
	KeyID     string
}

// IsSessionPrincipal reports whether this is a tenant-scoped session
// principal, as opposed to an API-key principal.
func (p Principal) IsSessionPrincipal() bool {
	return p.TenantID != uuid.Nil
}

// principalType returns the value stored in
// metering_shadow_verdicts.principal_type (CHECK'd to 'api_key' or
// 'session' by the migration in this same PR).
func (p Principal) principalType() string {
	if p.IsSessionPrincipal() {
		return "session"
	}
	return "api_key"
}

// RouteInfo is this package's OWN view of a resolved route's pricing,
// deliberately decoupled from control-plane's routing.SelectionResult --
// which does not carry pricing fields as of this PR (that field lands in a
// separate Wave 1a PR, apps/control-plane/internal/routing/types.go). A
// later Wave 3 adapter is responsible for populating this struct from
// whatever SelectionResult looks like once that PR merges; this package
// must not import that type before it exists.
//
// Provider is carried for structural parity with SelectionResult only. Per
// the org's provider-blind-errors rule, it is NEVER written to
// metering_shadow_verdicts (the migration in this PR has no provider
// column) and must never reach a customer-bound response.
type RouteInfo struct {
	LiteLLMModelName   string
	Provider           string
	InputPriceCredits  int64 // model_aliases.input_price_credits, credits per million input tokens
	OutputPriceCredits int64 // model_aliases.output_price_credits, credits per million output tokens
}

// HasCostBasis reports whether this route carries a usable price. A route
// with no resolved pricing (e.g. routing/pricing lookup failed, or an
// internal alias is legitimately unpriced) has no cost basis, and the gate
// resolves not_billable/no_cost_basis without consulting anything else.
func (r RouteInfo) HasCostBasis() bool {
	return r.InputPriceCredits > 0 || r.OutputPriceCredits > 0
}

// Request is the gate's per-dispatch input. Every adopter (chat/dispatch.go,
// inference/orchestrator.go, rag/chat_handler.go, rag/embed.go, in later
// waves) builds one of these from whatever it already resolved --
// auth.UserFrom, authz.AuthSnapshot, its own route selection. Gate never
// re-resolves auth or routing itself.
type Request struct {
	RequestID  string
	Principal  Principal
	Deployment string // public.tenants.deployment value, or "" for an API-key principal / unknown posture
	Endpoint   string
	AliasID    string
	Route      RouteInfo
}

// Outcome is informational only in shadow mode (Step 2). Nothing in it
// causes Execute to refuse, delay, or charge the wrapped dispatch; Step 4 is
// the step that may turn WouldRefuseCode into an actual refusal.
type Outcome struct {
	Verdict         string
	PrecedenceRule  string
	WouldRefuseCode string
	// Both figures are kept side by side per design brief section 3.5, so a
	// reviewer can see the gap between today's flat-token convention and the
	// provisional per-model formula directly in the data.
	EstimatedCreditsLegacy   int64
	EstimatedCreditsPerModel int64
}

// verdict is precedence.go's internal resolution result, before dispatch
// has reported token counts (so no credit figures live here yet -- those
// are derived in gate.go once DispatchResult is known).
type verdict struct {
	Verdict         string
	PrecedenceRule  string
	WouldRefuseCode string
	AccountID       uuid.UUID
}
