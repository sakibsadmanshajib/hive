package invoices

import (
	"context"
	"math/big"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// =============================================================================
// Live-schema coverage for the issue #1682 repair.
//
// The in-memory suite proves the arithmetic and the ordering. It cannot prove
// that the SELECT finds a NULL-rate row, that the UPDATE's own NULL guard makes
// a second pass a no-op at the database rather than only in the fake, or that a
// credit quantity survives the bigint column. Those are Postgres's opinions.
//
// Gated on HIVE_TEST_DB_URL like every other live suite here; CI's ephemeral
// Postgres carries the full migration chain and runs ./internal/payments/... in
// that leg.
// =============================================================================

// insertConflatedRow writes a pre-fix invoice directly, bypassing the
// repository, because the repository can no longer produce one: it always
// records a rate now. The raw INSERT is the point, since it reproduces exactly
// what the live table holds.
func insertConflatedRow(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, period Period, credits int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO public.invoices (
			id, workspace_id, period_start, period_end,
			total_bdt_subunits, line_items, pdf_storage_key
		)
		VALUES (gen_random_uuid(), $1, $2, $3, $4,
		        jsonb_build_array(jsonb_build_object(
		            'model_id', 'hive-fast',
		            'request_count', 412,
		            'bdt_subunits', $5::text)),
		        $6)
		RETURNING id
	`, accountID, period.Start, period.End, credits,
		strconv.FormatInt(credits, 10),
		storageKeyFor(accountID, period.Start)).Scan(&id)
	if err != nil {
		t.Fatalf("seed conflated invoice: %v", err)
	}
	return id
}

// readInvoiceRow reads the money columns straight out of the table rather than
// through the repository, so the assertion observes what Postgres holds and not
// what the application believes it wrote.
func readInvoiceRow(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (subunits int64, credits *int64, rate *string, source *string) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
		SELECT total_bdt_subunits, total_credits, usd_bdt_rate::text, usd_bdt_rate_source
		FROM public.invoices WHERE id = $1
	`, id).Scan(&subunits, &credits, &rate, &source); err != nil {
		t.Fatalf("read invoice row: %v", err)
	}
	return subunits, credits, rate, source
}

// TestRepairUnconvertedInvoices_Live is the acceptance check issue #1682 asks
// for: a conflated row goes in, the repair runs, and the row is read back out
// of the database and compared against the credit quantity rather than against
// the job's own log.
func TestRepairUnconvertedInvoices_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	accountID := seedInvoiceWorkspace(t, pool)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	id := insertConflatedRow(t, pool, accountID, period, conflatedCredits)

	// The ledger the row was generated from, so the repaired figure can be
	// checked against the credit magnitude the account actually burned rather
	// than only against the number already on the row.
	seedUsageCharge(t, pool, accountID, conflatedCredits, "hive-fast", period.Start.Add(24*time.Hour))

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{name: "Acme"}, nil)

	// Sanity: the row is selectable before the repair, which is what makes the
	// post-repair emptiness below mean something.
	pending, err := repo.ListUnconverted(ctx, 0)
	if err != nil {
		t.Fatalf("list unconverted: %v", err)
	}
	if !containsInvoice(pending, id) {
		t.Fatalf("seeded conflated row %s is not selectable by the repair predicate", id)
	}

	repaired, err := svc.RepairUnconvertedInvoices(ctx)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if repaired < 1 {
		t.Fatalf("repaired %d rows, want at least the seeded one", repaired)
	}

	subunits, credits, rate, source := readInvoiceRow(t, pool, id)
	if credits == nil || *credits != conflatedCredits {
		t.Fatalf("total_credits = %v, want %d", credits, conflatedCredits)
	}
	if subunits != 5247 {
		t.Fatalf("total_bdt_subunits = %d, want 5247 (BDT 52.47) at the pinned rate", subunits)
	}
	if rate == nil || *rate != "100.000000" {
		t.Fatalf("usd_bdt_rate = %v, want 100.000000", rate)
	}
	if source == nil || *source == "" {
		t.Fatalf("usd_bdt_rate_source = %v, want a recorded source", source)
	}

	// Reconcile against the ledger, which is the standard the issue sets: the
	// stored credit figure must equal what the account actually consumed in the
	// period, and the stored taka must be that quantity converted once.
	ledger, err := repo.AggregateByModel(ctx, accountID, period)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	ledgerCredits := new(big.Int)
	for _, row := range ledger {
		ledgerCredits.Add(ledgerCredits, row.Credits)
	}
	if ledgerCredits.Cmp(big.NewInt(*credits)) != 0 {
		t.Fatalf("repaired credits %d do not match the ledger's %s", *credits, ledgerCredits)
	}

	// Second pass writes nothing: the predicate no longer matches and the
	// UPDATE's own guard would refuse it anyway.
	before, _, _, _ := readInvoiceRow(t, pool, id)
	if _, err := svc.RepairUnconvertedInvoices(ctx); err != nil {
		t.Fatalf("second repair pass: %v", err)
	}
	after, afterCredits, afterRate, _ := readInvoiceRow(t, pool, id)
	if before != after {
		t.Fatalf("second pass moved the total: %d then %d", before, after)
	}
	if afterCredits == nil || *afterCredits != conflatedCredits {
		t.Fatalf("second pass moved the credits: %v", afterCredits)
	}
	if afterRate == nil || *afterRate != "100.000000" {
		t.Fatalf("second pass moved the rate: %v", afterRate)
	}
	stillPending, err := repo.ListUnconverted(ctx, 0)
	if err != nil {
		t.Fatalf("list unconverted after repair: %v", err)
	}
	if containsInvoice(stillPending, id) {
		t.Fatalf("row %s is still selectable after being repaired", id)
	}
}

