package metering

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// fakeSettings is a hand-rolled TenantSettingsCache fake -- no docker/DB
// fixture needed to exercise precedence logic in isolation.
type fakeSettings struct {
	enabled bool
	ok      bool
	err     error
}

func (f fakeSettings) EnableUsageMetering(ctx context.Context, tenantID uuid.UUID) (bool, bool, error) {
	return f.enabled, f.ok, f.err
}

// fakeBilling is a hand-rolled BillingAccountResolver fake.
type fakeBilling struct {
	accountID uuid.UUID
	found     bool
	err       error
}

func (f fakeBilling) Resolve(ctx context.Context, tenantID uuid.UUID) (uuid.UUID, bool, error) {
	return f.accountID, f.found, f.err
}

// TestResolvePrecedence is the core acceptance table for this package: one
// case per precedence rule named in the design brief (section 3.1's Outcome
// doc comment), asserting Verdict + PrecedenceRule exactly, plus the
// WouldRefuseCode / AccountID side effects that ride along with a couple of
// the rules. Nothing here calls a dispatch func -- that invariant (dispatch
// always runs regardless of verdict) is proven separately in gate_test.go.
func TestResolvePrecedence(t *testing.T) {
	tenantID := uuid.New()
	accountID := uuid.New()
	apiAccountID := uuid.New()
	pricedRoute := RouteInfo{InputPriceCredits: 12, OutputPriceCredits: 36}
	unpricedRoute := RouteInfo{}

	tests := []struct {
		name        string
		req         Request
		settings    fakeSettings
		billing     fakeBilling
		wantVerdict string
		wantRule    string
		wantRefuse  string
		wantAccount uuid.UUID
	}{
		{
			name: "no cost basis wins over every other signal",
			req: Request{
				Principal:  Principal{TenantID: tenantID},
				Deployment: DeploymentHiveCloud,
				Route:      unpricedRoute,
			},
			settings:    fakeSettings{ok: false},
			wantVerdict: VerdictNotBillable,
			wantRule:    RuleNoCostBasis,
		},
		{
			name: "enterprise deployment shadows regardless of an explicit setting",
			req: Request{
				Principal:  Principal{TenantID: tenantID},
				Deployment: DeploymentEnterpriseEdge,
				Route:      pricedRoute,
			},
			settings:    fakeSettings{enabled: true, ok: true},
			wantVerdict: VerdictNotBillable,
			wantRule:    RuleEnterpriseShadow,
		},
		{
			name: "explicit tenant setting enabled resolves billing and bills",
			req: Request{
				Principal:  Principal{TenantID: tenantID},
				Deployment: DeploymentHiveCloud,
				Route:      pricedRoute,
			},
			settings:    fakeSettings{enabled: true, ok: true},
			billing:     fakeBilling{accountID: accountID, found: true},
			wantVerdict: VerdictBillable,
			wantRule:    RuleTenantSetting,
			wantAccount: accountID,
		},
		{
			name: "explicit tenant setting disabled never bills",
			req: Request{
				Principal:  Principal{TenantID: tenantID},
				Deployment: DeploymentHiveCloud,
				Route:      pricedRoute,
			},
			settings:    fakeSettings{enabled: false, ok: true},
			wantVerdict: VerdictNotBillable,
			wantRule:    RuleTenantSetting,
		},
		{
			name: "api key principal defaults to billable off its own account",
			req: Request{
				Principal: Principal{AccountID: apiAccountID},
				Route:     pricedRoute,
			},
			settings:    fakeSettings{ok: false},
			wantVerdict: VerdictBillable,
			wantRule:    RuleAPIKeyDefault,
			wantAccount: apiAccountID,
		},
		{
			name: "session principal on cloud with no explicit setting defaults billable",
			req: Request{
				Principal:  Principal{TenantID: tenantID},
				Deployment: DeploymentHiveCloud,
				Route:      pricedRoute,
			},
			settings:    fakeSettings{ok: false},
			billing:     fakeBilling{accountID: accountID, found: true},
			wantVerdict: VerdictBillable,
			wantRule:    RuleSessionCloudDefault,
			wantAccount: accountID,
		},
		{
			name: "session principal with unresolved deployment falls to the resolved default",
			req: Request{
				Principal:  Principal{TenantID: tenantID},
				Deployment: "",
				Route:      pricedRoute,
			},
			settings:    fakeSettings{ok: false},
			billing:     fakeBilling{accountID: accountID, found: true},
			wantVerdict: VerdictBillable,
			wantRule:    RuleResolvedDefault,
			wantAccount: accountID,
		},
		{
			name: "missing billing mapping logs billing_not_configured but still bills (never refuses)",
			req: Request{
				Principal:  Principal{TenantID: tenantID},
				Deployment: DeploymentHiveCloud,
				Route:      pricedRoute,
			},
			settings:    fakeSettings{ok: false},
			billing:     fakeBilling{found: false},
			wantVerdict: VerdictBillable,
			wantRule:    RuleSessionCloudDefault,
			wantRefuse:  RefuseBillingNotConfigured,
		},
		{
			name: "billing resolver error surfaces billing_unavailable, still bills (never refuses)",
			req: Request{
				Principal:  Principal{TenantID: tenantID},
				Deployment: DeploymentHiveCloud,
				Route:      pricedRoute,
			},
			settings:    fakeSettings{ok: false},
			billing:     fakeBilling{err: errors.New("db down")},
			wantVerdict: VerdictBillable,
			wantRule:    RuleSessionCloudDefault,
			wantRefuse:  RefuseBillingUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &Gate{settings: tt.settings, billing: tt.billing}
			got := g.resolvePrecedence(context.Background(), tt.req)
			if got.Verdict != tt.wantVerdict {
				t.Errorf("Verdict = %q, want %q", got.Verdict, tt.wantVerdict)
			}
			if got.PrecedenceRule != tt.wantRule {
				t.Errorf("PrecedenceRule = %q, want %q", got.PrecedenceRule, tt.wantRule)
			}
			if got.WouldRefuseCode != tt.wantRefuse {
				t.Errorf("WouldRefuseCode = %q, want %q", got.WouldRefuseCode, tt.wantRefuse)
			}
			if tt.wantAccount != uuid.Nil && got.AccountID != tt.wantAccount {
				t.Errorf("AccountID = %v, want %v", got.AccountID, tt.wantAccount)
			}
		})
	}
}

