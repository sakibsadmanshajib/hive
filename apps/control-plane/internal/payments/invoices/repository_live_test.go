package invoices

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/packages/dbtest"
)

// =============================================================================
// Live-schema coverage for the invoice money path (issue #1648).
//
// The in-memory suites prove the conversion arithmetic. They cannot prove that
// the SQL reads the ledger in credits, nor that the recorded rate survives the
// numeric column round trip, because both of those are the database's opinion,
// not the fake's. Gated on HIVE_TEST_DB_URL like every other live suite here;
// CI's ephemeral Postgres carries the full migration chain and runs
// ./internal/payments/... in that leg.
// =============================================================================

func newInvoicesTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return dbtest.Pool(t, "HIVE_TEST_DB_URL")
}

// seedInvoiceWorkspace inserts the auth.users and public.accounts rows an
// invoice must reference, and registers cleanup for everything it writes.
func seedInvoiceWorkspace(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID, accountID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO auth.users (id, email, raw_user_meta_data)
		 VALUES (gen_random_uuid(), $1, '{}'::jsonb) RETURNING id`,
		"invoices-ws-"+uuid.NewString()+"@test.local",
	).Scan(&userID); err != nil {
		// Fatal, not Skip: the live leg bootstraps the whole migration chain,
		// so a seed failure is a schema regression and skipping it would report
		// green over one.
		t.Fatalf("seed auth.users failed (is this a migrated test DB?): %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		 VALUES (gen_random_uuid(), $1, 'invoices test', 'personal', $2) RETURNING id`,
		"invoices-ws-"+uuid.NewString(), userID,
	).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = pool.Exec(cleanup, `DELETE FROM public.invoices WHERE workspace_id = $1`, accountID)
		_, _ = pool.Exec(cleanup, `DELETE FROM public.credit_ledger_entries WHERE account_id = $1`, accountID)
		_, _ = pool.Exec(cleanup, `DELETE FROM public.accounts WHERE id = $1`, accountID)
		_, _ = pool.Exec(cleanup, `DELETE FROM auth.users WHERE id = $1`, userID)
	})

	return accountID
}

