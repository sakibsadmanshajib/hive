package ledger

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Behavioral tests for the money-path ledger against the real Postgres schema
// (HIVE_TEST_DB_URL gated, same convention as balance_over_release_live_test.go).
//
// Invariants encoded here (see .wolf/decisions.md D-031..D-034):
//
//   - Replay of an already-used idempotency key returns the ORIGINAL entry and
//     writes exactly one row (D-034: a reservation reaches its terminal state
//     exactly once).
//   - A capture consumes its own hold exactly once: hold - charge - release nets
//     the reservation to zero reserved, and the charge lands in posted credits,
//     never back into reserved.
//   - Negative and zero amounts are refused before any row is written
//     (fail-closed input validation on every posting surface).
//   - Reserved is per-reservation clamped-at-zero; over-release is reported as
//     a corruption counter, never rendered as a hold (issue #918 reader half,
//     covered end-to-end in balance_over_release_live_test.go).

func TestPostEntryIdempotentReplay_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	ctx := context.Background()
	svc := NewService(NewPgxRepository(pool))

	first, err := svc.GrantCredits(ctx, accountID, "grant-replay-1", 500, nil)
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	second, err := svc.GrantCredits(ctx, accountID, "grant-replay-1", 500, nil)
	if err != nil {
		t.Fatalf("replay grant: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("replay returned entry %s, want the original %s", second.ID, first.ID)
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM public.credit_ledger_entries WHERE account_id = $1`, accountID,
	).Scan(&n); err != nil {
		t.Fatalf("count entries: %v", err)
	}
	if n != 1 {
		t.Fatalf("replay wrote extra rows: %d entries for one idempotency key", n)
	}
}

// TestReservationLifecycle_Live walks hold -> charge -> release and asserts the
// settlement arithmetic on both GetBalance arms: posted credits absorb the
// capture, reserved credits return to zero, and replaying the terminal entries
// under the same keys changes nothing (terminal exactly once).
func TestReservationLifecycle_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	ctx := context.Background()
	repo := NewPgxRepository(pool)
	svc := NewService(repo)

	if _, err := svc.GrantCredits(ctx, accountID, "lifecycle-grant", 100000, nil); err != nil {
		t.Fatalf("grant: %v", err)
	}

	reservationID := uuid.New()
	post(t, svc, accountID, reservationID, "reserve", 5000, false)

	mid, err := svc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("mid balance: %v", err)
	}
	if mid.ReservedCredits != 5000 || mid.AvailableCredits != 100000-5000 {
		t.Fatalf("after hold: reserved=%d available=%d, want 5000 / %d",
			mid.ReservedCredits, mid.AvailableCredits, 100000-5000)
	}

	if _, err := svc.ChargeUsage(ctx, accountID, "req-lifecycle", &reservationID, &reservationID, "lifecycle-charge", 3000, nil); err != nil {
		t.Fatalf("charge: %v", err)
	}
	// Production settlement lifts the WHOLE hold after capture (the charge is
	// what the customer actually pays); releasing only the remainder would
	// strand actual credits in the reserved bucket forever.
	post(t, svc, accountID, reservationID, "release", 5000, true)

	balance, err := svc.GetBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}

	// Invariant: the capture consumed its hold exactly once. The full hold is
	// lifted by the release; the charge (3000) is settled into posted credits,
	// never back into reserved.
	if balance.PostedCredits != 100000-3000 {
		t.Fatalf("posted=%d, want %d (charge settles posted, not reserved)", balance.PostedCredits, 100000-3000)
	}
	if balance.ReservedCredits != 0 {
		t.Fatalf("reserved=%d, want 0 after charge+release settle the full hold", balance.ReservedCredits)
	}
	if balance.AvailableCredits != 100000-3000 {
		t.Fatalf("available=%d, want %d", balance.AvailableCredits, 100000-3000)
	}
	if balance.OverReleasedReservations != 0 {
		t.Fatalf("clean lifecycle reported %d over-released reservations", balance.OverReleasedReservations)
	}

	// Terminal exactly once: replaying both terminal entries under the same
	// keys must return the original entries and move nothing.
	chargeBefore, err := repo.GetUsageSince(ctx, accountID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("usage before replay: %v", err)
	}
	replayCharge, err := svc.ChargeUsage(ctx, accountID, "req-lifecycle", &reservationID, &reservationID, "lifecycle-charge", 3000, nil)
	if err != nil {
		t.Fatalf("charge replay: %v", err)
	}
	if replayCharge.CreditsDelta != -3000 {
		t.Fatalf("replay charge delta %d, want original -3000", replayCharge.CreditsDelta)
	}
	post(t, svc, accountID, reservationID, "release", 5000, true)

	chargeAfter, err := repo.GetUsageSince(ctx, accountID, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("usage after replay: %v", err)
	}
	if chargeAfter != chargeBefore {
		t.Fatalf("usage moved %d -> %d across terminal replays; a reservation must reach its terminal state exactly once",
			chargeBefore, chargeAfter)
	}
}

// TestGetBalanceArms_Live drives both arms of the GetBalance query with table
// driven scenarios: posted credits come from grant/adjustment/usage_charge/
// refund entries, reserved credits from outstanding per-reservation holds.
func TestGetBalanceArms_Live(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, svc *Service, accountID uuid.UUID)
		wantPosted int64
		wantRes    int64
	}{
		{
			name: "holds arm: outstanding hold reserves against posted",
			setup: func(t *testing.T, svc *Service, accountID uuid.UUID) {
				if _, err := svc.GrantCredits(context.Background(), accountID, "arms-grant-a", 100000, nil); err != nil {
					t.Fatalf("grant: %v", err)
				}
				res := uuid.New()
				post(t, svc, accountID, res, "reserve", 7500, false)
			},
			wantPosted: 100000,
			wantRes:    7500,
		},
		{
			name: "settled arm: full release clears the reservation",
			setup: func(t *testing.T, svc *Service, accountID uuid.UUID) {
				if _, err := svc.GrantCredits(context.Background(), accountID, "arms-grant-b", 100000, nil); err != nil {
					t.Fatalf("grant: %v", err)
				}
				res := uuid.New()
				post(t, svc, accountID, res, "reserve", 9000, false)
				post(t, svc, accountID, res, "release", 9000, true)
			},
			wantPosted: 100000,
			wantRes:    0,
		},
		{
			name: "partial release keeps the remainder held",
			setup: func(t *testing.T, svc *Service, accountID uuid.UUID) {
				if _, err := svc.GrantCredits(context.Background(), accountID, "arms-grant-c", 100000, nil); err != nil {
					t.Fatalf("grant: %v", err)
				}
				res := uuid.New()
				post(t, svc, accountID, res, "reserve", 9000, false)
				post(t, svc, accountID, res, "release", 4000, true)
			},
			wantPosted: 100000,
			wantRes:    5000,
		},
		{
			name: "refund posts positive without touching reserved",
			setup: func(t *testing.T, svc *Service, accountID uuid.UUID) {
				if _, err := svc.GrantCredits(context.Background(), accountID, "arms-grant-d", 10000, nil); err != nil {
					t.Fatalf("grant: %v", err)
				}
				if _, err := svc.ChargeUsage(context.Background(), accountID, "req-refund", nil, nil, "arms-charge", 2500, nil); err != nil {
					t.Fatalf("charge: %v", err)
				}
				if _, err := svc.RefundCredits(context.Background(), accountID, "req-refund", nil, nil, "arms-refund", 1000, nil); err != nil {
					t.Fatalf("refund: %v", err)
				}
			},
			wantPosted: 10000 - 2500 + 1000,
			wantRes:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pool := newLedgerTestPool(t)
			accountID := seedLedgerAccount(t, pool)
			svc := NewService(NewPgxRepository(pool))

			tc.setup(t, svc, accountID)

			balance, err := svc.GetBalance(context.Background(), accountID)
			if err != nil {
				t.Fatalf("GetBalance: %v", err)
			}
			if balance.PostedCredits != tc.wantPosted {
				t.Fatalf("posted=%d, want %d", balance.PostedCredits, tc.wantPosted)
			}
			if balance.ReservedCredits != tc.wantRes {
				t.Fatalf("reserved=%d, want %d", balance.ReservedCredits, tc.wantRes)
			}
			if balance.AvailableCredits != tc.wantPosted-tc.wantRes {
				t.Fatalf("available=%d, want %d (available = posted - reserved)",
					balance.AvailableCredits, tc.wantPosted-tc.wantRes)
			}
		})
	}
}

// TestLedgerRefusesBadAmounts_Live encodes the fail-closed input invariant:
// zero and negative amounts are refused by the service layer before any row is
// written, on every posting surface.
func TestLedgerRefusesBadAmounts_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	ctx := context.Background()
	svc := NewService(NewPgxRepository(pool))

	countEntries := func() int64 {
		t.Helper()
		var n int64
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM public.credit_ledger_entries WHERE account_id = $1`, accountID,
		).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	tests := []struct {
		name string
		call func() error
	}{
		{"grant zero", func() error { _, err := svc.GrantCredits(ctx, accountID, "bad-grant-0", 0, nil); return err }},
		{"grant negative", func() error { _, err := svc.GrantCredits(ctx, accountID, "bad-grant-neg", -5, nil); return err }},
		{"adjust zero", func() error { _, err := svc.AdjustCredits(ctx, accountID, "bad-adjust-0", 0, nil); return err }},
		{"hold zero", func() error {
			_, err := svc.ReserveCredits(ctx, accountID, "r", nil, nil, "bad-hold-0", 0, nil)
			return err
		}},
		{"hold negative", func() error {
			_, err := svc.ReserveCredits(ctx, accountID, "r", nil, nil, "bad-hold-neg", -1, nil)
			return err
		}},
		{"release zero", func() error {
			_, err := svc.ReleaseReservedCredits(ctx, accountID, "r", nil, nil, "bad-rel-0", 0, nil)
			return err
		}},
		{"charge zero", func() error {
			_, err := svc.ChargeUsage(ctx, accountID, "r", nil, nil, "bad-chg-0", 0, nil)
			return err
		}},
		{"charge negative", func() error {
			_, err := svc.ChargeUsage(ctx, accountID, "r", nil, nil, "bad-chg-neg", -2, nil)
			return err
		}},
		{"refund zero", func() error {
			_, err := svc.RefundCredits(ctx, accountID, "r", nil, nil, "bad-ref-0", 0, nil)
			return err
		}},
		{"blank idempotency key on grant", func() error { _, err := svc.GrantCredits(ctx, accountID, "   ", 10, nil); return err }},
		{"blank idempotency key on hold", func() error { _, err := svc.ReserveCredits(ctx, accountID, "r", nil, nil, "", 10, nil); return err }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := countEntries()
			err := tc.call()
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("expected ValidationError, got %v", err)
			}
			if after := countEntries(); after != before {
				t.Fatalf("refused call still wrote rows (%d -> %d); validation must fail closed before any write",
					before, after)
			}
		})
	}
}

