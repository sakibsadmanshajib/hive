package invoices

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
)

// =============================================================================
// Issue #1682 — repairing invoices written before the issue #1648 fix.
//
// Commit 4205e0a49 fixed GENERATION. It did not touch the rows already stored,
// and nothing else would: InsertOrFetch is ON CONFLICT DO NOTHING and the
// monthly cron is idempotent, so an existing row is never rewritten, and
// handlePDF redirects to the stored object rather than re-rendering it. The
// August 2026 invoice therefore still holds a raw Hive credit count in a column
// named total_bdt_subunits, which the console divides by one hundred and prints
// as taka: 524,653,338 credits read as about 5.2 million taka against a real
// spend of about 0.52 USD.
//
// What this pass does to each such row, and nothing else:
//
//	1. Reads the stored figure as the credit count it actually is.
//	2. Converts it once, at the rate that row would have been generated at:
//	   the account's own FX snapshot from before the period closed, or the
//	   platform rate when it has none. The rate and its source are recorded.
//	3. Re-renders the PDF and overwrites the stored object, because the
//	   customer downloads that object and not the row.
//	4. Writes the row, guarded on the same predicate that selected it.
//
// Three bounds, all enforced rather than argued:
//
//	CORRECT ROWS ARE NOT TOUCHED. The only predicate is `usd_bdt_rate IS NULL`.
//	A row with a rate is already converted; it is not read, not re-rendered and
//	not written.
//
//	IDEMPOTENT. Writing the rate is what removes a row from the predicate, so a
//	second pass selects nothing. The UPDATE repeats the predicate too, so two
//	replicas booting at once cannot both convert the same row.
//
//	NO SILENT RECOMPUTATION. The taka amount is derived from the credit count
//	already on the row, at a rate that is written down beside it. Nothing is
//	re-aggregated from the ledger, so a repaired invoice covers exactly the
//	consumption the original one did.
// =============================================================================

// repairBatchLimit bounds ONE SELECT, not the whole pass. The pass keeps
// fetching batches until one of them repairs nothing, so a population larger
// than this is finished by the same boot rather than waiting for the next one.
// Bounding the query at all is a guard against loading an unbounded result set
// into memory; the live population is a handful of rows.
const repairBatchLimit = 500

// errRepairedConcurrently marks the one non-failure that stops a row mid-pass:
// another replica wrote it between this pass's SELECT and its UPDATE. The row
// is correct, this pass simply did not do it, and it must not be logged as a
// failure or the loop below would read it as a lack of progress.
var errRepairedConcurrently = errors.New("invoices: row repaired concurrently")

// RepairUnconvertedInvoices converts every stored invoice whose amounts are a
// conflated credit count, regenerating its PDF, and returns how many rows it
// repaired.
//
// Per-row errors are isolated and logged, exactly as the monthly cron isolates
// per-workspace failures: one account with an unreadable rate or an unwritable
// bucket must not stop the rest of the repair. Only a failure to enumerate the
// rows at all is returned as a pass-level error, because that leaves the caller
// with no idea whether there was anything to do.
func (s *Service) RepairUnconvertedInvoices(ctx context.Context) (int, error) {
	repaired, seen := 0, 0
	for {
		pending, err := s.repo.ListUnconverted(ctx, repairBatchLimit)
		if err != nil {
			return repaired, fmt.Errorf("invoices: list unconverted: %w", err)
		}
		if len(pending) == 0 {
			break
		}
		seen += len(pending)

		// Progress, not batch size, is the loop condition. A row that fails
		// keeps its NULL rate and is therefore selected again by the next
		// SELECT, so counting batches would spin forever on a permanently
		// broken row. Counting repairs cannot: a batch that repairs nothing
		// ends the pass and leaves the rest for the next boot, with every
		// failure already named in the log.
		progress := 0
		for _, inv := range pending {
			err := s.repairOne(ctx, inv)
			switch {
			case err == nil:
				repaired++
				progress++
			case errors.Is(err, errRepairedConcurrently):
				// Another replica did it. Not a failure, and not this pass's
				// progress either; the row is gone from the predicate, so the
				// next SELECT will not return it.
				progress++
			default:
				s.logger.WarnContext(ctx, "invoice repair: row failed",
					"invoice_id", inv.ID,
					"workspace_id", inv.WorkspaceID,
					"period_start", inv.PeriodStart.Format("2006-01-02"),
					"error", err,
				)
			}
		}
		if progress == 0 {
			break
		}
	}

	if seen > 0 {
		s.logger.InfoContext(ctx, "invoice repair: pass complete",
			"unconverted_seen", seen,
			"invoices_repaired", repaired,
		)
	}
	return repaired, nil
}

