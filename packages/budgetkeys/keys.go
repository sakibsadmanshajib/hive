// Package budgetkeys holds the Redis key shapes that the control-plane budget
// writers and the edge-api budget gate have to agree on.
//
// It exists because they cannot agree by reading each other: both live under an
// `internal/` tree rooted at their own app, so neither can import the other.
// That is the structural reason issue #1651 happened at all, a gate reading a
// key nothing wrote, for months, with tests on both sides passing.
//
// The first repair for that was a literal pinned in each app's tests. It caught
// a change made on one side alone and missed the change that actually causes
// this class of defect: a coherent edit, where whoever moves the key builder
// also moves their own test constant, leaves the other app untouched, and
// nothing anywhere compares the two. One definition imported by both cannot
// drift, so this package replaces that convention rather than documenting it.
//
// Keys are hash-tagged on the workspace id (`{...}`) so a clustered deployment
// keeps one workspace's keys in one slot, and period-suffixed so a new billing
// month starts empty with nothing scheduled to reset it.
package budgetkeys

import (
	"fmt"
	"time"
)

// PeriodLayout is how a billing period is stamped into a key. Month precision:
// the period is a calendar month, in UTC, matching the invoice period and the
// spend-alert pass.
const PeriodLayout = "2006-01"

// HardCap is the workspace's hard cap, in BDT subunits, published by the
// control-plane budgets service and read by the edge-api gate.
func HardCap(workspaceID string) string {
	return fmt.Sprintf("budget:hard_cap:{%s}", workspaceID)
}

// MTDSpend is the workspace's month-to-date spend, in BDT subunits, written by
// the control-plane settlement counter and read by the edge-api gate. The unit
// is subunits and not ledger credits: a credit is one billionth of a USD and a
// paisa is one hundredth of a taka, so a counter in credits would trip the gate
// at roughly one ten-millionth of the intended spend.
func MTDSpend(workspaceID string, period time.Time) string {
	return fmt.Sprintf("budget:mtd_spend:{%s}:%s", workspaceID, period.UTC().Format(PeriodLayout))
}

// MTDCredits is the exact accumulator behind MTDSpend, in ledger credits.
// Control-plane internal: the gate never reads it, because the gate holds no
// conversion rate. It lives here so all three keys for a workspace are defined
// together and cannot drift apart in shape.
func MTDCredits(workspaceID string, period time.Time) string {
	return fmt.Sprintf("budget:mtd_credits:{%s}:%s", workspaceID, period.UTC().Format(PeriodLayout))
}
