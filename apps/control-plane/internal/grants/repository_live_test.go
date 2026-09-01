package grants

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// =============================================================================
// Live-schema coverage for the grant money path (issue #1659).
//
// credits_test.go proves the arithmetic. It cannot prove the two amounts reach
// the two columns, and that is the half that was actually broken: one variable
// was written into public.credit_ledger_entries.credits_delta AND public.
// credit_grants.amount_bdt_subunits, in different units, and no fake caught it
// because the service and handler suites never reach the repository at all.
// Only real SQL can catch the columns being swapped back.
//
// Gated on HIVE_TEST_DB_URL through the shared dbtest gate, which FAILS rather
// than skips in CI, so this cannot quietly stop running. ./internal/grants/...
// is named in the go-tests job's live-Postgres step for the same reason.
//
// CLEANUP IS DELIBERATELY PARTIAL. public.credit_grants carries a BEFORE UPDATE
// OR DELETE trigger that raises on any mutation, so the grant row this test
// writes cannot be removed, and the account it references cannot be dropped
// either (the FK has no ON DELETE CASCADE). That is the schema working as
// designed. The rows are inert, uniquely named, and land only in a database
// whose name contains "test", which dbtest enforces before connecting.
// =============================================================================

func newGrantsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return dbtest.Pool(t, "HIVE_TEST_DB_URL")
}

