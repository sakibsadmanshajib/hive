package invoices

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Credit unit fixtures and regression guard (issue #1648)
//
// The ledger stores credits, one billionth of a USD each. An invoice is
// denominated in taka. Converting between them is an FX step, and the service
// owns it. These tests pin the platform rate at 100 BDT per USD so every taka
// figure below is exact and independent of the deployment default.
//
// At that rate one paisa is 100,000 credits.
// =============================================================================

func TestMain(m *testing.M) {
	if err := os.Setenv("HIVE_USD_BDT_RATE", "100"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

// spendCredits returns the credit quantity worth the supplied BDT subunit
// amount at the pinned rate.
func spendCredits(subunits int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(subunits), big.NewInt(100_000))
}

// TestGenerateInvoiceForPeriod_ConvertsCreditsToTaka is the invoice half of
// issue #1648, pinned to the magnitude measured live on 2026-09-01.
//
// The workspace burned 524,653,338 credits in August 2026, about 0.52 USD,
// which the console reported as $0.525 of spend on its analytics page. The
// invoice page displayed ৳5,246,533.38, because the aggregate was stored as a
// paisa count without ever leaving the credit unit. At 100 BDT per USD the
// honest figure is ৳52.47.
//
// The assertion is on the amount, so it fails on the magnitude rather than on
// formatting: any implementation that carries the credit count through to the
// invoice row fails here even if every label around it says taka.
func TestGenerateInvoiceForPeriod_ConvertsCreditsToTaka(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	ws := uuid.New()
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		return []ModelCredits{
			{ModelID: "hive-fast", RequestCount: 412, Credits: big.NewInt(524_653_338)},
		}, nil
	}

	svc := NewService(repo, newFakeStorage(), &stubPDF{}, &fakeAccess{}, &fakeNamer{name: "Acme"}, nil)
	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}

	got, err := svc.GenerateInvoiceForPeriod(context.Background(), ws, period)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	want := big.NewInt(5247) // ৳52.47
	if got.TotalBDTSubunits.Cmp(want) != 0 {
		t.Fatalf("total = %s subunits, want %s: a credit count is being rendered as paisa",
			got.TotalBDTSubunits, want)
	}
	if got.TotalBDTSubunits.Cmp(big.NewInt(524_653_338)) == 0 {
		t.Fatal("invoice total is the raw credit count")
	}
	if got.USDBDTRate != "100" {
		t.Fatalf("recorded rate = %q, want %q", got.USDBDTRate, "100")
	}
}

// TestGenerateInvoiceForPeriod_TotalIsTheSumOfItsLines keeps the document
// internally consistent: a customer adding up the line items must reach the
// printed total, so the total is the sum of the rounded lines rather than a
// separately rounded aggregate.
func TestGenerateInvoiceForPeriod_TotalIsTheSumOfItsLines(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	ws := uuid.New()
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		// Three lines that each land on a half paisa: 150,000 credits is 1.5
		// paisa at the pinned rate, so each rounds up to 2 and the total is 6.
		// An implementation that summed the credits first and rounded once
		// would print 5 and the customer's own addition would disagree.
		return []ModelCredits{
			{ModelID: "a", RequestCount: 1, Credits: big.NewInt(150_000)},
			{ModelID: "b", RequestCount: 1, Credits: big.NewInt(150_000)},
			{ModelID: "c", RequestCount: 1, Credits: big.NewInt(150_000)},
		}, nil
	}

	svc := NewService(repo, newFakeStorage(), &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}

	got, err := svc.GenerateInvoiceForPeriod(context.Background(), ws, period)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	sum := new(big.Int)
	for _, item := range got.LineItems {
		if item.BDTSubunits == nil {
			t.Fatalf("line %q has no amount", item.ModelID)
		}
		sum.Add(sum, item.BDTSubunits)
	}
	if got.TotalBDTSubunits.Cmp(sum) != 0 {
		t.Fatalf("total %s does not equal the sum of its lines %s", got.TotalBDTSubunits, sum)
	}
	if got.TotalBDTSubunits.Cmp(big.NewInt(6)) != 0 {
		t.Fatalf("total = %s, want 6", got.TotalBDTSubunits)
	}
}

// TestGenerateInvoiceForPeriod_RefusesAnUnusableRate keeps generation fail
// closed. A misconfigured rate must stop the invoice, not produce one at a
// rate nobody chose.
func TestGenerateInvoiceForPeriod_RefusesAnUnusableRate(t *testing.T) {
	t.Setenv("HIVE_USD_BDT_RATE", "not-a-rate")

	repo := newFakeRepo()
	ws := uuid.New()
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		return []ModelCredits{{ModelID: "m", RequestCount: 1, Credits: big.NewInt(1_000_000_000)}}, nil
	}

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	period := Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}

	if _, err := svc.GenerateInvoiceForPeriod(context.Background(), ws, period); err == nil {
		t.Fatal("expected generation to refuse an unparseable rate")
	}
	if len(storage.uploads) != 0 {
		t.Fatalf("wrote %d PDFs despite an unusable rate", len(storage.uploads))
	}
}
