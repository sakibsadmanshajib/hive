package invoices

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// =============================================================================
// Issue #1702 — the scale error the issue #1682 repair introduced, and the
// general guard that would have caught it.
//
// The #1682 pass read every `usd_bdt_rate IS NULL` row as a credit count at
// TODAY's peg. Ten August 2026 rows were, and reconcile against the ledger
// exactly. Three July 2026 rows were not: they predate the credit unit rescale
// (D-046, migration 20260823_40), when one USD was 100,000 credits rather than
// 1,000,000,000, so their stored integers were understated by exactly the
// rescale factor of 10,000. Live invoice 49a53ec7 bills 1 paisa where its own
// ledger says 842,760,000 credits, about 103.77 taka.
//
// The rescale migration multiplied credit_ledger_entries.credits_delta by
// 10,000 along with every other credit column it could see. It could not see
// this one: at the time, the invoice's credit figure was sitting in a column
// named total_bdt_subunits, which the migration correctly read as taka and
// correctly left alone.
//
// WHY A SECOND PASS AND NOT A RE-RUN. The #1682 write is one way by design:
// setting usd_bdt_rate removes a row from `usd_bdt_rate IS NULL`, which is what
// makes that pass idempotent. The three rows now carry a rate, so they are
// frozen outside the predicate that produced them and no re-run can reach them.
//
// TWO CHANGES, NOT ONE.
//
//	The victims. RepairPreRescaleInvoices selects on the period rather than on
//	the rate, bounded by the rescale's own recorded application time read out of
//	the database, and corrects rows the ledger says are a rescale factor short.
//
//	The cause. Both passes now reconcile against credit_ledger_entries for the
//	row's own account and period before writing anything, and refuse a row whose
//	credit figure the ledger does not support. The line-item comparison #1682
//	shipped with could not have caught this and was not wrong to miss it: on a
//	pre-rescale row both sides of that comparison are in the same old scale, so
//	they agree. It defends against a decode producing a different number, not
//	against a correct decode of a differently scaled one. Only a figure from
//	outside the row can do that, and the ledger is the only such figure there
//	is.
//
// IDEMPOTENCE COMES FROM CONVERGENCE, NOT FROM A ONE-WAY FLAG. A corrected row
// agrees with its ledger, so the next pass computes a factor of one and writes
// nothing. That is deliberately not the #1682 shape: a marker that removes a
// row from its own predicate is exactly what made this defect unfixable in
// place, and it would make the next one unfixable too.
// =============================================================================

const (
	// preRescaleCreditFactor is the factor migration 20260823_40 multiplied
	// every credit column by: 1,000,000,000 / 100,000. It is a historical
	// constant and not derived from payments.CreditsPerUSD, because a future
	// change to that constant must not silently redefine what a July 2026 row
	// meant.
	preRescaleCreditFactor int64 = 10_000

	// creditRescaleMigrationFilename keys the fallback lookup in the applier's
	// own ledger. It is the KEY of a recorded timestamp, never a source of one:
	// the date in the filename is when the file was authored, which on the demo
	// box was a day before it ran.
	creditRescaleMigrationFilename = "20260823_40_credit_unit_rescale_billion.sql"

	// ledgerToleranceBasisPoints is how far a stored credit figure may sit from
	// the ledger before this package refuses to write it: 50 basis points, half
	// of one percent.
	//
	// Not zero, because an invoice is generated from a ledger read at a moment
	// in time and a late settlement landing inside a closed period afterwards
	// is an ordinary event that must not freeze a repair. Not loose either: the
	// error this exists to catch is four orders of magnitude, and every one of
	// the thirteen live rows agrees with its ledger exactly.
	ledgerToleranceBasisPoints int64 = 50

	// basisPointsPerWhole keeps the tolerance comparison in integers. Money
	// arithmetic in this package is math/big and never float.
	basisPointsPerWhole int64 = 10_000
)