// seedGrantParties inserts the granting admin, the grantee, and the grantee's
// workspace account.
func seedGrantParties(t *testing.T, pool *pgxpool.Pool) (admin, grantee, workspace uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	newUser := func(label string) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO auth.users (id, email, raw_user_meta_data)
			 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
			"grants-"+label+"-"+uuid.NewString()+"@test.local",
		).Scan(&id); err != nil {
			// Fatal, not Skip: the live leg bootstraps the whole migration
			// chain, so a seed failure is a schema regression and skipping it
			// would report green over one.
			t.Fatalf("seed auth.users (%s) failed (is this a migrated test DB?): %v", label, err)
		}
		return id
	}

	admin = newUser("admin")
	grantee = newUser("grantee")

	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, 'grants test', 'personal', $2) RETURNING id`,
		"grants-ws-"+uuid.NewString(), grantee,
	).Scan(&workspace); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	return admin, grantee, workspace
}

// TestCreateWithLedger_PostsCreditsNotSubunits_Live is the wiring proof for
// issue #1659: the two columns must now hold two different quantities in two
// different units, joined by the recorded rate.
//
// The rate is pinned to 100 BDT per USD so the expected values are round, and
// both of them are literals rather than expressions over payments.CreditsPerUSD
// and payments.SubunitsPerBDT. Deriving them would make the assertion the
// algebraic inverse of the code under test and it would survive any error in
// either constant, which is exactly what was measured on issue #1648.
func TestCreateWithLedger_PostsCreditsNotSubunits_Live(t *testing.T) {
	t.Setenv(payments.USDBDTRateEnvVar, "100")

	pool := newGrantsTestPool(t)
	admin, grantee, workspace := seedGrantParties(t, pool)
	repo := NewPgxRepository(pool)

	// 100,000 paisa is 1,000 taka, ten USD at the pinned rate, and ten USD is
	// ten billion credits.
	const (
		subunits    int64 = 100_000
		wantCredits int64 = 10_000_000_000
	)

	res, err := repo.CreateWithLedger(context.Background(), CreateInput{
		GrantedByUserID:      admin,
		GrantedToUserID:      grantee,
		GrantedToWorkspaceID: workspace,
		AmountBDTSubunits:    big.NewInt(subunits),
		ReasonNote:           "issue 1659 live wiring proof",
		IdempotencyKey:       "grants-live-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create grant: %v", err)
	}

	// The audit row keeps the taka the admin authorised.
	if res.Grant.AmountBDTSubunits.Cmp(big.NewInt(subunits)) != 0 {
		t.Fatalf("returned amount_bdt_subunits = %s, want %d", res.Grant.AmountBDTSubunits, subunits)
	}

	var (
		storedSubunits int64
		storedCredits  int64
		entryType      string
		rawMetadata    []byte
	)
	if err := pool.QueryRow(context.Background(),
		`SELECT g.amount_bdt_subunits, e.credits_delta, e.entry_type, e.metadata
		   FROM public.credit_grants g
		   JOIN public.credit_ledger_entries e ON e.id = g.ledger_entry_id
		  WHERE g.id = $1`,
		res.Grant.ID,
	).Scan(&storedSubunits, &storedCredits, &entryType, &rawMetadata); err != nil {
		t.Fatalf("read back grant and ledger entry: %v", err)
	}

	if storedSubunits != subunits {
		t.Fatalf("credit_grants.amount_bdt_subunits = %d, want %d", storedSubunits, subunits)
	}
	if storedCredits != wantCredits {
		t.Fatalf("credit_ledger_entries.credits_delta = %d, want %d", storedCredits, wantCredits)
	}
	if storedCredits == storedSubunits {
		t.Fatalf("both columns hold %d: the subunit count was posted as credits again", storedCredits)
	}
	if entryType != "grant" {
		t.Fatalf("entry_type = %q, want %q", entryType, "grant")
	}

	// The metadata is what lets the row reproduce its own amounts, and the
	// credit_unit stamp is what keeps the rescale migration's straggler
	// detector from reading this row as a pre-rescale one needing a x10000
	// correction.
	var metadata struct {
		CreditUnit string `json:"credit_unit"`
		Subunits   string `json:"amount_bdt_subunits"`
		Rate       string `json:"usd_bdt_rate"`
		RateSource string `json:"usd_bdt_rate_source"`
	}
	if err := json.Unmarshal(rawMetadata, &metadata); err != nil {
		t.Fatalf("decode ledger metadata: %v", err)
	}
	if metadata.CreditUnit != ledger.CreditUnitV2 {
		t.Fatalf("metadata.credit_unit = %q, want %q", metadata.CreditUnit, ledger.CreditUnitV2)
	}
	if metadata.Subunits != "100000" {
		t.Fatalf("metadata.amount_bdt_subunits = %q, want %q", metadata.Subunits, "100000")
	}
	if metadata.Rate != "100.000000" {
		t.Fatalf("metadata.usd_bdt_rate = %q, want %q", metadata.Rate, "100.000000")
	}
	if metadata.RateSource != payments.RateSourceEnv {
		t.Fatalf("metadata.usd_bdt_rate_source = %q, want %q", metadata.RateSource, payments.RateSourceEnv)
	}
}

// TestCreateWithLedger_RefusesAGrantItCannotDenominate_Live keeps the money
// path fail-closed against real Postgres: an unusable rate must leave no grant
// row, no ledger entry and no claimed idempotency key behind, so a retry after
// the operator fixes the configuration is a clean first attempt rather than a
// permanent duplicate failure.
func TestCreateWithLedger_RefusesAGrantItCannotDenominate_Live(t *testing.T) {
	t.Setenv(payments.USDBDTRateEnvVar, "not-a-rate")

	pool := newGrantsTestPool(t)
	admin, grantee, workspace := seedGrantParties(t, pool)
	repo := NewPgxRepository(pool)

	key := "grants-live-bad-rate-" + uuid.NewString()
	if _, err := repo.CreateWithLedger(context.Background(), CreateInput{
		GrantedByUserID:      admin,
		GrantedToUserID:      grantee,
		GrantedToWorkspaceID: workspace,
		AmountBDTSubunits:    big.NewInt(100_000),
		IdempotencyKey:       key,
	}); err == nil {
		t.Fatal("expected an unparseable rate to refuse the grant")
	}

	for _, q := range []struct {
		name  string
		query string
		arg   any
	}{
		{"credit_grants", `SELECT count(*) FROM public.credit_grants WHERE granted_to_workspace_id = $1`, workspace},
		{"credit_ledger_entries", `SELECT count(*) FROM public.credit_ledger_entries WHERE account_id = $1`, workspace},
		{"credit_idempotency_keys", `SELECT count(*) FROM public.credit_idempotency_keys WHERE idempotency_key = $1`, key},
	} {
		var n int
		if err := pool.QueryRow(context.Background(), q.query, q.arg).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", q.name, err)
		}
		if n != 0 {
			t.Fatalf("%s holds %d rows after a refused grant, want 0", q.name, n)
		}
	}
}
