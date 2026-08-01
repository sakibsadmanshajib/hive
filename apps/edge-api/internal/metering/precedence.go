package metering

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TenantSettingsCache resolves the explicit ENABLE_USAGE_METERING setting
// for a tenant. ok=false means no row exists for that tenant/key -- the
// "unset, fall through to the resolved default" case -- mirroring
// public.tenant_settings' own current-value-only shape (no separate
// ok=false/err distinction needed beyond a plain query miss).
//
// No concrete production implementation ships in this PR. edge-api cannot
// query public.tenant_settings directly: tools/lint-no-direct-tenant-setting.mjs
// blocks any direct SQL against that table outside
// apps/control-plane/internal/tenant/settings/, and Go's own internal-package
// visibility rules block importing settings.Resolver from edge-api anyway
// (control-plane and edge-api are separate modules under go.work; a package
// under .../internal/... is only importable from the module tree rooted one
// level above internal). The sanctioned pattern this repo already uses for
// "edge-api needs a control-plane-owned setting" is an HTTP fetch with a
// short-TTL cache -- see apps/edge-api/internal/featuregate/gate.go's
// Gate.Fetch against control-plane's GET /internal/featuregate/{tenant_id}.
// ENABLE_USAGE_METERING is not registered in public.feature_gate_keys and
// that endpoint's ClientVisibleEnabled deliberately excludes billing-adjacent
// keys from its category allowlist, so reusing it as-is is not an option
// either. Wiring a real settings source (a new control-plane internal
// endpoint, or a billing-category addition to featuregate) is a follow-up a
// later PR must do; until then Wave 3 either passes a nil
// TenantSettingsCache (Gate falls through to the resolved-default rule) or
// supplies its own adapter.
type TenantSettingsCache interface {
	EnableUsageMetering(ctx context.Context, tenantID uuid.UUID) (enabled bool, ok bool, err error)
}

// BillingAccountResolver resolves tenant_id -> account_id via
// public.tenant_billing_accounts. found=false means the tenant has no row
// yet; the gate logs that as billing_not_configured and still dispatches --
// never a refusal in shadow mode (spec section 9.2).
type BillingAccountResolver interface {
	Resolve(ctx context.Context, tenantID uuid.UUID) (accountID uuid.UUID, found bool, err error)
}

// PGBillingAccountResolver reads tenant_billing_accounts directly off the
// pool edge-api already holds open (e.g. chat.Deps.Pool) -- no new network
// hop to control-plane, matching design brief section 3.1's "at most one
// indexed Postgres lookup per request" contract. Unlike TenantSettingsCache
// above, no repo-wide lint blocks a direct read of
// public.tenant_billing_accounts from edge-api, so this concrete
// implementation ships in this PR.
//
// ponytail: a direct query, no in-process cache. Add one if this shows up as
// a real p95 cost once Wave 3 wires it in; nothing in the test plan for this
// step requires it.
type PGBillingAccountResolver struct {
	Pool *pgxpool.Pool
}

// Resolve implements BillingAccountResolver.
func (r *PGBillingAccountResolver) Resolve(ctx context.Context, tenantID uuid.UUID) (uuid.UUID, bool, error) {
	if r == nil || r.Pool == nil {
		return uuid.Nil, false, nil
	}
	var accountID uuid.UUID
	err := r.Pool.QueryRow(ctx, `
		SELECT account_id FROM public.tenant_billing_accounts WHERE tenant_id = $1`, tenantID).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("metering: read billing account: %w", err)
	}
	return accountID, true, nil
}

// resolvePrecedence applies the precedence order (design brief section 3.1,
// spec section 4) in this fixed sequence -- the first rule that matches
// wins:
//
//  1. no_cost_basis: the route carries no usable price. Nothing to grade,
//     so nothing else is even consulted.
//  2. enterprise_shadow: the tenant's deployment posture is ENTERPRISE_EDGE.
//     An Enterprise tenant has no prepaid relationship with Hive; this
//     short-circuits before a billing account or tenant setting is ever
//     read.
//  3. tenant_setting: a session principal with an EXPLICIT
//     ENABLE_USAGE_METERING row (enabled=true or enabled=false) follows it
//     exactly.
//  4. api_key_default: an API-key principal (never tenant-setting scoped)
//     defaults to billable off its own account -- API keys are billed today
//     via the existing fail-open reservation path, so shadow mode's default
//     for them matches that.
//  5. session_cloud_default: a session principal on HIVE_CLOUD with no
//     explicit setting defaults billable.
//  6. resolved_default: anything else (a session principal whose deployment
//     posture is empty/unresolved) falls to the same billable default as (5)
//     under a distinct rule name, so a reviewer can tell "explicitly cloud"
//     apart from "posture unknown" in the verdict log.
//
// Rules 3, 5, and 6 additionally resolve a billing account via
// BillingAccountResolver so WouldRefuseCode can flag
// billing_not_configured/billing_unavailable; rule 4 already carries its
// account on the principal (an API key IS an account) and never
// re-resolves it. Rules 1 and 2 never touch the billing resolver at all --
// a not_billable verdict has no reason to spend a lookup on an account
// nobody is about to charge.
func (g *Gate) resolvePrecedence(ctx context.Context, req Request) verdict {
	if !req.Route.HasCostBasis() {
		return verdict{Verdict: VerdictNotBillable, PrecedenceRule: RuleNoCostBasis}
	}
	if req.Deployment == DeploymentEnterpriseEdge {
		return verdict{Verdict: VerdictNotBillable, PrecedenceRule: RuleEnterpriseShadow}
	}

	if req.Principal.IsSessionPrincipal() {
		enabled, explicit := g.resolveEnableSetting(ctx, req.Principal.TenantID)
		if explicit {
			v := verdict{PrecedenceRule: RuleTenantSetting}
			if enabled {
				v.Verdict = VerdictBillable
				v.AccountID, v.WouldRefuseCode = g.resolveBillingAccount(ctx, req.Principal.TenantID)
			} else {
				v.Verdict = VerdictNotBillable
			}
			return v
		}

		rule := RuleResolvedDefault
		if req.Deployment == DeploymentHiveCloud {
			rule = RuleSessionCloudDefault
		}
		v := verdict{Verdict: VerdictBillable, PrecedenceRule: rule}
		v.AccountID, v.WouldRefuseCode = g.resolveBillingAccount(ctx, req.Principal.TenantID)
		return v
	}

	// API-key principal: it already carries its own account, so this never
	// depends on a tenant_billing_accounts row existing at all. A nil
	// AccountID here would be an internal invariant violation (authz should
	// never produce one), but we log it defensively rather than pretend a
	// resolvable account exists.
	v := verdict{Verdict: VerdictBillable, PrecedenceRule: RuleAPIKeyDefault, AccountID: req.Principal.AccountID}
	if req.Principal.AccountID == uuid.Nil {
		v.WouldRefuseCode = RefuseBillingNotConfigured
	}
	return v
}

