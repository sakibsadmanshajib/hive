package invoices

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Issue #1682 — repairing the rows written before the #1648 fix.
//
// A row whose usd_bdt_rate is NULL holds a raw credit count in the column named
// total_bdt_subunits, and the same conflation in every line item. The repair
// reads that count as what it actually is, converts it once at a recorded rate,
// re-renders the stored PDF, and writes both back.
//
// The magnitudes below are the ones measured live on 2026-09-01: 524,653,338
// credits, about 0.52 USD, displayed to the owner as about 5.2 million taka.
// At the rate this package's TestMain pins (100 BDT per USD) the honest figure
// is 52.47 taka, so an assertion on the amount fails on the magnitude rather
// than on any label.
// =============================================================================

const conflatedCredits int64 = 524_653_338

// ---------- fake repository support for the repair path ----------

func (f *fakeRepo) ListUnconverted(_ context.Context, limit int) ([]Invoice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unconvertedErr != nil {
		return nil, f.unconvertedErr
	}
	var out []Invoice
	for _, inv := range f.byID {
		if inv.USDBDTRate != "" {
			continue
		}
		out = append(out, cloneInvoice(inv))
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeRepo) UpdateConverted(_ context.Context, in Invoice) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return false, f.updateErr
	}
	existing, ok := f.byID[in.ID]
	if !ok || existing.USDBDTRate != "" {
		// Mirrors the SQL guard: the UPDATE carries `usd_bdt_rate IS NULL`, so
		// a row someone else already repaired is never rewritten.
		return false, nil
	}
	f.updates++
	merged := existing
	merged.TotalBDTSubunits = in.TotalBDTSubunits
	merged.TotalCredits = in.TotalCredits
	merged.LineItems = in.LineItems
	merged.USDBDTRate = in.USDBDTRate
	merged.USDBDTRateSource = in.USDBDTRateSource
	merged.PDFStorageKey = in.PDFStorageKey
	f.byID[in.ID] = merged
	f.byWorkspaceMonth[wsMonthKey(merged.WorkspaceID, merged.PeriodStart)] = merged
	return true, nil
}

func cloneInvoice(in Invoice) Invoice {
	out := in
	out.LineItems = append([]InvoiceLineItem(nil), in.LineItems...)
	return out
}

// seedConflatedInvoice writes the shape the live demo box holds: taka columns
// carrying a credit count, no rate, and a PDF object already in the bucket.
func seedConflatedInvoice(t *testing.T, repo *fakeRepo, ws uuid.UUID, credits int64) Invoice {
	t.Helper()
	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
	inv := Invoice{
		ID:               uuid.New(),
		WorkspaceID:      ws,
		PeriodStart:      period.Start,
		PeriodEnd:        period.End,
		TotalBDTSubunits: big.NewInt(credits),
		LineItems: []InvoiceLineItem{
			{ModelID: "hive-fast", RequestCount: 412, BDTSubunits: big.NewInt(credits)},
		},
		PDFStorageKey: storageKeyFor(ws, period.Start),
		GeneratedAt:   time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC),
	}
	saved, err := repo.InsertOrFetch(context.Background(), inv)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return *saved
}

// ---------- tests ----------

// TestRepairUnconvertedInvoices_ConvertsTheConflatedRow is the issue in one
// assertion: the stored figure stops being a credit count and becomes the taka
// the customer actually owes, the credit count moves to the column that means
// credits, and the row records the rate that related them.
func TestRepairUnconvertedInvoices_ConvertsTheConflatedRow(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	storage := newFakeStorage()
	ws := uuid.New()
	seeded := seedConflatedInvoice(t, repo, ws, conflatedCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{name: "Acme"}, nil)
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
	if got.TotalBDTSubunits.Cmp(big.NewInt(5247)) != 0 {
		t.Fatalf("total = %s subunits, want 5247 (BDT 52.47): the credit count is still being stored as paisa",
			got.TotalBDTSubunits)
	}
	if got.TotalCredits == nil || got.TotalCredits.Cmp(big.NewInt(conflatedCredits)) != 0 {
		t.Fatalf("total credits = %v, want %d", got.TotalCredits, conflatedCredits)
	}
	if got.USDBDTRate != "100.000000" {
		t.Fatalf("recorded rate = %q, want %q", got.USDBDTRate, "100.000000")
	}
	if got.USDBDTRateSource == "" {
		t.Fatal("repaired row records no rate source, so an operator cannot tell an account rate from the platform fallback")
	}
	if len(got.LineItems) != 1 {
		t.Fatalf("line items = %d, want 1", len(got.LineItems))
	}
	line := got.LineItems[0]
	if line.Credits == nil || line.Credits.Cmp(big.NewInt(conflatedCredits)) != 0 {
		t.Fatalf("line credits = %v, want %d", line.Credits, conflatedCredits)
	}
	if line.BDTSubunits.Cmp(big.NewInt(5247)) != 0 {
		t.Fatalf("line amount = %s subunits, want 5247", line.BDTSubunits)
	}

	// The customer downloads the stored object, not the row, so a repair that
	// leaves the old PDF in place has fixed nothing they can see.
	if len(storage.uploads) != 1 {
		t.Fatalf("uploaded %d PDFs, want 1: the stale object is still what a customer downloads", len(storage.uploads))
	}
	if _, ok := storage.uploads[FilesBucket+"|"+seeded.PDFStorageKey]; !ok {
		t.Fatalf("regenerated PDF did not overwrite %q; uploads: %v", seeded.PDFStorageKey, keysOf(storage.uploads))
	}
}

