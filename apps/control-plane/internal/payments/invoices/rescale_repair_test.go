package invoices

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Issue #1702 — the pre-rescale scale error the #1682 repair introduced.
//
// The #1682 pass read every NULL-rate row as a credit count at TODAY's peg.
// Three July 2026 rows predate the credit unit rescale (D-046, migration
// 20260823_40), when the peg was 100,000 credits per USD rather than
// 1,000,000,000, so their stored integers were understated by exactly the
// rescale factor of 10,000. The write is one way, so those rows are now frozen
// outside the predicate that produced them.
//
// The magnitudes below are the live ones, read off the demo box on 2026-09-02:
// invoice 49a53ec7 stores 84,276 credits where its own ledger holds
// 842,760,000, and bills 1 paisa where it owes 10,377. The August row beside it
// reconciles exactly and must not move.
// =============================================================================

const (
	// preRescaleStoredCredits is what invoice 49a53ec7 holds today.
	preRescaleStoredCredits int64 = 84_276
	// preRescaleLedgerCredits is what the ledger holds for the same account and
	// period. The ledger WAS rescaled by 20260823_40; the invoice row was not,
	// because at the time its credit figure was sitting in a column named after
	// taka and the rescale could not see it.
	preRescaleLedgerCredits int64 = 842_760_000
	// preRescaleExpectedSubunits is 842,760,000 credits (0.84276 USD) at the
	// rate already stored on that row: 103.7690388 BDT, 10376.90388 paisa,
	// rounded half up.
	preRescaleExpectedSubunits int64 = 10_377

	// postRescaleCredits and postRescaleSubunits are live invoice 30752857, an
	// August row that reconciles exactly today and is the control in every test
	// below.
	postRescaleCredits  int64 = 5_563_529
	postRescaleSubunits int64 = 69

	// storedRate is the rate the #1682 repair stamped on all thirteen rows.
	storedRate       = "123.130000"
	storedRateSource = "default"
)

// testRescaleBoundary is the live value of public.credit_unit_rescale.applied_at
// on the demo box, the clock_timestamp() upper bound migration 20260823_40
// recorded at the end of its own work.
var testRescaleBoundary = time.Date(2026, 8, 24, 0, 22, 12, 954_624_000, time.UTC)