func seedUsageCharge(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, credits int64, model string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO public.credit_ledger_entries
		     (account_id, entry_type, credits_delta, idempotency_key, request_id, created_at, metadata)
		 VALUES ($1, 'usage_charge', $2, $3, 'invoices-live', $4::timestamptz, jsonb_build_object('model', $5::text))`,
		accountID, -credits, "invoice-live-"+uuid.NewString(), at.Format(time.RFC3339Nano), model,
	); err != nil {
		t.Fatalf("seed usage charge: %v", err)
	}
}

// TestAggregateByModel_Live proves the aggregate reads the ledger in credits
// and buckets by model. The magnitude is the one measured on the live console
// on 2026-09-01, so a regression that reintroduces the subunit alias shows up
// here as a five-order-of-magnitude difference rather than as a rounding
// argument.
func TestAggregateByModel_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	accountID := seedInvoiceWorkspace(t, pool)
	repo := NewPgxRepository(pool)

	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	seedUsageCharge(t, pool, accountID, 500_000_000, "hive-fast", period.Start.Add(24*time.Hour))
	seedUsageCharge(t, pool, accountID, 24_653_338, "hive-fast", period.Start.Add(48*time.Hour))
	seedUsageCharge(t, pool, accountID, 1_000_000_000, "hive-auto", period.Start.Add(72*time.Hour))
	// Outside the window: must not be counted.
	seedUsageCharge(t, pool, accountID, 999_000_000_000, "hive-fast", period.End.Add(time.Hour))

	got, err := repo.AggregateByModel(context.Background(), accountID, period)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("buckets = %d, want 2: %+v", len(got), got)
	}

	byModel := map[string]ModelCredits{}
	for _, row := range got {
		byModel[row.ModelID] = row
	}
	fast, ok := byModel["hive-fast"]
	if !ok {
		t.Fatalf("no hive-fast bucket in %+v", got)
	}
	if want := big.NewInt(524_653_338); fast.Credits.Cmp(want) != 0 {
		t.Fatalf("hive-fast credits = %s, want %s", fast.Credits, want)
	}
	if fast.RequestCount != 2 {
		t.Fatalf("hive-fast requests = %d, want 2", fast.RequestCount)
	}
	if auto := byModel["hive-auto"]; auto.Credits.Cmp(big.NewInt(1_000_000_000)) != 0 {
		t.Fatalf("hive-auto credits = %s, want 1000000000", auto.Credits)
	}
}

// TestInsertOrFetch_RecordsRate_Live proves the recorded rate survives the
// numeric column in both directions, and that a row written without one reads
// back empty rather than as a plausible-looking default. NULL is how an
// operator tells a converted invoice from a pre-fix conflated one, so it has to
// stay distinguishable through the driver.
func TestInsertOrFetch_RecordsRate_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	accountID := seedInvoiceWorkspace(t, pool)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}

	saved, err := repo.InsertOrFetch(ctx, Invoice{
		WorkspaceID:      accountID,
		PeriodStart:      period.Start,
		PeriodEnd:        period.End,
		TotalBDTSubunits: big.NewInt(6460),
		LineItems: []InvoiceLineItem{
			{ModelID: "hive-fast", RequestCount: 2, BDTSubunits: big.NewInt(6460)},
		},
		GeneratedAt: time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC),
		USDBDTRate:  "123.130000",
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if saved.USDBDTRate != "123.130000" {
		t.Fatalf("rate round trip = %q, want %q", saved.USDBDTRate, "123.130000")
	}
	if saved.TotalBDTSubunits.Cmp(big.NewInt(6460)) != 0 {
		t.Fatalf("total = %s, want 6460", saved.TotalBDTSubunits)
	}

	fetched, err := repo.GetByID(ctx, saved.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if fetched.USDBDTRate != "123.130000" {
		t.Fatalf("fetched rate = %q, want %q", fetched.USDBDTRate, "123.130000")
	}

	listed, err := repo.ListByWorkspace(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].USDBDTRate != "123.130000" {
		t.Fatalf("listed = %+v, want one row carrying the rate", listed)
	}

	// A legacy-shaped row: no rate recorded, so the column stays NULL and reads
	// back as the empty string.
	julyStart := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	legacy, err := repo.InsertOrFetch(ctx, Invoice{
		WorkspaceID:      accountID,
		PeriodStart:      julyStart,
		PeriodEnd:        period.Start,
		TotalBDTSubunits: big.NewInt(524_653_338),
		GeneratedAt:      time.Date(2026, 8, 1, 2, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("insert legacy: %v", err)
	}
	if legacy.USDBDTRate != "" {
		t.Fatalf("legacy rate = %q, want empty", legacy.USDBDTRate)
	}
}

// TestLatestUSDBDTRate_Live proves the snapshot lookup reads the newest
// qualifying row for the right account, and that the period bound holds.
//
// The rate an invoice is denominated at comes from here first, so a query that
// silently returned nothing would send every invoice to the platform fallback,
// and one without the period bound would let a top-up after the month closed
// re-denominate it. Only real SQL can catch either: the fake repository answers
// whatever the test sets.
func TestLatestUSDBDTRate_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	accountID := seedInvoiceWorkspace(t, pool)
	other := seedInvoiceWorkspace(t, pool)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}

	// No snapshot yet: empty string, no error, so the caller falls back.
	rate, err := repo.LatestUSDBDTRate(ctx, accountID, period.End)
	if err != nil {
		t.Fatalf("lookup with no snapshot: %v", err)
	}
	if rate != "" {
		t.Fatalf("rate = %q, want empty for an account with no snapshot", rate)
	}

	seedFXSnapshot(t, pool, accountID, "120.000000", period.Start.Add(2*24*time.Hour))
	seedFXSnapshot(t, pool, accountID, "129.586500", period.Start.Add(20*24*time.Hour))
	seedFXSnapshot(t, pool, other, "999.000000", period.Start.Add(21*24*time.Hour))

	rate, err = repo.LatestUSDBDTRate(ctx, accountID, period.End)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if rate != "129.586500" {
		t.Fatalf("rate = %q, want the newest snapshot for this account (129.586500)", rate)
	}

	// A top-up after the month closed must not re-denominate it, and a
	// regeneration months later must produce the same figure the original run
	// did rather than the rate on the day it was re-run.
	seedFXSnapshot(t, pool, accountID, "150.000000", period.End.Add(48*time.Hour))

	rate, err = repo.LatestUSDBDTRate(ctx, accountID, period.End)
	if err != nil {
		t.Fatalf("lookup after a later snapshot: %v", err)
	}
	if rate != "129.586500" {
		t.Fatalf("rate = %q, want 129.586500: a snapshot taken after the period end re-denominated a closed month", rate)
	}

	// The same later snapshot is the right answer for the period it falls in.
	rate, err = repo.LatestUSDBDTRate(ctx, accountID, period.End.AddDate(0, 1, 0))
	if err != nil {
		t.Fatalf("lookup for the next period: %v", err)
	}
	if rate != "150.000000" {
		t.Fatalf("rate = %q, want 150.000000 for the period that snapshot falls in", rate)
	}
}

func seedFXSnapshot(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, effective string, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO public.fx_snapshots
		     (account_id, base_currency, quote_currency, mid_rate, fee_rate, effective_rate, source_api, fetched_at, created_at)
		 VALUES ($1, 'USD', 'BDT', $2, '0.05', $2, 'xe', $3::timestamptz, $3::timestamptz)`,
		accountID, effective, at.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("seed fx snapshot: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.fx_snapshots WHERE account_id = $1`, accountID)
	})
}