// TestRepairUnconvertedInvoices_IsIdempotent runs the repair twice and proves
// the second pass is a no-op, rather than arguing that it must be. A repair
// that converted an already-converted row would divide the amount by the rate
// again on every deploy.
func TestRepairUnconvertedInvoices_IsIdempotent(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	storage := newFakeStorage()
	ws := uuid.New()
	seeded := seedConflatedInvoice(t, repo, ws, conflatedCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{name: "Acme"}, nil)

	first, err := svc.RepairUnconvertedInvoices(context.Background())
	if err != nil {
		t.Fatalf("first pass: %v", err)
	}
	afterFirst, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back after first pass: %v", err)
	}

	second, err := svc.RepairUnconvertedInvoices(context.Background())
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	afterSecond, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back after second pass: %v", err)
	}

	if first != 1 || second != 0 {
		t.Fatalf("repaired counts = (%d, %d), want (1, 0)", first, second)
	}
	if afterFirst.TotalBDTSubunits.Cmp(afterSecond.TotalBDTSubunits) != 0 {
		t.Fatalf("total moved on the second pass: %s then %s",
			afterFirst.TotalBDTSubunits, afterSecond.TotalBDTSubunits)
	}
	if afterFirst.TotalCredits.Cmp(afterSecond.TotalCredits) != 0 {
		t.Fatalf("credits moved on the second pass: %s then %s",
			afterFirst.TotalCredits, afterSecond.TotalCredits)
	}
	if afterFirst.USDBDTRate != afterSecond.USDBDTRate {
		t.Fatalf("rate moved on the second pass: %q then %q", afterFirst.USDBDTRate, afterSecond.USDBDTRate)
	}
	if repo.updates != 1 {
		t.Fatalf("issued %d updates across two passes, want 1", repo.updates)
	}
	if len(storage.uploads) != 1 {
		t.Fatalf("uploaded %d objects across two passes, want 1", len(storage.uploads))
	}
}

// TestRepairUnconvertedInvoices_LeavesConvertedRowsUntouched holds the money
// bound: a row that already carries a rate is correct, and the repair must not
// read, rewrite or re-render it.
func TestRepairUnconvertedInvoices_LeavesConvertedRowsUntouched(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	storage := newFakeStorage()
	ws := uuid.New()
	period := Period{
		Start: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}
	converted := Invoice{
		ID:               uuid.New(),
		WorkspaceID:      ws,
		PeriodStart:      period.Start,
		PeriodEnd:        period.End,
		TotalBDTSubunits: big.NewInt(6400),
		LineItems: []InvoiceLineItem{
			{ModelID: "hive-fast", RequestCount: 12, BDTSubunits: big.NewInt(6400)},
		},
		PDFStorageKey: storageKeyFor(ws, period.Start),
		USDBDTRate:    "123.130000",
	}
	if _, err := repo.InsertOrFetch(context.Background(), converted); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	repaired, err := svc.RepairUnconvertedInvoices(context.Background())
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("repaired %d already-correct rows, want 0", repaired)
	}

	got, err := repo.GetByID(context.Background(), converted.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.TotalBDTSubunits.Cmp(big.NewInt(6400)) != 0 {
		t.Fatalf("a correct row moved: total = %s, want 6400", got.TotalBDTSubunits)
	}
	if got.USDBDTRate != "123.130000" {
		t.Fatalf("a correct row's rate moved: %q", got.USDBDTRate)
	}
	if repo.updates != 0 {
		t.Fatalf("issued %d updates against correct rows, want 0", repo.updates)
	}
	if len(storage.uploads) != 0 {
		t.Fatalf("re-rendered %d PDFs for correct rows, want 0", len(storage.uploads))
	}
}

