package budgetkeys_test

import (
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/packages/budgetkeys"
)

const workspace = "11111111-2222-3333-4444-555555555555"

// TestKeysAreTheDeployedShape pins the exact bytes both apps put on the wire.
// Changing a key shape is a deployment concern, not a refactor: the writer and
// the reader are separate processes that roll separately, so a rename here
// leaves the running gate reading the old key and the running control-plane
// writing the new one, which is issue #1651 all over again. This test is what
// makes that change loud enough to require a plan.
func TestKeysAreTheDeployedShape(t *testing.T) {
	period := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"hard cap", budgetkeys.HardCap(workspace), "budget:hard_cap:{11111111-2222-3333-4444-555555555555}"},
		{"mtd spend", budgetkeys.MTDSpend(workspace, period), "budget:mtd_spend:{11111111-2222-3333-4444-555555555555}:2026-09"},
		{"mtd credits", budgetkeys.MTDCredits(workspace, period), "budget:mtd_credits:{11111111-2222-3333-4444-555555555555}:2026-09"},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s key is %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestPeriodIsNormalisedToUTC keeps two callers in different zones from writing
// two different keys for one billing month. The counter passes a settlement
// timestamp and the gate passes its own clock, so the normalisation has to live
// here rather than in either caller's discipline.
func TestPeriodIsNormalisedToUTC(t *testing.T) {
	utc := time.Date(2026, 10, 1, 0, 30, 0, 0, time.UTC)
	dhaka := utc.In(time.FixedZone("Asia/Dhaka", 6*60*60)) // 06:30 on the same instant

	if budgetkeys.MTDSpend(workspace, utc) != budgetkeys.MTDSpend(workspace, dhaka) {
		t.Fatalf("one instant produced two keys: %q and %q",
			budgetkeys.MTDSpend(workspace, utc), budgetkeys.MTDSpend(workspace, dhaka))
	}
	// The instant above is 30 minutes into October UTC and 06:30 on 1 October in
	// Dhaka, so both must say October. A zone-local format would answer
	// September for a caller half a day west.
	if got := budgetkeys.MTDSpend(workspace, dhaka); got != "budget:mtd_spend:{"+workspace+"}:2026-10" {
		t.Fatalf("period suffix is %q, want the UTC month", got)
	}
}