// TestListEntriesWithCursorFilters_Live covers the parameterized query builder:
// default limit, entry-type filter, request-id filter, and cursor pagination
// whose pages are disjoint and together cover the whole account slice.
func TestListEntriesWithCursorFilters_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountA := seedLedgerAccount(t, pool)
	accountB := seedLedgerAccount(t, pool)
	ctx := context.Background()
	svc := NewService(NewPgxRepository(pool))

	for i, acct := range []uuid.UUID{accountA, accountB} {
		for j := 0; j < 3; j++ {
			key := uuid.NewString()
			if _, err := svc.GrantCredits(ctx, acct, key, int64(100+i+j), nil); err != nil {
				t.Fatalf("seed grant acct%d #%d: %v", i, j, err)
			}
		}
	}
	// One hold + charge pair sharing a request_id, plus a grant that does not.
	res := uuid.New()
	if _, err := svc.ReserveCredits(ctx, accountA, "req-filter-target", &res, &res, "filter-hold", 40, nil); err != nil {
		t.Fatalf("seed hold: %v", err)
	}
	if _, err := svc.ChargeUsage(ctx, accountA, "req-filter-target", &res, &res, "filter-charge", 15, nil); err != nil {
		t.Fatalf("seed charge: %v", err)
	}

	t.Run("default limit returns newest first", func(t *testing.T) {
		entries, err := svc.ListEntriesWithCursor(ctx, ListEntriesFilter{AccountID: accountB})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("got %d entries, want all 3", len(entries))
		}
		// Newest first means newest by created_at. This assertion used to read
		// descending `id`, which is what the query actually did and is not the
		// same thing at all: `id` is a v4 UUID, so it encoded the ordering bug
		// (a customer's money history served shuffled) as the requirement.
		// Non-increasing rather than strictly decreasing, since two entries can
		// share a timestamp; the deterministic ordering case lives in
		// repository_order_live_test.go.
		for i := 1; i < len(entries); i++ {
			if entries[i-1].CreatedAt.Before(entries[i].CreatedAt) {
				t.Fatalf(
					"entries not ordered newest first: %s then %s",
					entries[i-1].CreatedAt.Format(time.RFC3339Nano),
					entries[i].CreatedAt.Format(time.RFC3339Nano),
				)
			}
		}
	})

	t.Run("entry type filter narrows the page", func(t *testing.T) {
		grantsOnly := EntryTypeGrant
		entries, err := svc.ListEntriesWithCursor(ctx, ListEntriesFilter{AccountID: accountA, EntryType: &grantsOnly})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("got %d grant entries, want 3", len(entries))
		}
		for _, e := range entries {
			if e.EntryType != EntryTypeGrant {
				t.Fatalf("filter leaked entry type %s", e.EntryType)
			}
		}
	})

	t.Run("request id filter isolates one reservation lifecycle", func(t *testing.T) {
		entries, err := svc.ListEntriesWithCursor(ctx, ListEntriesFilter{AccountID: accountA, RequestID: "req-filter-target"})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("got %d entries for the request, want 2 (hold + charge)", len(entries))
		}
		for _, e := range entries {
			if e.RequestID != "req-filter-target" {
				t.Fatalf("filter leaked request_id %q", e.RequestID)
			}
			if e.ReservationID == nil || *e.ReservationID != res {
				t.Fatalf("entry missing the shared reservation id")
			}
		}
	})

	t.Run("cursor pagination pages are disjoint and complete", func(t *testing.T) {
		var page1, page2 []LedgerEntry
		var err error
		page1, err = svc.ListEntriesWithCursor(ctx, ListEntriesFilter{AccountID: accountA, Limit: 3})
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		if len(page1) != 3 {
			t.Fatalf("page1 got %d, want 3", len(page1))
		}
		cursor := page1[len(page1)-1].ID
		page2, err = svc.ListEntriesWithCursor(ctx, ListEntriesFilter{AccountID: accountA, Limit: 3, Cursor: &cursor})
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		if len(page2) != 2 {
			t.Fatalf("page2 got %d, want remaining 2", len(page2))
		}
		seen := map[uuid.UUID]bool{}
		for _, e := range append(append([]LedgerEntry{}, page1...), page2...) {
			if seen[e.ID] {
				t.Fatalf("entry %s appeared on two pages", e.ID)
			}
			seen[e.ID] = true
		}
		if len(seen) != 5 {
			t.Fatalf("pages cover %d unique entries, want all 5", len(seen))
		}
	})
}