func (g *Gate) resolveEnableSetting(ctx context.Context, tenantID uuid.UUID) (enabled bool, explicit bool) {
	if g.settings == nil {
		return false, false
	}
	enabled, ok, err := g.settings.EnableUsageMetering(ctx, tenantID)
	if err != nil {
		// A settings-lookup failure is not itself gradeable evidence of
		// anything -- fall through to the resolved default exactly as if no
		// row existed. This is a fail-OPEN outcome, correct for shadow mode
		// (section 1: a control-plane/DB hiccup must never turn into a
		// refusal), and distinct from the billing_unavailable code, which is
		// reserved for a failed billing-account lookup specifically.
		return false, false
	}
	return enabled, ok
}

func (g *Gate) resolveBillingAccount(ctx context.Context, tenantID uuid.UUID) (uuid.UUID, string) {
	if g.billing == nil {
		return uuid.Nil, RefuseBillingUnavailable
	}
	accountID, found, err := g.billing.Resolve(ctx, tenantID)
	if err != nil {
		return uuid.Nil, RefuseBillingUnavailable
	}
	if !found {
		return uuid.Nil, RefuseBillingNotConfigured
	}
	return accountID, RefuseNone
}

// creditsPerMillion is the unit model_aliases.input_price_credits /
// output_price_credits are stored in.
var creditsPerMillion = big.NewInt(1_000_000)

// UnitCharge is one priced quantity: Quantity metered units at
// CreditsPerMillion credits per million of them. The metered unit is whatever
// model_aliases.price_unit names for the alias (tokens for text, characters
// for speech synthesis, seconds for transcription).
type UnitCharge struct {
	Quantity          int64
	CreditsPerMillion int64
}

// ChargeCredits converts priced quantities into whole credits: every
// component's quantity times its credits-per-million figure is summed FIRST,
// then divided once by a million and rounded half up. One division, not one
// per component, so two halves can never round independently and drift.
//
// This is the only implementation of that arithmetic in the tree (D-031,
// credits are per million units). math/big throughout, per repo convention:
// no float64 anywhere near a charge. Exported so the non-token modalities
// (edge-api internal/audio, which meters characters and seconds) reuse it
// rather than growing a second copy that could round differently.
func ChargeCredits(charges ...UnitCharge) int64 {
	numerator := new(big.Int)
	for _, charge := range charges {
		numerator.Add(numerator, new(big.Int).Mul(
			big.NewInt(charge.Quantity),
			big.NewInt(charge.CreditsPerMillion),
		))
	}

	quotient, remainder := new(big.Int).QuoRem(numerator, creditsPerMillion, new(big.Int))
	// Round half up: a remainder at least half of creditsPerMillion bumps
	// the quotient by one.
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(creditsPerMillion) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.Int64()
}

// priceEstimate computes both credit figures the design brief (section 3.5)
// asks be logged side by side:
//
//   - legacy: today's flat int64(total_tokens) convention (matches
//     chat/dispatch.go:234's costCredits := int64(totalTokens)).
//   - perModel: the engineering-recommended INTERIM rule for the unresolved
//     credit-unit question (spec section 12 item 1, blocks Step 4, not this
//     step): bigint math/big, credits-per-million-tokens, round half up,
//     floored at 1 for any BILLABLE request that produced tokens. This
//     figure is PROVISIONAL -- it is not gradeable as a final dollar amount
//     until the owner resolves the unit question -- but it is enough to
//     grade precedence-order correctness now, which is the actual Step 2
//     pass condition.
//
// No float64 anywhere near this calculation, per repo convention
// (limits/budget_gate.go, math/big for all cap/price comparisons).
func priceEstimate(route RouteInfo, promptTokens, completionTokens int64, verdictStr string) (legacy int64, perModel int64) {
	legacy = promptTokens + completionTokens

	perModel = ChargeCredits(
		UnitCharge{Quantity: promptTokens, CreditsPerMillion: route.InputPriceCredits},
		UnitCharge{Quantity: completionTokens, CreditsPerMillion: route.OutputPriceCredits},
	)

	if verdictStr == VerdictBillable && legacy > 0 && perModel < 1 {
		perModel = 1
	}
	return legacy, perModel
}
