package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The console's billing overview rendered "the last five ledger events" as
// 11, 16, 26, 16, 24 August, and the paginated Ledger tab was shuffled the
// same way, because both read ListEntriesWithCursor and it ordered by `id`.
// `id` is gen_random_uuid (supabase/migrations/20260330_01_credits_ledger.sql),
// a v4 UUID, so it carries no time order whatsoever.
//
// The ids below are fixed rather than generated, and chosen so that id order
// is the exact inverse of time order. That makes the test deterministic in
// both directions: the old ORDER BY id DESC returns this set oldest-first
// every single run, never accidentally chronological.
var orderFixture = []struct {
	id        uuid.UUID
	createdAt time.Time
	credits   int64
}{
	{uuid.MustParse("00000000-0000-4000-8000-000000000001"), time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC), 500},
	{uuid.MustParse("00000000-0000-4000-8000-000000000002"), time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC), 400},
	{uuid.MustParse("00000000-0000-4000-8000-000000000003"), time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC), 300},
	{uuid.MustParse("00000000-0000-4000-8000-000000000004"), time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC), 200},
}

// A tie group: four entries on one timestamp, which is what the `id DESC`
// tie-breaker and the (created_at, id) row comparison exist for. Ordered
// newest-first the group must come back id-descending, and a page boundary
// falling inside it must neither skip nor repeat a row. The timestamp sits
// between fixture rows 2 and 3 so the group has neighbours on both sides.
//
// Ordered by id ascending here, so the expected newest-first order is this
// slice reversed. Without the tie-breaker the group's internal order is
// whatever the heap hands back and a cursor inside it has no defined position
// at all, which is the failure this covers.
var tieFixture = []uuid.UUID{
	uuid.MustParse("00000000-0000-4000-8000-0000000000a1"),
	uuid.MustParse("00000000-0000-4000-8000-0000000000a2"),
	uuid.MustParse("00000000-0000-4000-8000-0000000000a3"),
	uuid.MustParse("00000000-0000-4000-8000-0000000000a4"),
}

var tieCreatedAt = time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