// errRowAlreadyAtLedgerScale marks the ordinary outcome of a second pass: the
// row agrees with its ledger and there is nothing to do. It is not a failure
// and must not be logged as one, or every boot would report the ten correct
// August rows as errors.
var errRowAlreadyAtLedgerScale = errors.New("invoices: row already agrees with its ledger")

// RepairPreRescaleInvoices corrects invoices whose stored credit quantity is
// denominated in the pre-rescale unit, and returns how many rows it corrected.
//
// The candidate set is closed and cannot grow: it is the invoices whose period
// ended before the credit unit rescale was applied, and the rescale happened
// once, in the past. Rows in it that already agree with their ledger are
// skipped without a write, which is what makes this safe to run on every boot.
func (s *Service) RepairPreRescaleInvoices(ctx context.Context) (int, error) {
	boundary, ok, err := s.repo.CreditRescaleAppliedAt(ctx)
	if err != nil {
		return 0, fmt.Errorf("invoices: read credit unit rescale boundary: %w", err)
	}
	if !ok {
		// A database that never rescaled has no pre-rescale rows by definition,
		// and assuming a boundary from the migration filename would be assuming
		// the defect this pass exists to remove.
		//
		// WARN and not INFO, because the other way to reach this branch is a
		// connecting role that cannot READ the boundary: both source tables
		// have row level security enabled with no policies, so a non-superuser
		// without BYPASSRLS sees zero rows and gets the same answer a fresh
		// database gives. Control-plane connects as postgres today, which
		// bypasses both, but a quiet "nothing to do" is exactly how a silent
		// no-op would present if that ever changed.
		s.logger.WarnContext(ctx, "invoice rescale repair: no credit unit rescale boundary is readable, so no invoice can be identified as pre-rescale",
			"check", "if this database did rescale, confirm the connecting role can read public.credit_unit_rescale and public.hive_schema_migrations")
		return 0, nil
	}

	pending, err := s.repo.ListPreRescale(ctx, boundary, repairBatchLimit)
	if err != nil {
		return 0, fmt.Errorf("invoices: list pre-rescale invoices: %w", err)
	}
	if len(pending) == repairBatchLimit {
		// Not a loop: a corrected row still matches the period predicate, so
		// looping would re-select it forever. Say so instead; the live
		// population is three rows and the historical set cannot grow.
		s.logger.WarnContext(ctx, "invoice rescale repair: candidate batch came back full, some rows were not examined",
			"limit", repairBatchLimit)
	}

	repaired, skipped := 0, 0
	for _, inv := range pending {
		switch err := s.repairPreRescaleOne(ctx, inv); {
		case err == nil:
			repaired++
		case errors.Is(err, errRowAlreadyAtLedgerScale):
			skipped++
		case errors.Is(err, errRepairedConcurrently):
			// Another writer corrected it between this pass's SELECT and its
			// UPDATE. The row is right, this pass simply did not do it, and it
			// is not a failure. Counted with the rows that needed nothing,
			// which is what it now is.
			skipped++
		default:
			s.logger.WarnContext(ctx, "invoice rescale repair: row failed",
				"invoice_id", inv.ID,
				"workspace_id", inv.WorkspaceID,
				"period_start", inv.PeriodStart.Format("2006-01-02"),
				"error", err,
			)
		}
	}

	if len(pending) > 0 {
		s.logger.InfoContext(ctx, "invoice rescale repair: pass complete",
			"rescale_applied_at", boundary.UTC().Format("2006-01-02T15:04:05Z07:00"),
			"candidates", len(pending),
			"invoices_repaired", repaired,
			"already_correct", skipped,
		)
	}
	return repaired, nil
}