// TestGetUsageSinceWindow_Live pins the usage window semantics: only charges at
// or after the cutoff count, non-charge entries never count.
func TestGetUsageSinceWindow_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	ctx := context.Background()
	repo := NewPgxRepository(pool)
	svc := NewService(repo)

	// An old charge outside any sane window, backdated via direct insert.
	if _, err := pool.Exec(ctx,
		`INSERT INTO public.credit_ledger_entries (account_id, entry_type, credits_delta, idempotency_key, created_at)
		 VALUES ($1, 'usage_charge', -777, 'old-charge', now() - interval '48 hours')`, accountID,
	); err != nil {
		t.Fatalf("seed old charge: %v", err)
	}
	if _, err := svc.ChargeUsage(ctx, accountID, "req-win", nil, nil, "win-charge-a", 120, nil); err != nil {
		t.Fatalf("charge a: %v", err)
	}
	if _, err := svc.ChargeUsage(ctx, accountID, "req-win", nil, nil, "win-charge-b", 30, nil); err != nil {
		t.Fatalf("charge b: %v", err)
	}
	if _, err := svc.GrantCredits(ctx, accountID, "win-grant", 99999, nil); err != nil {
		t.Fatalf("grant: %v", err)
	}

	cutoff := time.Now().Add(-time.Hour)
	used, err := repo.GetUsageSince(ctx, accountID, cutoff)
	if err != nil {
		t.Fatalf("usage since: %v", err)
	}
	if used != 150 {
		t.Fatalf("used=%d, want 150 (only charges inside the window)", used)
	}
}