// repairOne converts a single conflated row. It returns an error without
// writing anything if any step fails, so a row that cannot be fully repaired
// stays selectable by the next pass rather than half repaired.
func (s *Service) repairOne(ctx context.Context, inv Invoice) error {
	if inv.USDBDTRate != "" {
		// Belt and braces against a caller that hands this an already-converted
		// row. Dividing a converted amount by the rate a second time is the one
		// outcome this file must make impossible.
		return fmt.Errorf("invoices: row %s already carries rate %q", inv.ID, inv.USDBDTRate)
	}

	period := Period{Start: inv.PeriodStart, End: inv.PeriodEnd}
	rate, err := s.resolveRate(ctx, inv.WorkspaceID, period)
	if err != nil {
		return err
	}

	items, total, totalCredits, err := convertLines(conflatedLines(inv), rate.Rate)
	if err != nil {
		return err
	}

	// Reconcile the derivation against the figure already on the row before
	// overwriting it. Both the new total and the new credit quantity come from
	// line_items, and the old generator summed its lines into the total in the
	// same loop, so on every row this pass can actually see they agree.
	//
	// The check is here because the write is one way. Setting the rate removes
	// the row from the predicate permanently and no later pass revisits it, so
	// a row whose lines decode to less than its own stored total would be
	// quietly rewritten downward, frozen there, and its customer-facing PDF
	// regenerated to match. An empty bdt_subunits string decodes to a nil
	// amount, which conflatedLines reads as zero credits, and the row would be
	// repaired to a total of zero with a rate stamped on it as though that were
	// a measurement. Failing loudly leaves the row selectable and names it in
	// the log, which is the outcome this repository prefers over a quiet
	// rewrite of stored money.
	if inv.TotalBDTSubunits != nil && totalCredits.Cmp(inv.TotalBDTSubunits) != 0 {
		return fmt.Errorf("invoices: row %s line items sum to %s credits but the row stores %s; refusing to rewrite a total from lines that disagree with it",
			inv.ID, totalCredits, inv.TotalBDTSubunits)
	}

	if !total.IsInt64() {
		return fmt.Errorf("invoices: repaired total %s subunits exceeds bigint storage", total)
	}

	repaired := inv
	repaired.TotalBDTSubunits = total
	repaired.TotalCredits = totalCredits
	repaired.LineItems = items
	repaired.USDBDTRate = rate.Display
	repaired.USDBDTRateSource = rate.Source
	if repaired.PDFStorageKey == "" {
		repaired.PDFStorageKey = storageKeyFor(inv.WorkspaceID, inv.PeriodStart)
	}

	// Regenerate the customer-facing document BEFORE the row. The stored object
	// is what a download serves, so a repaired row over a stale PDF would leave
	// the wrong figure in the customer's hands while the console claimed it was
	// fixed. If the upload fails, the row keeps its NULL rate and the next pass
	// retries the whole thing.
	//
	// ponytail: the object write is last-writer-wins while the row write is
	// guarded, so two processes repairing the same row AT DIFFERENT RATES could
	// leave the stored PDF disagreeing with the row permanently. Unreachable
	// today twice over: the deploy runs one control-plane at a time, and both
	// arms of resolveRate are deterministic for a closed period unless two live
	// processes hold different HIVE_USD_BDT_RATE values. If this ever runs
	// multi-replica, the fix is at the !wrote branch below: re-read the row and
	// re-render from the winning values so the object converges on the row that
	// won rather than on whichever writer was slower.
	if err := s.rewritePDF(ctx, repaired); err != nil {
		return err
	}

	wrote, err := s.repo.UpdateConverted(ctx, repaired)
	if err != nil {
		return fmt.Errorf("invoices: persist repair: %w", err)
	}
	if !wrote {
		return fmt.Errorf("%w: %s", errRepairedConcurrently, inv.ID)
	}

	s.logger.InfoContext(ctx, "invoice repaired",
		"invoice_id", inv.ID,
		"workspace_id", inv.WorkspaceID,
		"period_start", inv.PeriodStart.Format("2006-01-02"),
		"credits", totalCredits.String(),
		"total_bdt_subunits", total.String(),
		"rate", rate.Display,
		"rate_source", rate.Source,
	)
	return nil
}

// conflatedLines reads a pre-fix row's line items as what they are: credit
// quantities stored under a field named after taka.
//
// A row with no line items still yields one bucket, from the total, so an
// invoice whose JSONB was empty is repaired rather than silently zeroed. The
// literal "unknown" matches the bucket AggregateByModel uses for ledger rows
// without model metadata.
func conflatedLines(inv Invoice) []ModelCredits {
	if len(inv.LineItems) == 0 {
		credits := new(big.Int)
		if inv.TotalBDTSubunits != nil {
			credits.Set(inv.TotalBDTSubunits)
		}
		return []ModelCredits{{ModelID: "unknown", RequestCount: 0, Credits: credits}}
	}
	out := make([]ModelCredits, 0, len(inv.LineItems))
	for _, item := range inv.LineItems {
		credits := new(big.Int)
		if item.BDTSubunits != nil {
			credits.Set(item.BDTSubunits)
		}
		out = append(out, ModelCredits{
			ModelID:      item.ModelID,
			RequestCount: item.RequestCount,
			Credits:      credits,
		})
	}
	return out
}

// rewritePDF renders the repaired invoice and overwrites the stored object at
// the key the row already points at, so an existing download link keeps working
// and starts serving the corrected document.
func (s *Service) rewritePDF(ctx context.Context, inv Invoice) error {
	if s.storage == nil {
		return fmt.Errorf("invoices: no storage backend, cannot regenerate the pdf for %s", inv.ID)
	}
	workspaceName := inv.WorkspaceID.String()
	if s.naming != nil {
		if name, err := s.naming.WorkspaceName(ctx, inv.WorkspaceID); err == nil && name != "" {
			workspaceName = name
		}
	}
	pdfBytes, err := s.pdf.Render(inv, workspaceName)
	if err != nil {
		return fmt.Errorf("invoices: render repaired pdf: %w", err)
	}
	if err := s.storage.Upload(ctx, FilesBucket, inv.PDFStorageKey,
		bytes.NewReader(pdfBytes), int64(len(pdfBytes)), "application/pdf",
	); err != nil {
		return fmt.Errorf("invoices: upload repaired pdf: %w", err)
	}
	return nil
}
