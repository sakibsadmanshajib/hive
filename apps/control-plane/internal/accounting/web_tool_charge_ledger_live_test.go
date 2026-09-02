package accounting

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/routing"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/usage"
)

// Proof by ledger magnitude for the per-call web tools (issue #1695), on a real
// database, through the real catalog read, the real reservation lifecycle and
// the real ledger SQL.
//
// What it proves, and why each half is needed:
//
//  1. The price comes from the CATALOG. The figure charged below is read out of
//     public.model_aliases through routing.Service.AliasPrice, exactly as
//     edge-api reads it, rather than being restated in the test. A migration
//     that seeded the wrong number fails here rather than passing on a literal
//     the test and the code both got wrong.
//  2. A call MOVES THE LEDGER. Balance is read back out of the database before
//     and after, and the assertion is the difference, not a boolean "settled"
//     flag. A charge of zero, the shape that billed nothing for three days in
//     July, fails this.
//  3. The two magnitudes RELATE. A fetch costs exactly twice a search, so a
//     lookup that resolved both tools to one alias fails even though both
//     charges would look plausible in isolation.
//
// Gated on HIVE_TEST_DB_URL like the rest of this package's live suite, and run
// against the ephemeral instance bootstrapped from supabase/migrations by
// scripts/ci-throwaway-db.sh. ./internal/accounting/... is already on the
// live-Postgres step's package list in .github/workflows/ci.yml; without it
// this file is decoration.
func TestWebToolCallChargeMovesTheLedger_Live(t *testing.T) {
	pool := newAccountingTestPool(t)
	accountID := seedReleaseIdempotencyAccount(t, pool)
	ctx := context.Background()

	ledgerSvc := ledger.NewService(ledger.NewPgxRepository(pool))
	svc := NewService(
		NewPgxRepository(pool),
		ledgerSvc,
		usage.NewService(usage.NewPgxRepository(pool)),
	)
	// nil entitlements is correct here and only here: AliasPrice resolves no
	// tenant and selects no route, so there is nothing to entitle. A tenant
	// scoped SelectRoute against this service would fail closed instead.
	priceSvc := routing.NewService(routing.NewPgxRepository(pool), nil)

	if _, err := ledgerSvc.GrantCredits(ctx, accountID, uuid.NewString(), 10_000_000,
		map[string]any{"reason": "web tool charge proof"}); err != nil {
		t.Fatalf("grant credits: %v", err)
	}

	charges := map[string]int64{}
	for _, tc := range []struct{ alias, endpoint string }{
		{"hive-web-search", "web_search"},
		{"hive-web-fetch", "web_fetch"},
	} {
		pricing, priceUnit, err := priceSvc.AliasPrice(ctx, tc.alias)
		if err != nil {
			t.Fatalf("%s: alias price: %v", tc.alias, err)
		}
		if priceUnit != "calls" {
			t.Fatalf("%s: price_unit = %q, want calls", tc.alias, priceUnit)
		}
		if pricing.OutputPriceCredits == nil || *pricing.OutputPriceCredits <= 0 {
			t.Fatalf("%s: output price = %v, want a positive per-million-calls rate", tc.alias, pricing.OutputPriceCredits)
		}
		if pricing.InputPriceCredits == nil || *pricing.InputPriceCredits != 0 {
			t.Fatalf("%s: input price = %v, want an explicit 0 on a non-token unit", tc.alias, pricing.InputPriceCredits)
		}
		// One call, at a rate quoted per million calls. The catalog figure is an
		// exact multiple of a million by construction (see the migration's
		// worked derivation), so this is the same arithmetic edge-api's
		// metering.ChargeCredits performs, with nothing to round.
		credits := *pricing.OutputPriceCredits / 1_000_000
		if credits*1_000_000 != *pricing.OutputPriceCredits {
			t.Fatalf("%s: per-million rate %d is not a whole number of credits per call",
				tc.alias, *pricing.OutputPriceCredits)
		}

		before, err := ledgerSvc.GetBalance(ctx, accountID)
		if err != nil {
			t.Fatalf("%s: balance before: %v", tc.alias, err)
		}

		reservation, err := svc.CreateReservation(ctx, CreateReservationInput{
			AccountID:        accountID,
			RequestID:        uuid.NewString(),
			AttemptNumber:    1,
			Endpoint:         tc.endpoint,
			ModelAlias:       tc.alias,
			EstimatedCredits: credits,
		})
		if err != nil {
			t.Fatalf("%s: CreateReservation: %v", tc.alias, err)
		}

		// Positive control: the hold is really on the books, so the assertions
		// after settlement cannot pass over a ledger that never moved.
		during, err := ledgerSvc.GetBalance(ctx, accountID)
		if err != nil {
			t.Fatalf("%s: balance during: %v", tc.alias, err)
		}
		if during.ReservedCredits != before.ReservedCredits+credits {
			t.Fatalf("%s: reserved after hold = %d, want %d",
				tc.alias, during.ReservedCredits, before.ReservedCredits+credits)
		}

		if _, err := svc.FinalizeReservation(ctx, FinalizeReservationInput{
			AccountID:              accountID,
			ReservationID:          reservation.ID,
			ActualCredits:          credits,
			TerminalUsageConfirmed: true,
			Status:                 string(usage.AttemptStatusCompleted),
			// A per-call charge meters no tokens, and the row says so rather
			// than implying a count nobody measured.
			InputTokens:  0,
			OutputTokens: 0,
		}); err != nil {
			t.Fatalf("%s: FinalizeReservation: %v", tc.alias, err)
		}

		after, err := ledgerSvc.GetBalance(ctx, accountID)
		if err != nil {
			t.Fatalf("%s: balance after: %v", tc.alias, err)
		}
		if moved := before.AvailableCredits - after.AvailableCredits; moved != credits {
			t.Errorf("%s: available fell by %d, want exactly %d", tc.alias, moved, credits)
		}
		if after.ReservedCredits != before.ReservedCredits {
			t.Errorf("%s: reserved = %d after settlement, want it back at %d",
				tc.alias, after.ReservedCredits, before.ReservedCredits)
		}

		// The usage row, read back out of the database rather than inferred: a
		// per-call charge with a credit delta and no tokens.
		var (
			creditDelta  int64
			inputTokens  int64
			outputTokens int64
			rowAlias     string
			rowEndpoint  string
		)
		if err := pool.QueryRow(ctx, `
			SELECT hive_credit_delta, input_tokens, output_tokens, model_alias, endpoint
			  FROM public.usage_events
			 WHERE account_id = $1 AND model_alias = $2 AND hive_credit_delta <> 0
			 ORDER BY created_at DESC
			 LIMIT 1
		`, accountID, tc.alias).Scan(&creditDelta, &inputTokens, &outputTokens, &rowAlias, &rowEndpoint); err != nil {
			t.Fatalf("%s: reading the usage row back: %v", tc.alias, err)
		}
		// Negative, because hive_credit_delta is signed and a charge is a
		// DEBIT against the account. Asserting the magnitude alone would pass
		// on a row that credited the customer.
		if creditDelta != -credits {
			t.Errorf("%s: usage row credit delta = %d, want %d (a debit of %d credits)",
				tc.alias, creditDelta, -credits, credits)
		}
		if inputTokens != 0 || outputTokens != 0 {
			t.Errorf("%s: usage row reports tokens %d/%d, want 0/0 on a per-call charge",
				tc.alias, inputTokens, outputTokens)
		}
		if rowEndpoint != tc.endpoint {
			t.Errorf("%s: usage row endpoint = %q, want %q", tc.alias, rowEndpoint, tc.endpoint)
		}

		// Logged, not just asserted, so a run of this test is itself the
		// evidence a reviewer can read rather than a green tick to trust.
		t.Logf("%s (%s): catalog %d credits per million calls -> %d per call; available %d -> %d; reserved %d -> %d -> %d; usage row %s delta %d tokens %d/%d",
			tc.alias, rowAlias, *pricing.OutputPriceCredits, credits,
			before.AvailableCredits, after.AvailableCredits,
			before.ReservedCredits, during.ReservedCredits, after.ReservedCredits,
			rowEndpoint, creditDelta, inputTokens, outputTokens)

		charges[tc.alias] = credits
	}

	// The relationship between the two, which a single-alias lookup bug would
	// break while still charging a plausible figure for each.
	if charges["hive-web-fetch"] != 2*charges["hive-web-search"] {
		t.Errorf("fetch charged %d and search %d, want the fetch to be exactly twice the search",
			charges["hive-web-fetch"], charges["hive-web-search"])
	}
}
