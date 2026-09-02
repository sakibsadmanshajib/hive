package limits

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// =============================================================================
// Cross-module contract with the writer (issue #1651)
//
// The counter this gate reads is written by
// apps/control-plane/internal/budgets/mtd_counter.go. Neither module can
// import the other (both live under an `internal/` tree rooted at their own
// app), which is the structural reason a reader and a writer sat in this
// repository for months without meeting: nothing could fail when only one of
// them existed.
//
// The join is therefore a literal, spelled out on BOTH sides rather than
// derived from either side's helper. The counterpart assertions are
// TestRecordSettledSpend_WritesTheSubunitCounterTheEdgeGateReads and
// TestGateKeysMatchTheEdgeAPIContract in that package, which pin the same
// three strings against a real Redis. Change the key shape on either side and
// that side's test goes red.
//
// The value is a BDT subunit count, never ledger credits. A credit is one
// billionth of a USD and a paisa is one hundredth of a taka, so a counter in
// credits would trip this gate at roughly one ten-millionth of the intended
// spend (issue #1648 pointed the other way).
// =============================================================================

const (
	// A workspace id, formatted the way both sides format it: inside Redis
	// hash-tag braces so a clustered deployment keeps a workspace's keys on
	// one slot.
	contractWorkspaceID = "11111111-2222-3333-4444-555555555555"

	contractMTDKey     = "budget:mtd_spend:{11111111-2222-3333-4444-555555555555}:2026-09"
	contractHardCapKey = "budget:hard_cap:{11111111-2222-3333-4444-555555555555}"

	// One thousand taka of settled spend, in paisa, exactly as the
	// control-plane counter records it after converting the month's ledger
	// credits at the platform rate.
	contractMTDSubunits = "100000"
)

func contractClock() func() time.Time {
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return at }
}

func contractGate(t *testing.T, cache CacheReader) *BudgetGate {
	t.Helper()
	gate, err := New(Config{
		Cache:                cache,
		WorkspaceFromRequest: func(*http.Request) (string, bool) { return contractWorkspaceID, true },
		Now:                  contractClock(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gate
}

// TestGateRefusesSpendPastTheCapTheCustomerSet is the behavioural half of
// issue #1651. The console advertises the hard cap as "Requests blocked beyond
// this", and until the writer landed nothing was ever blocked. The values here
// are the writer's own output, not a shape invented by this test.
func TestGateRefusesSpendPastTheCapTheCustomerSet(t *testing.T) {
	cache := newFakeCache()
	// Customer typed a five hundred taka cap; the month has settled a
	// thousand taka of spend.
	cache.set(contractHardCapKey, "50000")
	cache.set(contractMTDKey, contractMTDSubunits)

	served := false
	rec := httptest.NewRecorder()
	contractGate(t, cache).Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		served = true
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if served {
		t.Fatal("request past the hard cap reached the upstream handler")
	}
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status %d, want 402", rec.Code)
	}
	var body hardCapExceededBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode 402 body: %v", err)
	}
	if body.Error.Code != "budget_hard_cap_exceeded" {
		t.Fatalf("error code %q, want budget_hard_cap_exceeded", body.Error.Code)
	}
	if body.Error.MTDBDTSubunits != contractMTDSubunits {
		t.Fatalf("402 reports mtd %q, want %q", body.Error.MTDBDTSubunits, contractMTDSubunits)
	}
}

// TestGateServesSpendBelowTheCap is the other half: the gate must not be a
// blanket refusal. Same counter value, a cap the customer set above it.
func TestGateServesSpendBelowTheCap(t *testing.T) {
	cache := newFakeCache()
	// Two thousand taka cap against the same thousand taka of spend.
	cache.set(contractHardCapKey, "200000")
	cache.set(contractMTDKey, contractMTDSubunits)

	served := false
	rec := httptest.NewRecorder()
	contractGate(t, cache).Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))

	if !served {
		t.Fatal("request below the hard cap was refused")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
}

// TestGateKeysMatchTheControlPlaneWriter pins this side of the literal. Its
// counterpart lives in the control-plane budgets package and pins the same two
// strings against the keys that package actually writes into Redis.
func TestGateKeysMatchTheControlPlaneWriter(t *testing.T) {
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	if got := MTDSpendRedisKeyPattern(contractWorkspaceID, period); got != contractMTDKey {
		t.Fatalf("mtd key %q, want %q", got, contractMTDKey)
	}
	if got := HardCapRedisKeyPattern(contractWorkspaceID); got != contractHardCapKey {
		t.Fatalf("hard cap key %q, want %q", got, contractHardCapKey)
	}
}