// TestRepairUnconvertedInvoices_PrefersTheAccountSnapshotRate keeps the repair
// on the same rate policy generation uses: the account's own snapshot taken
// before the period closed, and the platform rate only when it has none. A
// repair denominated at a different rate than the row would have been generated
// at is a silent recomputation, which the money bound forbids.
func TestRepairUnconvertedInvoices_PrefersTheAccountSnapshotRate(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.fxRate = "50.000000"
	storage := newFakeStorage()
	ws := uuid.New()
	seeded := seedConflatedInvoice(t, repo, ws, conflatedCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	if _, err := svc.RepairUnconvertedInvoices(context.Background()); err != nil {
		t.Fatalf("repair: %v", err)
	}

	got, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// 524,653,338 credits is 0.524653338 USD; at 50 BDT per USD that is
	// 26.2326669 BDT, which is 2623.26669 paisa and rounds to 2623.
	if got.TotalBDTSubunits.Cmp(big.NewInt(2623)) != 0 {
		t.Fatalf("total = %s subunits, want 2623 at the account's own rate", got.TotalBDTSubunits)
	}
	if got.USDBDTRate != "50.000000" {
		t.Fatalf("recorded rate = %q, want %q", got.USDBDTRate, "50.000000")
	}
	if got.USDBDTRateSource != "fx_snapshot" {
		t.Fatalf("rate source = %q, want %q", got.USDBDTRateSource, "fx_snapshot")
	}
	if !repo.fxBefore.Equal(seeded.PeriodEnd) {
		t.Fatalf("snapshot bound = %s, want the period end %s: a later top-up must not re-denominate a closed month",
			repo.fxBefore, seeded.PeriodEnd)
	}
}

// TestRepairUnconvertedInvoices_DoesNotWriteTheRowWhenThePDFCannotBeStored
// keeps the two halves of a repair together. If the regenerated object cannot
// be written, the row stays conflated and NULL so the next pass retries it,
// rather than presenting a repaired figure in the console over a stale
// download.
func TestRepairUnconvertedInvoices_DoesNotWriteTheRowWhenThePDFCannotBeStored(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	storage := newFakeStorage()
	storage.failOnce = true
	storage.failErr = errors.New("bucket unavailable")
	ws := uuid.New()
	seeded := seedConflatedInvoice(t, repo, ws, conflatedCredits)

	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	repaired, err := svc.RepairUnconvertedInvoices(context.Background())
	if err != nil {
		t.Fatalf("repair reported a pass-level error for one failed row: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("counted %d repairs despite the upload failing", repaired)
	}

	got, err := repo.GetByID(context.Background(), seeded.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.USDBDTRate != "" {
		t.Fatalf("row was marked converted at %q while its PDF write failed", got.USDBDTRate)
	}
	if got.TotalBDTSubunits.Cmp(big.NewInt(conflatedCredits)) != 0 {
		t.Fatalf("row amounts moved without a stored PDF: %s", got.TotalBDTSubunits)
	}
	if repo.updates != 0 {
		t.Fatalf("issued %d updates despite the upload failing", repo.updates)
	}
}

// TestRepairUnconvertedInvoices_TerminatesOnAPermanentlyFailingRow guards the
// batch loop. A failed row keeps its NULL rate and is therefore returned by the
// next SELECT, so a loop that continued on batch size rather than on progress
// would fetch the same row forever. The pass must end and report what it could
// not do, not spin.
func TestRepairUnconvertedInvoices_TerminatesOnAPermanentlyFailingRow(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.updateErr = errors.New("write refused")
	ws := uuid.New()
	seedConflatedInvoice(t, repo, ws, conflatedCredits)

	svc := NewService(repo, newFakeStorage(), &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)

	done := make(chan struct{})
	var repaired int
	var err error
	go func() {
		repaired, err = svc.RepairUnconvertedInvoices(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("repair pass did not terminate on a row it can never write")
	}
	if err != nil {
		t.Fatalf("pass reported an error for one failed row: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("counted %d repairs while every write was refused", repaired)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