// repairPreRescaleOne corrects one row, or returns an error without writing
// anything. errRowAlreadyAtLedgerScale means the row was correct.
func (s *Service) repairPreRescaleOne(ctx context.Context, inv Invoice) error {
	if inv.TotalCredits == nil {
		// A row with no credit quantity was never touched by the #1682 pass and
		// still holds its figure in the taka column. That row is that pass's,
		// which now reads it at the right scale; correcting it here would
		// double the correction.
		return fmt.Errorf("invoices: row %s carries no credit quantity; the unconverted pass owns it", inv.ID)
	}

	period := Period{Start: inv.PeriodStart, End: inv.PeriodEnd}
	ledger, err := s.ledgerCredits(ctx, inv.WorkspaceID, period)
	if err != nil {
		return err
	}
	factor, err := creditScaleFactor(inv.TotalCredits, ledger)
	if err != nil {
		return fmt.Errorf("invoices: row %s: %w", inv.ID, err)
	}
	if factor.Cmp(big.NewInt(1)) == 0 {
		return errRowAlreadyAtLedgerScale
	}

	// The rate already on the row, and only that. This pass corrects a quantity
	// and writes no rate, so a row that carries none has nothing to denominate
	// its corrected taka at: resolving one here would convert at a rate the
	// write then discards, leaving taka in a row that pass one still selects as
	// holding credits. Refused rather than resolved, so that outcome is
	// unreachable by construction instead of by ListPreRescale's separate
	// `total_credits IS NOT NULL` predicate happening to exclude it.
	if inv.USDBDTRate == "" {
		return fmt.Errorf("invoices: row %s carries no rate, so its corrected taka has nothing to convert at; the unconverted pass owns it", inv.ID)
	}
	rate, err := payments.ParseUSDBDTRate(inv.USDBDTRate, inv.USDBDTRateSource)
	if err != nil {
		return fmt.Errorf("invoices: row %s carries an unusable rate: %w", inv.ID, err)
	}

	items, total, totalCredits, err := convertLines(repairedLines(inv, factor), rate.Rate)
	if err != nil {
		return err
	}
	// The bound the issue asks for, applied to the figure actually being
	// written rather than to the one it was derived from: the lines are scaled
	// individually, so a row whose lines do not sum to its total lands here
	// with a quantity the ledger does not support, and is refused.
	if !withinLedgerTolerance(totalCredits, ledger) {
		return fmt.Errorf("invoices: row %s would be written at %s credits but its ledger holds %s; refusing",
			inv.ID, totalCredits, ledger)
	}
	if !total.IsInt64() {
		return fmt.Errorf("invoices: rescaled total %s subunits exceeds bigint storage", total)
	}

	repaired := inv
	repaired.TotalBDTSubunits = total
	repaired.TotalCredits = totalCredits
	repaired.LineItems = items
	if repaired.PDFStorageKey == "" {
		repaired.PDFStorageKey = storageKeyFor(inv.WorkspaceID, inv.PeriodStart)
	}

	// The stored object is what a download serves, so it is regenerated before
	// the row. A failed upload leaves the row uncorrected and still a candidate
	// for the next pass, which is the retry.
	if err := s.rewritePDF(ctx, repaired); err != nil {
		return err
	}

	wrote, err := s.repo.UpdateRescaled(ctx, repaired, inv.TotalCredits)
	if err != nil {
		return fmt.Errorf("invoices: persist rescale repair: %w", err)
	}
	if !wrote {
		return fmt.Errorf("%w: %s", errRepairedConcurrently, inv.ID)
	}

	s.logger.InfoContext(ctx, "invoice rescaled",
		"invoice_id", inv.ID,
		"workspace_id", inv.WorkspaceID,
		"period_start", inv.PeriodStart.Format("2006-01-02"),
		"credits_before", inv.TotalCredits.String(),
		"credits_after", totalCredits.String(),
		"total_bdt_subunits_before", inv.TotalBDTSubunits.String(),
		"total_bdt_subunits_after", total.String(),
		"ledger_credits", ledger.String(),
		"rate", rate.Display,
	)
	return nil
}