func julyPeriod() Period {
	return Period{
		Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
}

// ---------- fake repository support for the rescale pass ----------

func (f *fakeRepo) CreditRescaleAppliedAt(_ context.Context) (time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rescaleErr != nil {
		return time.Time{}, false, f.rescaleErr
	}
	if f.rescaleAppliedAt.IsZero() {
		return time.Time{}, false, nil
	}
	return f.rescaleAppliedAt, true, nil
}

func (f *fakeRepo) ListPreRescale(_ context.Context, boundary time.Time, limit int) ([]Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Invoice
	for _, inv := range f.byID {
		if inv.TotalCredits == nil || inv.PeriodEnd.After(boundary) {
			continue
		}
		out = append(out, cloneInvoice(inv))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateRescaled(_ context.Context, in Invoice, previousCredits *big.Int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rescaleUpdateErr != nil {
		return false, f.rescaleUpdateErr
	}
	existing, ok := f.byID[in.ID]
	if !ok {
		return false, nil
	}
	// Mirrors the SQL guard: the UPDATE pins total_credits to the value this
	// pass read, so a row another writer already scaled is left alone rather
	// than multiplied a second time.
	if existing.TotalCredits == nil || previousCredits == nil || existing.TotalCredits.Cmp(previousCredits) != 0 {
		return false, nil
	}
	f.rescaleUpdates++
	merged := existing
	merged.TotalBDTSubunits = in.TotalBDTSubunits
	merged.TotalCredits = in.TotalCredits
	merged.LineItems = in.LineItems
	merged.PDFStorageKey = in.PDFStorageKey
	f.byID[in.ID] = merged
	f.byWorkspaceMonth[wsMonthKey(merged.WorkspaceID, merged.PeriodStart)] = merged
	return true, nil
}

// seedRepairedInvoice writes a row in the state the #1682 pass leaves behind: a
// credit quantity, a taka amount derived from it, and the rate that related the
// two. That is the shape all thirteen live rows are in, correct and incorrect
// alike, which is why the ledger and not the rate is what tells them apart.
func seedRepairedInvoice(t *testing.T, repo *fakeRepo, ws uuid.UUID, period Period, credits, subunits int64) Invoice {
	t.Helper()
	inv := Invoice{
		ID:               uuid.New(),
		WorkspaceID:      ws,
		PeriodStart:      period.Start,
		PeriodEnd:        period.End,
		TotalBDTSubunits: big.NewInt(subunits),
		TotalCredits:     big.NewInt(credits),
		LineItems: []InvoiceLineItem{
			{ModelID: "unknown", RequestCount: 242, BDTSubunits: big.NewInt(subunits), Credits: big.NewInt(credits)},
		},
		PDFStorageKey:    storageKeyFor(ws, period.Start),
		GeneratedAt:      period.End.Add(2 * time.Hour),
		USDBDTRate:       storedRate,
		USDBDTRateSource: storedRateSource,
	}
	saved, err := repo.InsertOrFetch(context.Background(), inv)
	if err != nil {
		t.Fatalf("seed repaired invoice: %v", err)
	}
	return *saved
}

// ---------- tests ----------

// TestRepairPreRescaleInvoices_ScalesTheJulyRowAndLeavesTheAugustRowAlone is
// the issue in one fixture. Both rows carry a rate, both were written by the
// same pass, and only one of them is wrong: the ledger is what says which.
func TestRepairPreRescaleInvoices_ScalesTheJulyRowAndLeavesTheAugustRowAlone(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.rescaleAppliedAt = testRescaleBoundary
	storage := newFakeStorage()

	julyWS, augustWS := uuid.New(), uuid.New()
	july := seedRepairedInvoice(t, repo, julyWS, julyPeriod(), preRescaleStoredCredits, 1)
	august := seedRepairedInvoice(t, repo, augustWS, augustPeriod(), postRescaleCredits, postRescaleSubunits)
	repo.seedLedger(julyWS, julyPeriod().Start, preRescaleLedgerCredits)
	repo.seedLedger(augustWS, augustPeriod().Start, postRescaleCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{name: "Acme"}, nil)
	repaired, err := svc.RepairPreRescaleInvoices(context.Background())
	if err != nil {
		t.Fatalf("rescale repair: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired %d rows, want exactly the pre-rescale one", repaired)
	}

	got, err := repo.GetByID(context.Background(), july.ID)
	if err != nil {
		t.Fatalf("read back the july row: %v", err)
	}
	if got.TotalCredits == nil || got.TotalCredits.Cmp(big.NewInt(preRescaleLedgerCredits)) != 0 {
		t.Fatalf("july credits = %v, want the ledger's %d", got.TotalCredits, preRescaleLedgerCredits)
	}
	if got.TotalBDTSubunits.Cmp(big.NewInt(preRescaleExpectedSubunits)) != 0 {
		t.Fatalf("july total = %s subunits, want %d: the row still bills a pre-rescale figure",
			got.TotalBDTSubunits, preRescaleExpectedSubunits)
	}
	if got.USDBDTRate != storedRate || got.USDBDTRateSource != storedRateSource {
		t.Fatalf("july row was re-denominated: rate %q source %q, want %q / %q",
			got.USDBDTRate, got.USDBDTRateSource, storedRate, storedRateSource)
	}
	if len(got.LineItems) != 1 {
		t.Fatalf("july line items = %d, want 1", len(got.LineItems))
	}
	if line := got.LineItems[0]; line.Credits == nil || line.Credits.Cmp(big.NewInt(preRescaleLedgerCredits)) != 0 {
		t.Fatalf("july line credits = %v, want %d", line.Credits, preRescaleLedgerCredits)
	}
	if line := got.LineItems[0]; line.BDTSubunits.Cmp(big.NewInt(preRescaleExpectedSubunits)) != 0 {
		t.Fatalf("july line amount = %s, want %d", line.BDTSubunits, preRescaleExpectedSubunits)
	}

	// The correct row must not move. Read it back rather than asserting that
	// the predicate was scoped carefully: this whole issue is what a carefully
	// scoped predicate looked like.
	stillCorrect, err := repo.GetByID(context.Background(), august.ID)
	if err != nil {
		t.Fatalf("read back the august row: %v", err)
	}
	if stillCorrect.TotalCredits.Cmp(big.NewInt(postRescaleCredits)) != 0 {
		t.Fatalf("august credits moved to %s", stillCorrect.TotalCredits)
	}
	if stillCorrect.TotalBDTSubunits.Cmp(big.NewInt(postRescaleSubunits)) != 0 {
		t.Fatalf("august total moved to %s subunits", stillCorrect.TotalBDTSubunits)
	}
	if repo.rescaleUpdates != 1 {
		t.Fatalf("issued %d rescale updates, want 1", repo.rescaleUpdates)
	}

	// The customer downloads the object, not the row.
	if len(storage.uploads) != 1 {
		t.Fatalf("uploaded %d PDFs, want 1: %v", len(storage.uploads), keysOf(storage.uploads))
	}
	if _, ok := storage.uploads[FilesBucket+"|"+july.PDFStorageKey]; !ok {
		t.Fatalf("regenerated PDF did not overwrite %q; uploads: %v", july.PDFStorageKey, keysOf(storage.uploads))
	}
}

// TestRepairPreRescaleInvoices_IsIdempotent runs the pass twice. The second one
// must write nothing: a row that already agrees with its ledger is not a
// candidate, which is what makes this safe to leave running on every boot.
func TestRepairPreRescaleInvoices_IsIdempotent(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.rescaleAppliedAt = testRescaleBoundary
	storage := newFakeStorage()

	ws := uuid.New()
	seeded := seedRepairedInvoice(t, repo, ws, julyPeriod(), preRescaleStoredCredits, 1)
	repo.seedLedger(ws, julyPeriod().Start, preRescaleLedgerCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)

	first, err := svc.RepairPreRescaleInvoices(context.Background())
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	afterFirst, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back after the first pass: %v", err)
	}

	second, err := svc.RepairPreRescaleInvoices(context.Background())
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	afterSecond, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back after the second pass: %v", err)
	}

	if first != 1 || second != 0 {
		t.Fatalf("repaired counts = (%d, %d), want (1, 0)", first, second)
	}
	if afterFirst.TotalCredits.Cmp(afterSecond.TotalCredits) != 0 {
		t.Fatalf("credits moved on the second pass: %s then %s", afterFirst.TotalCredits, afterSecond.TotalCredits)
	}
	if afterFirst.TotalBDTSubunits.Cmp(afterSecond.TotalBDTSubunits) != 0 {
		t.Fatalf("total moved on the second pass: %s then %s", afterFirst.TotalBDTSubunits, afterSecond.TotalBDTSubunits)
	}
	if repo.rescaleUpdates != 1 {
		t.Fatalf("issued %d rescale updates across two passes, want 1", repo.rescaleUpdates)
	}
	if len(storage.uploads) != 1 {
		t.Fatalf("uploaded %d objects across two passes, want 1", len(storage.uploads))
	}
}

// TestRepairPreRescaleInvoices_RefusesARowTheLedgerDoesNotSupport is the guard
// the issue asks for as the general one. Neither the stored figure nor the
// stored figure times the rescale factor matches what the account actually
// consumed, so this pass has no basis for a number and must write none.
func TestRepairPreRescaleInvoices_RefusesARowTheLedgerDoesNotSupport(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.rescaleAppliedAt = testRescaleBoundary
	storage := newFakeStorage()

	ws := uuid.New()
	seeded := seedRepairedInvoice(t, repo, ws, julyPeriod(), preRescaleStoredCredits, 1)
	repo.seedLedger(ws, julyPeriod().Start, 500_000_000)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	repaired, err := svc.RepairPreRescaleInvoices(context.Background())
	if err != nil {
		t.Fatalf("pass reported an error for one refused row: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repaired %d rows the ledger does not support, want 0", repaired)
	}

	got, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.TotalCredits.Cmp(big.NewInt(preRescaleStoredCredits)) != 0 {
		t.Fatalf("a refused row was rewritten to %s credits", got.TotalCredits)
	}
	if repo.rescaleUpdates != 0 {
		t.Fatalf("issued %d updates for a refused row", repo.rescaleUpdates)
	}
	if len(storage.uploads) != 0 {
		t.Fatalf("regenerated %d PDFs for a refused row", len(storage.uploads))
	}
}

// TestRepairPreRescaleInvoices_DoesNothingWhenTheRescaleNeverRan covers the
// database that never applied 20260823_40. With no boundary there is no such
// thing as a pre-rescale row, and guessing one from a filename is exactly the
// assumption this fix exists to remove.
func TestRepairPreRescaleInvoices_DoesNothingWhenTheRescaleNeverRan(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	storage := newFakeStorage()

	ws := uuid.New()
	seeded := seedRepairedInvoice(t, repo, ws, julyPeriod(), preRescaleStoredCredits, 1)
	repo.seedLedger(ws, julyPeriod().Start, preRescaleLedgerCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	repaired, err := svc.RepairPreRescaleInvoices(context.Background())
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repaired %d rows against a database that never rescaled, want 0", repaired)
	}
	got, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.TotalCredits.Cmp(big.NewInt(preRescaleStoredCredits)) != 0 {
		t.Fatalf("row moved to %s credits with no rescale boundary to justify it", got.TotalCredits)
	}
}

// TestRepairUnconvertedInvoices_ScalesAPreRescaleRowToTheLedger fixes the root
// cause rather than only its three victims: the #1682 pass itself assumed the
// current peg. A NULL-rate row from before the rescale must be read at the peg
// it was written at, which the ledger is the authority on.
func TestRepairUnconvertedInvoices_ScalesAPreRescaleRowToTheLedger(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	storage := newFakeStorage()
	ws := uuid.New()

	repo.seedLedger(ws, julyPeriod().Start, preRescaleLedgerCredits)
	seeded := seedConflatedInvoiceForPeriod(t, repo, ws, julyPeriod(), preRescaleStoredCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	repaired, err := svc.RepairUnconvertedInvoices(context.Background())
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("repaired %d rows, want 1", repaired)
	}

	got, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.TotalCredits == nil || got.TotalCredits.Cmp(big.NewInt(preRescaleLedgerCredits)) != 0 {
		t.Fatalf("credits = %v, want the ledger's %d: the pass read a pre-rescale figure at today's peg",
			got.TotalCredits, preRescaleLedgerCredits)
	}
	// 842,760,000 credits is 0.84276 USD; at the 100 BDT per USD this package's
	// TestMain pins that is 84.276 BDT, 8427.6 paisa, rounded half up.
	if got.TotalBDTSubunits.Cmp(big.NewInt(8428)) != 0 {
		t.Fatalf("total = %s subunits, want 8428", got.TotalBDTSubunits)
	}
}

// TestRepairUnconvertedInvoices_RefusesARowTheLedgerDoesNotSupport is the same
// general guard on the #1682 pass. The line-item comparison it shipped with
// cannot catch a correct decode of a differently scaled number, because both
// sides of that comparison are in the same wrong scale.
func TestRepairUnconvertedInvoices_RefusesARowTheLedgerDoesNotSupport(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	storage := newFakeStorage()
	ws := uuid.New()

	repo.seedLedger(ws, julyPeriod().Start, 7_777_777)
	seeded := seedConflatedInvoiceForPeriod(t, repo, ws, julyPeriod(), preRescaleStoredCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	repaired, err := svc.RepairUnconvertedInvoices(context.Background())
	if err != nil {
		t.Fatalf("pass reported an error for one refused row: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repaired %d rows the ledger does not support, want 0", repaired)
	}

	got, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.USDBDTRate != "" {
		t.Fatalf("a row the ledger contradicts was stamped with rate %q and frozen", got.USDBDTRate)
	}
	if repo.updates != 0 {
		t.Fatalf("issued %d updates for a refused row", repo.updates)
	}
	if len(storage.uploads) != 0 {
		t.Fatalf("regenerated %d PDFs for a refused row", len(storage.uploads))
	}
}

// TestWithinLedgerTolerance pins the tolerance itself, since every refusal
// above is decided by it and a silently widened one would let the next scale
// error through.
func TestWithinLedgerTolerance(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		figure, ledger int64
		want           bool
	}{
		{"exact", 842_760_000, 842_760_000, true},
		{"inside the tolerance", 842_760_000, 842_000_000, true},
		{"outside the tolerance", 842_760_000, 800_000_000, false},
		{"the rescale factor is far outside it", 84_276, 842_760_000, false},
		{"an empty ledger admits only zero", 1, 0, false},
		{"zero against an empty ledger", 0, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := withinLedgerTolerance(big.NewInt(tc.figure), big.NewInt(tc.ledger))
			if got != tc.want {
				t.Fatalf("withinLedgerTolerance(%d, %d) = %v, want %v", tc.figure, tc.ledger, got, tc.want)
			}
		})
	}
}