// TestUpdateConverted_RefusesAnAlreadyConvertedRow_Live pins the database-side
// half of idempotence. Two control-plane replicas boot together on every
// deploy; without the WHERE guard the second would convert a converted amount a
// second time, dividing a customer's invoice by the rate again.
func TestUpdateConverted_RefusesAnAlreadyConvertedRow_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	accountID := seedInvoiceWorkspace(t, pool)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	period := Period{
		Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	id := insertConflatedRow(t, pool, accountID, period, conflatedCredits)

	first := Invoice{
		ID:               id,
		TotalBDTSubunits: big.NewInt(5247),
		TotalCredits:     big.NewInt(conflatedCredits),
		LineItems: []InvoiceLineItem{
			{ModelID: "hive-fast", RequestCount: 412, BDTSubunits: big.NewInt(5247), Credits: big.NewInt(conflatedCredits)},
		},
		USDBDTRate:       "100.000000",
		USDBDTRateSource: "env",
		PDFStorageKey:    storageKeyFor(accountID, period.Start),
	}
	wrote, err := repo.UpdateConverted(ctx, first)
	if err != nil {
		t.Fatalf("first update: %v", err)
	}
	if !wrote {
		t.Fatal("first update wrote nothing")
	}

	// A second writer arriving with a different amount must be refused, not
	// applied, and must say so rather than erroring.
	second := first
	second.TotalBDTSubunits = big.NewInt(1)
	second.USDBDTRate = "1.000000"
	wrote, err = repo.UpdateConverted(ctx, second)
	if err != nil {
		t.Fatalf("second update errored instead of reporting no-op: %v", err)
	}
	if wrote {
		t.Fatal("second update rewrote an already-converted row")
	}

	subunits, _, rate, _ := readInvoiceRow(t, pool, id)
	if subunits != 5247 {
		t.Fatalf("total_bdt_subunits = %d after a refused update, want 5247", subunits)
	}
	if rate == nil || *rate != "100.000000" {
		t.Fatalf("usd_bdt_rate = %v after a refused update, want 100.000000", rate)
	}
}

// TestRepairLeavesConvertedRowsUntouched_Live is the money bound at the
// database: a row that already carries a rate is never selected, so it cannot
// be rewritten by any pass.
func TestRepairLeavesConvertedRowsUntouched_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	accountID := seedInvoiceWorkspace(t, pool)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	period := Period{
		Start: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}
	correct := Invoice{
		WorkspaceID:      accountID,
		PeriodStart:      period.Start,
		PeriodEnd:        period.End,
		TotalBDTSubunits: big.NewInt(6400),
		TotalCredits:     big.NewInt(519_800_000),
		LineItems: []InvoiceLineItem{
			{ModelID: "hive-fast", RequestCount: 12, BDTSubunits: big.NewInt(6400), Credits: big.NewInt(519_800_000)},
		},
		USDBDTRate:       "123.130000",
		USDBDTRateSource: "fx_snapshot",
		PDFStorageKey:    storageKeyFor(accountID, period.Start),
	}
	saved, err := repo.InsertOrFetch(ctx, correct)
	if err != nil {
		t.Fatalf("seed correct row: %v", err)
	}

	pending, err := repo.ListUnconverted(ctx, 0)
	if err != nil {
		t.Fatalf("list unconverted: %v", err)
	}
	if containsInvoice(pending, saved.ID) {
		t.Fatalf("a correct row %s was selected for repair", saved.ID)
	}

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	if _, err := svc.RepairUnconvertedInvoices(ctx); err != nil {
		t.Fatalf("repair: %v", err)
	}

	subunits, credits, rate, source := readInvoiceRow(t, pool, saved.ID)
	if subunits != 6400 || credits == nil || *credits != 519_800_000 {
		t.Fatalf("a correct row moved: subunits=%d credits=%v", subunits, credits)
	}
	if rate == nil || *rate != "123.130000" || source == nil || *source != "fx_snapshot" {
		t.Fatalf("a correct row's provenance moved: rate=%v source=%v", rate, source)
	}
}