// TestInvoicesRoundTrip_Live covers the invoice read surface against real rows,
// including the ErrNotFound contract for a foreign invoice id.
func TestInvoicesRoundTrip_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	ctx := context.Background()
	repo := NewPgxRepository(pool)

	lineItems := `[{"kind":"topup","credits":1000}]`
	var invoiceIDs []uuid.UUID
	for i := 0; i < 2; i++ {
		var intentID uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO public.payment_intents (account_id, rail, status, credits, amount_usd, amount_local, local_currency, idempotency_key)
			 VALUES ($1, 'stripe', 'created', 1000, 1000, 0, '', $2) RETURNING id`,
			accountID, "inv-test-"+uuid.NewString(),
		).Scan(&intentID); err != nil {
			t.Fatalf("seed intent %d: %v", i, err)
		}
		var invID uuid.UUID
		if err := pool.QueryRow(ctx,
			`INSERT INTO public.payment_invoices (account_id, payment_intent_id, invoice_number, status, credits, amount_usd, amount_local, local_currency, tax_treatment, rail, line_items)
			 VALUES ($1, $2, $3, 'issued', 1000, 1000, 0, 'USD', 'none', 'stripe', $4::jsonb)
			 RETURNING id`,
			accountID, intentID, "INV-LIVE-"+strconv.Itoa(i)+"-"+uuid.NewString()[:8], lineItems,
		).Scan(&invID); err != nil {
			t.Fatalf("seed invoice %d: %v", i, err)
		}
		invoiceIDs = append(invoiceIDs, invID)
	}

	invoices, err := repo.ListInvoices(ctx, accountID)
	if err != nil {
		t.Fatalf("list invoices: %v", err)
	}
	if len(invoices) != 2 {
		t.Fatalf("got %d invoices, want 2", len(invoices))
	}
	if invoices[0].CreatedAt.Before(invoices[1].CreatedAt) {
		t.Fatalf("invoices not ordered newest first")
	}
	if invoices[0].LineItems == nil || len(invoices[0].LineItems) != 1 {
		t.Fatalf("line items did not decode: %+v", invoices[0].LineItems)
	}

	got, err := repo.GetInvoice(ctx, accountID, invoiceIDs[0])
	if err != nil {
		t.Fatalf("get invoice: %v", err)
	}
	if got.ID != invoiceIDs[0] || got.AccountID != accountID {
		t.Fatalf("fetched wrong invoice %+v", got)
	}

	if _, err := repo.GetInvoice(ctx, accountID, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign invoice id returned %v, want ErrNotFound", err)
	}
}

// TestCreditUnitStampingAndHelpers covers the pure helpers around PostEntryTx:
// the v2 credit-unit stamp never mutates the caller's metadata and yields to a
// caller-provided unit, and unique-violation detection recognizes code 23505.
func TestCreditUnitStampingAndHelpers(t *testing.T) {
	stamped := stampCreditUnit(map[string]any{"k": "v"})
	if stamped["credit_unit"] != CreditUnitV2 {
		t.Fatalf("stamp missing: %+v", stamped)
	}
	callerUnit := stampCreditUnit(map[string]any{"credit_unit": "caller-unit"})
	if callerUnit["credit_unit"] != "caller-unit" {
		t.Fatalf("caller unit overwritten: %+v", callerUnit)
	}
	if stampCreditUnit(nil)["credit_unit"] != CreditUnitV2 {
		t.Fatalf("nil metadata not stamped")
	}
	source := map[string]any{"k": "v"}
	_ = stampCreditUnit(source)
	if _, polluted := source["credit_unit"]; polluted {
		t.Fatal("stamping mutated the caller's metadata map")
	}

	if normalizeMetadata(nil) == nil {
		t.Fatal("nil metadata must normalize to an empty map, not nil")
	}

	pgErr := &pgconn.PgError{Code: "23505"}
	if !isUniqueViolation(pgErr) {
		t.Fatal("23505 not recognized as unique violation")
	}
	if isUniqueViolation(&pgconn.PgError{Code: "22P02"}) {
		t.Fatal("non-unique code misclassified")
	}
	if isUniqueViolation(errors.New("not a pg error")) {
		t.Fatal("plain error misclassified")
	}
}

// TestShouldLogAnomalyThrottles pins the operator-signal contract: the first
// sighting of an anomaly logs, immediate repeats within the window do not.
func TestShouldLogAnomalyThrottles(t *testing.T) {
	svc := NewService(newStubRepo())
	accountID := uuid.New()

	if !svc.shouldLogAnomaly(accountID) {
		t.Fatal("first anomaly sighting must log")
	}
	if svc.shouldLogAnomaly(accountID) {
		t.Fatal("repeat sighting within the window must be throttled")
	}

	other := uuid.New()
	if !svc.shouldLogAnomaly(other) {
		t.Fatal("throttle must be per account")
	}

	svc.anomalyLogged.Store(accountID, time.Now().Add(-16*time.Minute))
	if !svc.shouldLogAnomaly(accountID) {
		t.Fatal("sighting outside the window must log again")
	}
}

// TestGetBalancePropagatesRepoError keeps the fail-loud contract on reads: a
// repository failure surfaces instead of being answered with zeros.
func TestGetBalancePropagatesRepoError(t *testing.T) {
	repo := newStubRepo()
	repo.balanceErr = errors.New("db down")
	svc := NewService(repo)
	if _, err := svc.GetBalance(context.Background(), uuid.New()); err == nil {
		t.Fatal("expected repo error to propagate")
	}
}

// TestValidationErrorHelpers pins the error-contract helpers other packages
// switch on when mapping ledger failures to HTTP responses.
func TestValidationErrorHelpers(t *testing.T) {
	ve := &ValidationError{Field: "credits", Message: "credits must be greater than zero"}
	if ve.Error() != "credits must be greater than zero" {
		t.Fatalf("Error() = %q", ve.Error())
	}

	var target *ValidationError
	if !AsValidationError(fmt.Errorf("wrapped: %w", ve), &target) {
		t.Fatal("AsValidationError must unwrap wrapped validation errors")
	}
	if target.Field != "credits" {
		t.Fatalf("decoded wrong error: %+v", target)
	}
	if AsValidationError(errors.New("other"), &target) {
		t.Fatal("non-validation error must not match")
	}
}

// TestListEntriesServiceWrapper_Live covers the legacy ListEntries surface
// against real rows, including its default limit of 20.
func TestListEntriesServiceWrapper_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	ctx := context.Background()
	svc := NewService(NewPgxRepository(pool))

	for i := 0; i < 3; i++ {
		if _, err := svc.GrantCredits(ctx, accountID, uuid.NewString(), int64(10+i), nil); err != nil {
			t.Fatalf("grant %d: %v", i, err)
		}
	}

	entries, err := svc.ListEntries(ctx, accountID, 0)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want all 3", len(entries))
	}

	entries, err = svc.ListEntries(ctx, accountID, -1)
	if err != nil {
		t.Fatalf("list entries negative limit: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("negative limit must fall back to the default page size, got %d", len(entries))
	}
}