func seedOrderFixture(t *testing.T, accountID uuid.UUID) {
	t.Helper()
	pool := newLedgerTestPool(t)
	ctx := context.Background()

	for i, row := range orderFixture {
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.credit_ledger_entries
			   (id, account_id, entry_type, credits_delta, idempotency_key, created_at)
			 VALUES ($1, $2, 'grant', $3, $4, $5)`,
			row.id, accountID, row.credits, "order-fixture-"+row.id.String(), row.createdAt,
		); err != nil {
			t.Fatalf("seed fixture row %d: %v", i, err)
		}
	}
}

func TestListEntriesWithCursorIsChronological_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	seedOrderFixture(t, accountID)

	entries, err := NewPgxRepository(pool).ListEntriesWithCursor(
		context.Background(),
		ListEntriesFilter{AccountID: accountID, Limit: 10},
	)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	if len(entries) != len(orderFixture) {
		t.Fatalf("got %d entries, want %d", len(entries), len(orderFixture))
	}

	for i, entry := range entries {
		if entry.ID != orderFixture[i].id {
			t.Fatalf(
				"position %d is entry %s (%s), want %s (%s): the page is not newest-first",
				i, entry.ID, entry.CreatedAt.UTC().Format(time.RFC3339),
				orderFixture[i].id, orderFixture[i].createdAt.Format(time.RFC3339),
			)
		}
	}
}

func TestListEntriesWithCursorPagesInChronologicalOrder_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	seedOrderFixture(t, accountID)

	ctx := context.Background()
	repo := NewPgxRepository(pool)

	first, err := repo.ListEntriesWithCursor(ctx, ListEntriesFilter{AccountID: accountID, Limit: 2})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 2 || first[0].ID != orderFixture[0].id || first[1].ID != orderFixture[1].id {
		t.Fatalf("first page = %v, want the two newest entries", ids(first))
	}

	// The cursor the HTTP layer hands back is the last entry's id (see
	// handleListLedger in http.go), so the keyset has to resolve the sort key
	// from that id rather than compare ids directly.
	cursor := first[len(first)-1].ID
	second, err := repo.ListEntriesWithCursor(ctx, ListEntriesFilter{
		AccountID: accountID,
		Limit:     2,
		Cursor:    &cursor,
	})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second) != 2 || second[0].ID != orderFixture[2].id || second[1].ID != orderFixture[3].id {
		t.Fatalf("second page = %v, want the two older entries in order", ids(second))
	}

	// A page must never repeat a row the previous page already showed.
	for _, entry := range second {
		for _, seen := range first {
			if entry.ID == seen.ID {
				t.Fatalf("entry %s appears on both pages", entry.ID)
			}
		}
	}
}

func TestListEntriesWithCursorRejectsUnknownCursor_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	seedOrderFixture(t, accountID)

	unknown := uuid.MustParse("00000000-0000-4000-8000-0000000000ff")
	entries, err := NewPgxRepository(pool).ListEntriesWithCursor(
		context.Background(),
		ListEntriesFilter{AccountID: accountID, Limit: 10, Cursor: &unknown},
	)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}
	// A cursor naming no row has no position in the ordering, so the honest
	// answer is an empty page rather than an arbitrary slice of the history.
	if len(entries) != 0 {
		t.Fatalf("got %d entries for an unknown cursor, want 0: %v", len(entries), ids(entries))
	}
}

func seedTieFixture(t *testing.T, accountID uuid.UUID) {
	t.Helper()
	pool := newLedgerTestPool(t)
	ctx := context.Background()

	for i, id := range tieFixture {
		if _, err := pool.Exec(ctx,
			`INSERT INTO public.credit_ledger_entries
			   (id, account_id, entry_type, credits_delta, idempotency_key, created_at)
			 VALUES ($1, $2, 'grant', $3, $4, $5)`,
			id, accountID, int64(10+i), "tie-fixture-"+id.String(), tieCreatedAt,
		); err != nil {
			t.Fatalf("seed tie row %d: %v", i, err)
		}
	}
}

// Entries sharing a created_at must still come back in one total order, and a
// page boundary landing inside that group must not skip or repeat a row. That
// is the whole job of the `id DESC` tie-breaker and of comparing
// (created_at, id) as a row rather than comparing created_at alone.
func TestListEntriesWithCursorBreaksTiesTotally_Live(t *testing.T) {
	pool := newLedgerTestPool(t)
	accountID := seedLedgerAccount(t, pool)
	seedTieFixture(t, accountID)

	ctx := context.Background()
	repo := NewPgxRepository(pool)

	// Newest-first over a tie group is id descending, so the reverse of the
	// fixture slice.
	want := make([]uuid.UUID, 0, len(tieFixture))
	for i := len(tieFixture) - 1; i >= 0; i-- {
		want = append(want, tieFixture[i])
	}

	whole, err := repo.ListEntriesWithCursor(ctx, ListEntriesFilter{AccountID: accountID, Limit: 10})
	if err != nil {
		t.Fatalf("list whole tie group: %v", err)
	}
	if len(whole) != len(want) {
		t.Fatalf("got %d entries, want %d", len(whole), len(want))
	}
	for i, entry := range whole {
		if entry.ID != want[i] {
			t.Fatalf("tie group position %d is %s, want %s: ties are not broken by id", i, entry.ID, want[i])
		}
	}

	// Now walk it two at a time, so both page boundaries fall inside the tie
	// group, and check the union is exactly the group with nothing repeated.
	var seen []uuid.UUID
	var cursor *uuid.UUID
	for page := 0; page < 4; page++ {
		entries, err := repo.ListEntriesWithCursor(ctx, ListEntriesFilter{
			AccountID: accountID,
			Limit:     2,
			Cursor:    cursor,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			for _, already := range seen {
				if entry.ID == already {
					t.Fatalf("entry %s returned twice while paging a tie group", entry.ID)
				}
			}
			seen = append(seen, entry.ID)
		}
		last := entries[len(entries)-1].ID
		cursor = &last
	}

	if len(seen) != len(want) {
		t.Fatalf("paging a tie group two at a time yielded %d of %d entries: %v", len(seen), len(want), seen)
	}
	for i, id := range seen {
		if id != want[i] {
			t.Fatalf("paged position %d is %s, want %s", i, id, want[i])
		}
	}
}

func ids(entries []LedgerEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.ID.String()+"@"+entry.CreatedAt.UTC().Format("2006-01-02"))
	}
	return out
}
