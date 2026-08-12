package profiles

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/testdb"
)

// newBillingJoinTestPool connects to the CI-bootstrapped Postgres that has the
// full supabase/migrations chain applied (see .github/workflows/ci.yml). Skips
// when HIVE_TEST_DB_URL is unset and refuses a DSN without a "test" marker,
// mirroring rag/provision_test.go and tenant/settings/testhelpers_test.go,
// because the fixtures below delete rows by slug and email prefix.
func newBillingJoinTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := testdb.RequireTestDSN(t)
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// seedBillingJoinAccount inserts an auth user plus an account with the given
// slug and account type, and no account_profiles row. Registers cleanup that
// removes both (account_profiles and account_billing_profiles cascade).
func seedBillingJoinAccount(t *testing.T, pool *pgxpool.Pool, slug, accountType string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	email := slug + "@example.test"

	var ownerID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users(id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		email).Scan(&ownerID); err != nil {
		t.Fatalf("insert auth user: %v", err)
	}

	var accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts(id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, $1, $2, $3) RETURNING id`,
		slug, accountType, ownerID).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(ctx, `DELETE FROM auth.users WHERE id = $1`, ownerID)
	})

	return accountID
}

// TestGetBillingProfileWithoutAccountProfileRow is the regression guard for the
// live 500 on /console/settings/billing. GetBillingProfile inner-joined
// public.account_profiles, so an account with zero profile rows produced
// pgx.ErrNoRows -> ErrNotFound -> 404, which the web-console client turned into
// a thrown error that crashed the Server Components tree. The query's own
// COALESCE fallbacks already assume that table may be absent, so a missing
// profile row must yield the coalesced empty profile instead of not-found.
func TestGetBillingProfileWithoutAccountProfileRow(t *testing.T) {
	pool := newBillingJoinTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	cases := []struct {
		name            string
		accountType     string
		wantEntityType  string
	}{
		{"personal account defaults to individual", "personal", "individual"},
		{"business account defaults to private company", "business", "private_company"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			accountID := seedBillingJoinAccount(t, pool, "billingjoin-"+tc.accountType+"-"+uuid.NewString()[:8], tc.accountType)

			profile, err := repo.GetBillingProfile(ctx, accountID)
			if err != nil {
				t.Fatalf("GetBillingProfile for account with no profile row: %v", err)
			}

			if profile.LegalEntityType != tc.wantEntityType {
				t.Fatalf("legal_entity_type = %q, want %q", profile.LegalEntityType, tc.wantEntityType)
			}
			for field, got := range map[string]string{
				"billing_contact_name":         profile.BillingContactName,
				"billing_contact_email":        profile.BillingContactEmail,
				"legal_entity_name":            profile.LegalEntityName,
				"business_registration_number": profile.BusinessRegistrationNumber,
				"vat_number":                   profile.VATNumber,
				"tax_id_type":                  profile.TaxIDType,
				"tax_id_value":                 profile.TaxIDValue,
				"country_code":                 profile.CountryCode,
				"state_region":                 profile.StateRegion,
			} {
				if got != "" {
					t.Fatalf("%s = %q, want empty string for a profile-less account", field, got)
				}
			}
		})
	}
}

// TestGetBillingProfileWithAccountProfileRowUnchanged pins the pre-existing
// behaviour for an account that does have an account_profiles row: the
// ap.* COALESCE fallbacks still supply contact and location values. Widening
// the join must not change what a provisioned tenant already receives.
func TestGetBillingProfileWithAccountProfileRowUnchanged(t *testing.T) {
	pool := newBillingJoinTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	accountID := seedBillingJoinAccount(t, pool, "billingjoin-withprofile-"+uuid.NewString()[:8], "business")
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.account_profiles(account_id, owner_name, login_email, country_code, state_region, profile_setup_complete)
		 VALUES ($1, 'Ada Owner', 'ada@example.test', 'CA', 'ON', true)`,
		accountID); err != nil {
		t.Fatalf("insert account profile: %v", err)
	}

	profile, err := repo.GetBillingProfile(ctx, accountID)
	if err != nil {
		t.Fatalf("GetBillingProfile for account with a profile row: %v", err)
	}

	for _, check := range []struct {
		field string
		got   string
		want  string
	}{
		{"billing_contact_name", profile.BillingContactName, "Ada Owner"},
		{"billing_contact_email", profile.BillingContactEmail, "ada@example.test"},
		{"country_code", profile.CountryCode, "CA"},
		{"state_region", profile.StateRegion, "ON"},
		{"legal_entity_type", profile.LegalEntityType, "private_company"},
	} {
		if check.got != check.want {
			t.Fatalf("%s = %q, want %q", check.field, check.got, check.want)
		}
	}
}
