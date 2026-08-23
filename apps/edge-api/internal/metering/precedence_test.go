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

// --- Issue #617: catalog price correction -------------------------------
//
// The seeded catalog prices were arbitrary placeholders sitting roughly
// 1300x to 7500x below real provider rates (actual ratios 1312x, 1750x,
// 1750x, 2333x, 5600x, 7467x). The migration
// 20260801_01_alias_pricing_correction.sql replaces them with
// ceil(provider list price per million * 1.4 * CreditsPerUSD).
//
// The trap these tests exist to avoid: priceEstimate floors a billable
// request at 1 credit, so at small token counts a badly under-priced alias
// and a correctly priced one BOTH return 1. An assertion written with tens
// of tokens passes with the bug fully intact. Every assertion below
// therefore uses thousands of tokens, and every expected figure is computed
// by hand in the comment so a later price change cannot silently invalidate
// the test -- it has to fail and be re-derived.

// correctedHiveFast mirrors the public.model_aliases row for hive-fast AS OF
// its 20260801-era repricing, in the PRE-RESCALE credit unit (1 USD was then
// 100,000 credits; since 2026-08-23 it is 1e9 and every stored figure is
// 10,000x larger). These are hand-built fixtures for ChargeCredits arithmetic,
// so their absolute size is arbitrary; only the derivation below has to stay
// true of what they claim to mirror: Groq llama-3.1-8b-instant at 0.05 in /
// 0.08 out USD per million (20260801_14_route_groq_fast_cheapest_model.sql),
// times the 1.4 margin multiplier, times the then-current CreditsPerUSD.
//
//	input:  0.05 * 1.4 * 100_000 =  7_000
//	output: 0.08 * 1.4 * 100_000 = 11_200
var correctedHiveFast = RouteInfo{InputPriceCredits: 7_000, OutputPriceCredits: 11_200}

// correctedHiveDefault mirrors hive-default AS OF its 20260801-era repricing,
// in the pre-rescale credit unit: OpenRouter openai/gpt-4o-mini at 0.15 in /
// 0.60 out USD per million.
//
//	input:  0.15 * 1.4 * 100_000 = 21_000
//	output: 0.60 * 1.4 * 100_000 = 84_000
var correctedHiveDefault = RouteInfo{InputPriceCredits: 21_000, OutputPriceCredits: 84_000}

// TestPriceEstimate_CorrectedPricesBillHighTokenRequests is requirement (a):
// a high-token request must bill the corrected amount, not the floored 1
// credit the placeholder prices produced.
func TestPriceEstimate_CorrectedPricesBillHighTokenRequests(t *testing.T) {
	// hive-fast, 40k prompt + 12k completion:
	//   40_000 *  7_000 =   280_000_000
	//   12_000 * 11_200 =   134_400_000
	//   sum             =   414_400_000
	//   / 1_000_000     =           414, remainder 400_000 -- 2*400_000 <
	//   1_000_000, so round-half-up leaves it at 414 rather than bumping to 415
	_, fast := priceEstimate(correctedHiveFast, 40_000, 12_000, VerdictBillable)
	if fast != 414 {
		t.Errorf("hive-fast perModel = %d, want 414", fast)
	}

	// hive-default, 50k prompt + 10k completion:
	//   50_000 * 21_000 = 1_050_000_000
	//   10_000 * 84_000 =   840_000_000
	//   sum             = 1_890_000_000
	//   / 1_000_000     =         1_890 exactly, remainder 0
	_, def := priceEstimate(correctedHiveDefault, 50_000, 10_000, VerdictBillable)
	if def != 1_890 {
		t.Errorf("hive-default perModel = %d, want 1890", def)
	}
}

// TestPriceEstimate_PlaceholderPricesUnderchargedAtHighTokenCounts is the
// regression guard for issue #617 itself. It pins the size of the defect at
// a realistic request size, so a revert to the placeholder numbers fails
// here loudly rather than being absorbed by the floor.
func TestPriceEstimate_PlaceholderPricesUnderchargedAtHighTokenCounts(t *testing.T) {
	placeholderHiveFast := RouteInfo{InputPriceCredits: 8, OutputPriceCredits: 24}

	// 40_000 * 8 + 12_000 * 24 = 320_000 + 288_000 = 608_000
	// 608_000 / 1_000_000 = 0 remainder 608_000; 2*608_000 >= 1_000_000 so
	// round half up gives 1. Billable, so the floor is not even reached.
	_, placeholder := priceEstimate(placeholderHiveFast, 40_000, 12_000, VerdictBillable)
	if placeholder != 1 {
		t.Errorf("placeholder perModel = %d, want 1 (this is the #617 defect)", placeholder)
	}

	_, corrected := priceEstimate(correctedHiveFast, 40_000, 12_000, VerdictBillable)
	if corrected <= placeholder {
		t.Fatalf("corrected (%d) must exceed placeholder (%d)", corrected, placeholder)
	}
	if corrected/placeholder != 414 {
		t.Errorf("corrected/placeholder = %d, want 414x", corrected/placeholder)
	}
}

// TestPriceEstimate_LowTokenCountsMaskTheDefect documents why every
// assertion above uses thousands of tokens. At 20 tokens the placeholder and
// the corrected price produce the SAME answer, because the floor-at-1 rule
// swallows the difference. A test written this way would have passed
// throughout the entire lifetime of issue #617.
func TestPriceEstimate_LowTokenCountsMaskTheDefect(t *testing.T) {
	placeholderHiveFast := RouteInfo{InputPriceCredits: 8, OutputPriceCredits: 24}

	_, placeholder := priceEstimate(placeholderHiveFast, 15, 5, VerdictBillable)
	_, corrected := priceEstimate(correctedHiveFast, 15, 5, VerdictBillable)

	if placeholder != 1 || corrected != 1 {
		t.Fatalf("expected both to floor to 1 at 20 tokens, got placeholder=%d corrected=%d", placeholder, corrected)
	}
}