// TestPriceEstimate_PerModelDiffersByAliasPrice is the regression guard
// spec 8.2 item 9 asks for: two aliases with different per-model prices must
// not collapse back to the flat total_tokens figure.
func TestPriceEstimate_PerModelDiffersByAliasPrice(t *testing.T) {
	cheap := RouteInfo{InputPriceCredits: 8, OutputPriceCredits: 24}
	pricey := RouteInfo{InputPriceCredits: 12, OutputPriceCredits: 36}

	_, cheapCredits := priceEstimate(cheap, 1_000_000, 1_000_000, VerdictBillable)
	_, priceyCredits := priceEstimate(pricey, 1_000_000, 1_000_000, VerdictBillable)

	if cheapCredits == priceyCredits {
		t.Fatalf("expected different per-model credits for different alias prices, got %d for both", cheapCredits)
	}
	if cheapCredits != 32 { // (1e6*8 + 1e6*24) / 1e6
		t.Errorf("cheap perModel = %d, want 32", cheapCredits)
	}
	if priceyCredits != 48 { // (1e6*12 + 1e6*36) / 1e6
		t.Errorf("pricey perModel = %d, want 48", priceyCredits)
	}
}

// TestPriceEstimate_FloorsAtOneWhenBillableAndTokensProduced covers the
// engineering-recommended interim rounding rule (design brief section 3.5,
// spec section 12 item 1): a real, tiny, billable request must never grade
// as zero credits just because per-million pricing rounds down to nothing.
func TestPriceEstimate_FloorsAtOneWhenBillableAndTokensProduced(t *testing.T) {
	route := RouteInfo{InputPriceCredits: 1, OutputPriceCredits: 1}
	_, perModel := priceEstimate(route, 1, 0, VerdictBillable)
	if perModel != 1 {
		t.Errorf("perModel = %d, want 1 (floored)", perModel)
	}
}

// TestPriceEstimate_NotBillableIsNeverFlooredToOne makes sure the floor-at-1
// rule only applies to billable verdicts -- a not_billable request logging a
// provisional per-model figure of 1 credit would misreport as "would have
// charged something" when the precedence rule already said it would not.
func TestPriceEstimate_NotBillableIsNeverFlooredToOne(t *testing.T) {
	route := RouteInfo{InputPriceCredits: 1, OutputPriceCredits: 1}
	_, perModel := priceEstimate(route, 1, 0, VerdictNotBillable)
	if perModel != 0 {
		t.Errorf("perModel = %d, want 0 (not_billable is never floored)", perModel)
	}
}

func TestPriceEstimate_LegacyIsFlatTokenSum(t *testing.T) {
	legacy, _ := priceEstimate(RouteInfo{InputPriceCredits: 12, OutputPriceCredits: 36}, 100, 50, VerdictBillable)
	if legacy != 150 {
		t.Errorf("legacy = %d, want 150", legacy)
	}
}

// TestPriceEstimate_HighTokenRequestBillsCorrectedRoutePrice is #617/D-032's
// sanity check, done right: the ~90-token sample in #617's own writeup
// rounds to the same 1-credit floor whether priced at the old broken 8/24
// or a corrected route price, because the floor-at-1 rule swallows the
// whole error at that volume. A high-token request is what actually proves
// repricing worked. Uses hive-fast's Groq route (llama-3.3-70b-versatile,
// $0.59/$0.79 per million tokens, live 2026-07-31) at the ruled 1.4x margin:
// 82,600 input / 110,600 output credits per million (D-032).
func TestPriceEstimate_HighTokenRequestBillsCorrectedRoutePrice(t *testing.T) {
	route := RouteInfo{InputPriceCredits: 82600, OutputPriceCredits: 110600}

	// Hand-computed, not derived from the code under test:
	//   (50,000*82,600 + 10,000*110,600) / 1,000,000
	// = (4,130,000,000 + 1,106,000,000) / 1,000,000
	// = 5,236,000,000 / 1,000,000 = 5236 exactly (no rounding).
	// The OLD stored price (8/24) on this same request:
	//   (50,000*8 + 10,000*24) / 1,000,000 = 640,000/1,000,000 -> floors to 1.
	// So the pre-repricing catalog would have billed 1 credit for a request
	// that should cost 5,236 -- the under-pricing #617 describes, visible
	// only because this request is large enough to clear the floor.
	_, perModel := priceEstimate(route, 50_000, 10_000, VerdictBillable)
	if perModel != 5236 {
		t.Fatalf("expected corrected charge 5236 credits, got %d", perModel)
	}
}