// repairedLines reads the credit quantities off an already-converted row and
// scales them, keeping the model buckets and request counts the original
// invoice reported. The quantities are re-derived rather than re-aggregated
// from the ledger on purpose: a corrected invoice must cover exactly the
// consumption the original one did, and today's ledger may carry entries the
// generator never saw.
func repairedLines(inv Invoice, factor *big.Int) []ModelCredits {
	if len(inv.LineItems) == 0 {
		credits := new(big.Int)
		if inv.TotalCredits != nil {
			credits.Mul(inv.TotalCredits, factor)
		}
		return []ModelCredits{{ModelID: "unknown", RequestCount: 0, Credits: credits}}
	}
	out := make([]ModelCredits, 0, len(inv.LineItems))
	for _, item := range inv.LineItems {
		credits := new(big.Int)
		if item.Credits != nil {
			credits.Mul(item.Credits, factor)
		}
		out = append(out, ModelCredits{
			ModelID:      item.ModelID,
			RequestCount: item.RequestCount,
			Credits:      credits,
		})
	}
	return out
}

// ledgerCredits is what the account actually consumed in the period, summed
// from credit_ledger_entries. This is the authority every write in this package
// is checked against; the figure stored on the row is the claim being checked.
func (s *Service) ledgerCredits(ctx context.Context, workspaceID uuid.UUID, period Period) (*big.Int, error) {
	buckets, err := s.repo.AggregateByModel(ctx, workspaceID, period)
	if err != nil {
		return nil, fmt.Errorf("invoices: ledger reconciliation: %w", err)
	}
	total := new(big.Int)
	for _, bucket := range buckets {
		if bucket.Credits != nil {
			total.Add(total, bucket.Credits)
		}
	}
	return total, nil
}

// creditScaleFactor reports which credit unit a stored figure is denominated
// in, by asking the ledger.
//
// One means the figure is already in today's unit. preRescaleCreditFactor means
// it is in the pre-rescale one and has to be multiplied. Anything else is an
// error: a figure the ledger supports at neither scale is not a unit problem,
// and this package has no basis on which to write a number for it.
//
// ponytail: two candidate scales, tried in order, rather than a general search.
// There has been exactly one rescale. A second one adds a third candidate here
// and a second historical constant beside preRescaleCreditFactor.
func creditScaleFactor(stored, ledger *big.Int) (*big.Int, error) {
	current := new(big.Int)
	if stored != nil {
		current.Set(stored)
	}
	if withinLedgerTolerance(current, ledger) {
		return big.NewInt(1), nil
	}
	rescaled := new(big.Int).Mul(current, big.NewInt(preRescaleCreditFactor))
	if withinLedgerTolerance(rescaled, ledger) {
		return big.NewInt(preRescaleCreditFactor), nil
	}
	return nil, fmt.Errorf(
		"stored credit figure %s matches the ledger's %s at neither the current unit nor the pre-rescale one (factor %d); refusing to write stored money on a figure nothing supports",
		current, ledger, preRescaleCreditFactor)
}

// withinLedgerTolerance reports whether a credit figure agrees with the ledger
// closely enough to be written, within ledgerToleranceBasisPoints.
//
// An empty ledger admits only a zero figure. That is deliberate and it fails
// closed: a period whose entries have been removed offers no evidence for any
// amount, and inventing one would present a guess as a measurement.
func withinLedgerTolerance(figure, ledger *big.Int) bool {
	if figure == nil || ledger == nil {
		return false
	}
	diff := new(big.Int).Sub(figure, ledger)
	diff.Abs(diff)
	lhs := diff.Mul(diff, big.NewInt(basisPointsPerWhole))
	rhs := new(big.Int).Mul(ledger, big.NewInt(ledgerToleranceBasisPoints))
	rhs.Abs(rhs)
	return lhs.Cmp(rhs) <= 0
}
