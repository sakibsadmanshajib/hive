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
// Which rate an invoice is denominated at (issue #1648 review, HIGH).
//
// A BD customer buys credits through payments.FXService at the XE mid rate plus
// FXFeeRate, and that number is persisted to public.fx_snapshots. Invoicing the
// consumption of those same credits at a different rate would put a receipt and
// an invoice for the same money in disagreement, both of them authoritative
// looking. So the account's own snapshot wins, and the platform rate is only
// the fallback for an account that has never taken one.
// =============================================================================

const augustPeriodModel = "hive-fast"

func augustPeriod() Period {
	return Period{
		Start: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
}

func oneUSDOfCredits() []ModelCredits {
	return []ModelCredits{{
		ModelID:      augustPeriodModel,
		RequestCount: 1,
		Credits:      big.NewInt(1_000_000_000), // exactly 1 USD
	}}
}

// TestGenerateInvoice_PrefersTheAccountsPurchaseRate: one USD of credits, an
// account whose last checkout snapshot was 129.586500 BDT per USD. The invoice
// must be 12,958.65 paisa, the rate the customer actually paid, not the 100.00
// platform rate these tests pin.
func TestGenerateInvoice_PrefersTheAccountsPurchaseRate(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.fxRate = "129.586500" // as payments.FXService writes it: mid x 1.05
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		return oneUSDOfCredits(), nil
	}

	svc := NewService(repo, newFakeStorage(), &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	got, err := svc.GenerateInvoiceForPeriod(context.Background(), uuid.New(), augustPeriod())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// 1 USD at 129.5865 BDT per USD is 129.5865 taka, 12,958.65 paisa, half up.
	if want := big.NewInt(12_959); got.TotalBDTSubunits.Cmp(want) != 0 {
		t.Fatalf("total = %s subunits, want %s: the invoice ignored the rate the credits were bought at",
			got.TotalBDTSubunits, want)
	}
	if got.USDBDTRate != "129.586500" {
		t.Fatalf("recorded rate = %q, want the snapshot rate %q", got.USDBDTRate, "129.586500")
	}
}

// TestGenerateInvoice_FallsBackToPlatformRateWithoutSnapshot: an account that
// never bought through a BDT rail (granted or card-funded credits) has no rate
// of its own, and billing must not stop for want of one.
func TestGenerateInvoice_FallsBackToPlatformRateWithoutSnapshot(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.fxRate = "" // no snapshot for this account
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		return oneUSDOfCredits(), nil
	}

	svc := NewService(repo, newFakeStorage(), &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	got, err := svc.GenerateInvoiceForPeriod(context.Background(), uuid.New(), augustPeriod())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// 1 USD at the pinned platform rate of 100 BDT per USD is 100 taka.
	if want := big.NewInt(10_000); got.TotalBDTSubunits.Cmp(want) != 0 {
		t.Fatalf("total = %s subunits, want %s", got.TotalBDTSubunits, want)
	}
	if got.USDBDTRate != "100.000000" {
		t.Fatalf("recorded rate = %q, want %q", got.USDBDTRate, "100.000000")
	}
}

// TestGenerateInvoice_RefusesWhenTheSnapshotLookupFails: this fails closed, not
// open.
//
// `AggregateByModel` answered from the same pool a few lines earlier, so an
// error here is not transient connectivity but something structural, and those
// are the cases where quietly falling back to the platform rate is worst: the
// row would be denominated differently from every other invoice that account
// has received, and `USDBDTRate` records the rate but not the source, so
// nothing on the row could say why.
func TestGenerateInvoice_RefusesWhenTheSnapshotLookupFails(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.fxErr = errors.New("fx snapshot table unreachable")
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		return oneUSDOfCredits(), nil
	}

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	if _, err := svc.GenerateInvoiceForPeriod(context.Background(), uuid.New(), augustPeriod()); err == nil {
		t.Fatal("expected generation to stop when the fx snapshot lookup fails")
	}
	if len(storage.uploads) != 0 {
		t.Fatalf("wrote %d PDFs despite an unresolvable rate", len(storage.uploads))
	}
}

// TestGenerateInvoice_BoundsTheSnapshotLookupToThePeriod proves the period end
// reaches the query. Without it a top-up on the last day of the month
// re-denominates credits consumed on the second, and a correction-path
// regeneration in October prices an August invoice at October's rate. The rule
// this pins is that a closed month's rate is immutable; the SQL half is pinned
// by TestLatestUSDBDTRate_Live.
func TestGenerateInvoice_BoundsTheSnapshotLookupToThePeriod(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.fxRate = "129.586500"
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		return oneUSDOfCredits(), nil
	}

	svc := NewService(repo, newFakeStorage(), &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	period := augustPeriod()
	if _, err := svc.GenerateInvoiceForPeriod(context.Background(), uuid.New(), period); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !repo.fxBefore.Equal(period.End) {
		t.Fatalf("snapshot lookup bounded at %s, want the period end %s", repo.fxBefore, period.End)
	}
}

// TestGenerateInvoice_RefusesAnUnreadableSnapshotRate: a stored rate the money
// path cannot parse is not quietly swapped for the fallback. Something is wrong
// with the FX table, and inventing a different number is how a customer ends up
// invoiced at a rate nobody chose. Nothing is uploaded and nothing is written.
func TestGenerateInvoice_RefusesAnUnreadableSnapshotRate(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.fxRate = "one hundred and twenty three"
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		return oneUSDOfCredits(), nil
	}

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	if _, err := svc.GenerateInvoiceForPeriod(context.Background(), uuid.New(), augustPeriod()); err == nil {
		t.Fatal("expected generation to refuse a snapshot rate it cannot parse")
	}
	if len(storage.uploads) != 0 {
		t.Fatalf("wrote %d PDFs despite an unusable rate", len(storage.uploads))
	}
}

// TestGenerateInvoice_RefusesATotalBeyondBigintBeforeUploading: the column is a
// bigint, so an oversized total wraps on the way in and is rejected by its
// CHECK, after the PDF has already been written to storage. The guard runs
// before the render so the failure names the amount and leaves no orphan
// object in the bucket.
func TestGenerateInvoice_RefusesATotalBeyondBigintBeforeUploading(t *testing.T) {
	t.Parallel()

	repo := newFakeRepo()
	repo.aggregateFn = func(_ context.Context, _ uuid.UUID, _ Period) ([]ModelCredits, error) {
		// Credits worth more paisa than an int64 can hold at the pinned rate:
		// 1e26 credits is 1e17 USD, which is 1e21 paisa at 100 BDT per USD.
		huge := new(big.Int).Exp(big.NewInt(10), big.NewInt(26), nil)
		return []ModelCredits{{ModelID: "m", RequestCount: 1, Credits: huge}}, nil
	}

	storage := newFakeStorage()
	svc := NewService(repo, storage, &stubPDF{}, &fakeAccess{}, &fakeNamer{}, nil)
	if _, err := svc.GenerateInvoiceForPeriod(context.Background(), uuid.New(), augustPeriod()); err == nil {
		t.Fatal("expected a total beyond bigint to be refused")
	}
	if len(storage.uploads) != 0 {
		t.Fatalf("uploaded %d PDFs for an invoice that cannot be stored", len(storage.uploads))
	}
}