// TestRepairPreRescaleInvoices_RefusesARowWithNoRate holds the boundary between
// the two passes. This one writes no rate, so a row that carries none has
// nothing to denominate its corrected taka at: converting at a freshly resolved
// rate would store taka the write then cannot explain, in a row the unconverted
// pass still selects as holding credits. Refused rather than resolved.
func TestRepairPreRescaleInvoices_RefusesARowWithNoRate(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.rescaleAppliedAt = testRescaleBoundary
	storage := newFakeStorage()

	ws := uuid.New()
	seeded := seedRepairedInvoice(t, repo, ws, julyPeriod(), preRescaleStoredCredits, 1)
	rateless := seeded
	rateless.USDBDTRate = ""
	rateless.USDBDTRateSource = ""
	repo.byID[seeded.ID] = rateless
	repo.byWorkspaceMonth[wsMonthKey(rateless.WorkspaceID, rateless.PeriodStart)] = rateless
	repo.seedLedger(ws, julyPeriod().Start, preRescaleLedgerCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	repaired, err := svc.RepairPreRescaleInvoices(context.Background())
	if err != nil {
		t.Fatalf("pass reported an error for one refused row: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repaired %d rate-less rows, want 0", repaired)
	}

	got, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.TotalCredits.Cmp(big.NewInt(preRescaleStoredCredits)) != 0 {
		t.Fatalf("a rate-less row was rewritten to %s credits", got.TotalCredits)
	}
	if got.TotalBDTSubunits.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("a rate-less row's taka moved to %s", got.TotalBDTSubunits)
	}
	if repo.rescaleUpdates != 0 || len(storage.uploads) != 0 {
		t.Fatalf("wrote %d rows and %d PDFs for a refused row", repo.rescaleUpdates, len(storage.uploads))
	}
}
