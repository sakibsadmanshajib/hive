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
// Live-schema coverage for the issue #1702 pre-rescale repair.
//
// The in-memory suite proves the arithmetic and the refusals. It cannot prove
// that the period predicate selects a row out of Postgres, that the boundary is
// actually readable from public.credit_unit_rescale, or that the UPDATE's own
// total_credits guard makes a second write a no-op at the database rather than
// only in the fake. Those are Postgres's opinions, and this defect exists
// because nobody asked Postgres.
//
// Gated on HIVE_TEST_DB_URL like every other live suite here.
// =============================================================================

// insertRepairedRow writes the shape the #1682 pass leaves behind, straight
// through SQL, because the repository can no longer produce a mis-scaled row.
func insertRepairedRow(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, period Period, credits, subunits int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO public.invoices (
			id, workspace_id, period_start, period_end,
			total_bdt_subunits, line_items, pdf_storage_key,
			usd_bdt_rate, usd_bdt_rate_source, total_credits
		)
		VALUES (gen_random_uuid(), $1, $2, $3, $4,
		        jsonb_build_array(jsonb_build_object(
		            'model_id', 'unknown',
		            'request_count', 242,
		            'bdt_subunits', $5::text,
		            'credits', $6::text)),
		        $7, $8::numeric, $9, $10)
		RETURNING id
	`, accountID, period.Start, period.End, subunits,
		strconv.FormatInt(subunits, 10), strconv.FormatInt(credits, 10),
		storageKeyFor(accountID, period.Start), storedRate, storedRateSource, credits).Scan(&id)
	if err != nil {
		t.Fatalf("seed repaired invoice: %v", err)
	}
	return id
}

// rescaleBoundary reads the boundary the pass will use, so the periods this
// test seeds sit provably on either side of it rather than on either side of a
// date the test assumed.
func rescaleBoundary(t *testing.T, repo Repository) time.Time {
	t.Helper()
	at, ok, err := repo.CreditRescaleAppliedAt(context.Background())
	if err != nil {
		t.Fatalf("read the rescale boundary: %v", err)
	}
	if !ok {
		t.Fatal("no credit unit rescale recorded on a database carrying the full migration chain")
	}
	return at
}

// TestRepairPreRescaleInvoices_Live is the acceptance check: a pre-rescale row
// and a post-rescale row go in, the pass runs twice, and both rows are read
// straight back out of the table and reconciled against the ledger. Reading the
// row back is the whole point; this defect shipped green because nobody did.
func TestRepairPreRescaleInvoices_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	boundary := rescaleBoundary(t, repo)

	// One month closed before the rescale and one closing after it, derived
	// from the boundary the database reports rather than from a literal.
	preStart := boundary.UTC().AddDate(0, 0, -60).Truncate(24 * time.Hour)
	pre := Period{Start: preStart, End: preStart.AddDate(0, 1, 0)}
	postStart := boundary.UTC().AddDate(0, 0, 1).Truncate(24 * time.Hour)
	post := Period{Start: postStart, End: postStart.AddDate(0, 1, 0)}
	if !pre.End.Before(boundary) {
		t.Fatalf("the pre-rescale period %s does not close before the boundary %s", pre.End, boundary)
	}
	if !post.End.After(boundary) {
		t.Fatalf("the post-rescale period %s does not close after the boundary %s", post.End, boundary)
	}

	preWS := seedInvoiceWorkspace(t, pool)
	postWS := seedInvoiceWorkspace(t, pool)
	preID := insertRepairedRow(t, pool, preWS, pre, preRescaleStoredCredits, 1)
	postID := insertRepairedRow(t, pool, postWS, post, postRescaleCredits, postRescaleSubunits)

	// The ledger both rows are checked against. The rescale multiplied the
	// ledger and not the invoice, which is the whole defect: the pre-rescale
	// account's entries are 10,000 times its stored figure.
	seedUsageCharge(t, pool, preWS, preRescaleLedgerCredits, "unknown", pre.Start.Add(24*time.Hour))
	seedUsageCharge(t, pool, postWS, postRescaleCredits, "unknown", post.Start.Add(24*time.Hour))

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{name: "Acme"}, nil)

	// Sanity: the row is selectable by the period predicate before the pass,
	// which is what makes its correction below mean something.
	candidates, err := repo.ListPreRescale(ctx, boundary, 0, uuid.Nil)
	if err != nil {
		t.Fatalf("list pre-rescale: %v", err)
	}
	if !containsInvoice(candidates, preID) {
		t.Fatalf("seeded pre-rescale row %s is not selectable by the period predicate", preID)
	}
	if containsInvoice(candidates, postID) {
		t.Fatalf("post-rescale row %s is selectable by a predicate that must not reach it", postID)
	}

	if _, err := svc.RepairPreRescaleInvoices(ctx); err != nil {
		t.Fatalf("rescale repair: %v", err)
	}

	subunits, credits, rate, source := readInvoiceRow(t, pool, preID)
	if credits == nil || *credits != preRescaleLedgerCredits {
		t.Fatalf("total_credits = %v, want the ledger's %d", credits, preRescaleLedgerCredits)
	}
	if subunits != preRescaleExpectedSubunits {
		t.Fatalf("total_bdt_subunits = %d, want %d at the rate already on the row", subunits, preRescaleExpectedSubunits)
	}
	if rate == nil || *rate != storedRate || source == nil || *source != storedRateSource {
		t.Fatalf("the row was re-denominated: rate %v source %v", rate, source)
	}

	// Reconcile the written figure against the ledger through the same
	// aggregate the repair used, out of the database.
	ledger, err := repo.AggregateByModel(ctx, preWS, pre)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	ledgerCredits := new(big.Int)
	for _, row := range ledger {
		ledgerCredits.Add(ledgerCredits, row.Credits)
	}
	if ledgerCredits.Cmp(big.NewInt(*credits)) != 0 {
		t.Fatalf("corrected credits %d do not match the ledger's %s", *credits, ledgerCredits)
	}

	// The correct row did not move. Read it back rather than trusting the
	// predicate that was supposed to exclude it.
	postSubunits, postCredits, _, _ := readInvoiceRow(t, pool, postID)
	if postCredits == nil || *postCredits != postRescaleCredits {
		t.Fatalf("post-rescale credits moved to %v", postCredits)
	}
	if postSubunits != postRescaleSubunits {
		t.Fatalf("post-rescale total moved to %d", postSubunits)
	}

	// Second pass: the row now agrees with its ledger, so it is not a
	// candidate for correction and nothing is written.
	if _, err := svc.RepairPreRescaleInvoices(ctx); err != nil {
		t.Fatalf("second rescale pass: %v", err)
	}
	againSubunits, againCredits, _, _ := readInvoiceRow(t, pool, preID)
	if againSubunits != subunits {
		t.Fatalf("second pass moved the total: %d then %d", subunits, againSubunits)
	}
	if againCredits == nil || *againCredits != *credits {
		t.Fatalf("second pass moved the credits: %d then %v", *credits, againCredits)
	}
}

// TestUpdateRescaled_RefusesAWriteAgainstAMovedQuantity_Live pins the
// database-side half of idempotence. The UPDATE names the quantity it read, so
// a second writer arriving with a stale reading changes nothing rather than
// multiplying a customer's invoice by the rescale factor a second time.
func TestUpdateRescaled_RefusesAWriteAgainstAMovedQuantity_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	accountID := seedInvoiceWorkspace(t, pool)
	period := Period{
		Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	id := insertRepairedRow(t, pool, accountID, period, preRescaleStoredCredits, 1)

	corrected := Invoice{
		ID:               id,
		TotalBDTSubunits: big.NewInt(preRescaleExpectedSubunits),
		TotalCredits:     big.NewInt(preRescaleLedgerCredits),
		LineItems: []InvoiceLineItem{{
			ModelID:      "unknown",
			RequestCount: 242,
			BDTSubunits:  big.NewInt(preRescaleExpectedSubunits),
			Credits:      big.NewInt(preRescaleLedgerCredits),
		}},
	}
	wrote, err := repo.UpdateRescaled(ctx, corrected, big.NewInt(preRescaleStoredCredits))
	if err != nil {
		t.Fatalf("first rescale write: %v", err)
	}
	if !wrote {
		t.Fatal("first rescale write changed nothing")
	}

	// A second writer holding the original reading must be refused.
	doubled := corrected
	doubled.TotalCredits = new(big.Int).Mul(big.NewInt(preRescaleLedgerCredits), big.NewInt(preRescaleCreditFactor))
	doubled.TotalBDTSubunits = big.NewInt(999_999)
	wrote, err = repo.UpdateRescaled(ctx, doubled, big.NewInt(preRescaleStoredCredits))
	if err != nil {
		t.Fatalf("second rescale write errored instead of being refused: %v", err)
	}
	if wrote {
		t.Fatal("a second writer holding a stale quantity was allowed to rewrite the row")
	}

	subunits, credits, _, _ := readInvoiceRow(t, pool, id)
	if credits == nil || *credits != preRescaleLedgerCredits {
		t.Fatalf("total_credits = %v after a refused write, want %d", credits, preRescaleLedgerCredits)
	}
	if subunits != preRescaleExpectedSubunits {
		t.Fatalf("total_bdt_subunits = %d after a refused write, want %d", subunits, preRescaleExpectedSubunits)
	}
}

// liveJulyRow is one of the three invoices the demo box holds in the wrong
// unit, read off it on 2026-09-02.
type liveJulyRow struct {
	invoice        string // the live id, for the failure message
	storedCredits  int64  // what public.invoices holds today
	storedSubunits int64  // what it bills today
	ledgerCredits  int64  // what credit_ledger_entries holds for the same account and period
	wantSubunits   int64  // what it owes, at the rate already on the row
}

// TestRepairPreRescaleInvoices_ReplaysTheThreeLiveRows_Live runs the exact
// figures the demo box holds through the real pass against a database carrying
// the whole migration chain, and reads every row back out of the table.
//
// This is a replay and not the live box itself, which only a deploy can reach.
// It exists because the numbers, not the shape, are what went wrong: the
// generic fixtures above would still pass if the rescale factor were off, and
// these will not.
//
// The single line item per row is the live shape, not a convenience.
// convertLines rounds per line and then sums, so a row carrying several model
// buckets can land a paisa or two away from the figure a single-bucket
// conversion gives, and a replay with the wrong shape would assert the wrong
// total. Checked against the box on 2026-09-02: all three rows carry exactly
// one line item, bucketed under `unknown`, because the generator that wrote
// them had no model metadata to group by.
func TestRepairPreRescaleInvoices_ReplaysTheThreeLiveRows_Live(t *testing.T) {
	pool := newInvoicesTestPool(t)
	repo := NewPgxRepository(pool)
	ctx := context.Background()

	boundary := rescaleBoundary(t, repo)
	start := boundary.UTC().AddDate(0, 0, -60).Truncate(24 * time.Hour)
	period := Period{Start: start, End: start.AddDate(0, 1, 0)}
	if !period.End.Before(boundary) {
		t.Fatalf("the replay period %s does not close before the boundary %s", period.End, boundary)
	}

	// 842,760,000 credits is 0.84276 USD, 103.7690388 BDT at 123.13.
	// 20,000,000 is 0.02 USD, 2.4626 BDT. 1,100,000 is 0.0011 USD, 0.135443 BDT.
	rows := []liveJulyRow{
		{invoice: "49a53ec7", storedCredits: 84_276, storedSubunits: 1, ledgerCredits: 842_760_000, wantSubunits: 10_377},
		{invoice: "9a056d8a", storedCredits: 2_000, storedSubunits: 0, ledgerCredits: 20_000_000, wantSubunits: 246},
		{invoice: "a1942bcb", storedCredits: 110, storedSubunits: 0, ledgerCredits: 1_100_000, wantSubunits: 14},
	}

	ids := make([]uuid.UUID, len(rows))
	for i, row := range rows {
		ws := seedInvoiceWorkspace(t, pool)
		ids[i] = insertRepairedRow(t, pool, ws, period, row.storedCredits, row.storedSubunits)
		seedUsageCharge(t, pool, ws, row.ledgerCredits, "unknown", period.Start.Add(24*time.Hour))
	}

	svc := NewService(repo, newFakeStorage(), &stubPDF{}, &fakeAccess{}, &fakeNamer{name: "Acme"}, nil)
	if _, err := svc.RepairPreRescaleInvoices(ctx); err != nil {
		t.Fatalf("rescale repair: %v", err)
	}

	for i, row := range rows {
		subunits, credits, _, _ := readInvoiceRow(t, pool, ids[i])
		if credits == nil || *credits != row.ledgerCredits {
			t.Fatalf("invoice %s: total_credits = %v, want the ledger's %d", row.invoice, credits, row.ledgerCredits)
		}
		if subunits != row.wantSubunits {
			t.Fatalf("invoice %s: total_bdt_subunits = %d, want %d (it bills %d today)",
				row.invoice, subunits, row.wantSubunits, row.storedSubunits)
		}
	}

	// Second pass writes nothing, read back rather than assumed.
	if _, err := svc.RepairPreRescaleInvoices(ctx); err != nil {
		t.Fatalf("second rescale pass: %v", err)
	}
	for i, row := range rows {
		subunits, credits, _, _ := readInvoiceRow(t, pool, ids[i])
		if credits == nil || *credits != row.ledgerCredits || subunits != row.wantSubunits {
			t.Fatalf("invoice %s moved on the second pass: subunits=%d credits=%v", row.invoice, subunits, credits)
		}
	}
}