// TestUpdateConverted_RefusesARateWithNoProvenance_Live covers both halves of
// the same invariant: the application refuses the write, and the schema refuses
// it too, so a future writer that bypasses this repository cannot store a rate
// nobody can attribute.
func TestUpdateConverted_RefusesARateWithNoProvenance_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	accountID := seedInvoiceWorkspace(t, pool)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	period := Period{
		Start: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	id := insertConflatedRow(t, pool, accountID, period, conflatedCredits)

	_, err := repo.UpdateConverted(ctx, Invoice{
		ID:               id,
		TotalBDTSubunits: big.NewInt(5247),
		TotalCredits:     big.NewInt(conflatedCredits),
		USDBDTRate:       "100.000000",
		USDBDTRateSource: "",
		PDFStorageKey:    storageKeyFor(accountID, period.Start),
	})
	if err == nil {
		t.Fatal("repository accepted a rate with no recorded source on the repair path")
	}

	// The same seam guards generation, which is the other writer of this table
	// and had the identical hole.
	if _, err := repo.InsertOrFetch(ctx, Invoice{
		WorkspaceID:      accountID,
		PeriodStart:      time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:        time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		TotalBDTSubunits: big.NewInt(100),
		USDBDTRate:       "100.000000",
	}); err == nil {
		t.Fatal("repository accepted a rate with no recorded source on the generation path")
	}

	// The schema half, reached directly so the application guard above cannot
	// be the only thing holding the invariant.
	if _, err := pool.Exec(ctx, `
		UPDATE public.invoices SET usd_bdt_rate = 100.000000 WHERE id = $1
	`, id); err == nil {
		t.Fatal("the database accepted a rate with a NULL usd_bdt_rate_source")
	}
}

// TestListUnconverted_SkipsAnUndecodableRow_Live keeps one unreadable legacy
// row from blocking the repair of every other row on every boot. The population
// this pass reads was written by code that no longer exists, so a row that does
// not match today's decoder is the shape to expect, and the failure must be
// partial rather than total.
func TestListUnconverted_SkipsAnUndecodableRow_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	accountID := seedInvoiceWorkspace(t, pool)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	badPeriod := Period{
		Start: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
	}
	// bdt_subunits as a JSON number rather than the string the decoder expects.
	var badID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.invoices (
			id, workspace_id, period_start, period_end,
			total_bdt_subunits, line_items, pdf_storage_key
		)
		VALUES (gen_random_uuid(), $1, $2, $3, 1000,
		        jsonb_build_array(jsonb_build_object(
		            'model_id', 'hive-fast', 'request_count', 1, 'bdt_subunits', 1000)),
		        $4)
		RETURNING id
	`, accountID, badPeriod.Start, badPeriod.End,
		storageKeyFor(accountID, badPeriod.Start)).Scan(&badID); err != nil {
		t.Fatalf("seed undecodable row: %v", err)
	}

	goodPeriod := Period{
		Start: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
	}
	goodID := insertConflatedRow(t, pool, accountID, goodPeriod, conflatedCredits)
	// The ledger the readable row is reconciled against. Since issue #1702 the
	// repair refuses to write a credit figure the ledger does not support, so a
	// row seeded without one is refused rather than repaired and this test
	// would be asserting the guard instead of the skip it is about.
	seedUsageCharge(t, pool, accountID, conflatedCredits, "hive-fast", goodPeriod.Start.Add(24*time.Hour))

	rows, err := repo.ListUnconverted(ctx, 0)
	if err != nil {
		t.Fatalf("one undecodable row aborted the whole scan: %v", err)
	}
	if containsInvoice(rows, badID) {
		t.Fatalf("undecodable row %s was returned rather than skipped", badID)
	}
	if !containsInvoice(rows, goodID) {
		t.Fatalf("readable row %s was lost alongside the undecodable one", goodID)
	}

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	if _, err := svc.RepairUnconvertedInvoices(ctx); err != nil {
		t.Fatalf("repair: %v", err)
	}
	subunits, credits, rate, _ := readInvoiceRow(t, pool, goodID)
	if subunits != 5247 || credits == nil || *credits != conflatedCredits || rate == nil {
		t.Fatalf("readable row was not repaired alongside the skipped one: subunits=%d credits=%v rate=%v",
			subunits, credits, rate)
	}
}

func containsInvoice(rows []Invoice, id uuid.UUID) bool {
	for _, row := range rows {
		if row.ID == id {
			return true
		}
	}
	return false
}
