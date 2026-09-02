package budgets_test

import (
	"context"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/budgets"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
	platformredis "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/redis"
)

// =============================================================================
// Month-to-date spend counter (issue #1651)
//
// The edge-api budget gate refuses a request when the workspace's month-to-date
// spend has reached the hard cap the customer set on /console/billing/budget.
// It reads both numbers from Redis, in BDT subunits, and until this counter
// existed nothing ever wrote the spend one, so the comparison was always zero
// against the cap and no request was ever refused.
//
// These tests run against a real Redis (miniredis) rather than a fake map, so
// the key literals asserted here are the bytes a deployed control-plane puts on
// the wire. Their counterparts in apps/edge-api/internal/limits pin the same
// literals from the reading side; see budget_gate_contract_test.go there.
//
// The pinned rate is 100 BDT per USD, set by this package's TestMain, and every
// seeded quantity goes through spendCredits so an assertion talks in taka while
// the ledger talks in credits.
// =============================================================================

// The workspace id and keys this package shares with the edge-api gate. Written
// out rather than derived so a change to either side's key shape turns that
// side's own test red.
const (
	counterWorkspaceID  = "11111111-2222-3333-4444-555555555555"
	counterMTDKey       = "budget:mtd_spend:{11111111-2222-3333-4444-555555555555}:2026-09"
	counterHardCapKey   = "budget:hard_cap:{11111111-2222-3333-4444-555555555555}"
	counterMTDSubunits  = "100000" // one thousand taka, in paisa
	counterMTDInCredits = "10000000000"
)

// counterAt is inside September 2026, matching the period suffix above.
var counterAt = time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

func newCounter(t *testing.T, repo budgets.WorkspaceBudgetRepository) (*budgets.MTDSpendCounter, *goredis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	rate, err := payments.PlatformUSDBDTRate()
	if err != nil {
		t.Fatalf("resolve rate: %v", err)
	}
	// Guards the fixture itself: every assertion below is stated in taka at
	// exactly this rate, so a TestMain that stopped pinning it would leave them
	// measuring the deployment default instead.
	if rate.Display != "100.000000" {
		t.Fatalf("fixture rate is %s, want 100.000000", rate.Display)
	}
	return budgets.NewMTDSpendCounter(client, repo, rate, nil), client
}

func mustGet(t *testing.T, client *goredis.Client, key string) string {
	t.Helper()
	val, err := client.Get(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	return val
}

// TestRecordSettledSpend_WritesTheSubunitCounterTheEdgeGateReads is the
// regression guard for the defect itself: a settled charge must move the exact
// key the gate reads, in the unit the cap is stored in.
func TestRecordSettledSpend_WritesTheSubunitCounterTheEdgeGateReads(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	counter, client := newCounter(t, repo)
	ws := uuid.MustParse(counterWorkspaceID)

	// One thousand taka of settled spend, expressed as the credit quantity the
	// ledger would hold.
	if err := counter.RecordSettledSpend(context.Background(), ws, spendCredits(100_000).Int64(), counterAt); err != nil {
		t.Fatalf("RecordSettledSpend: %v", err)
	}

	got := mustGet(t, client, counterMTDKey)
	if got == counterMTDInCredits {
		t.Fatalf("counter wrote %s ledger credits into a key the gate reads as BDT subunits; "+
			"the gate would refuse this workspace at one ten-millionth of its cap", got)
	}
	if got != counterMTDSubunits {
		t.Fatalf("counter holds %q, want %q paisa", got, counterMTDSubunits)
	}
}

// TestRecordSettledSpend_ConvertsTheRunningTotalNotEachCharge pins the reason
// the accumulator is kept in credits: converting per charge rounds repeatedly.
// The case below is its worst form, charges worth less than half a paisa each,
// where every conversion floors to zero and the counter never leaves the floor
// no matter how much is spent. Larger charges do move it and instead accumulate
// up to half a paisa of error per settlement with a consistent sign, which this
// test does not measure and the comment on the file does describe.
func TestRecordSettledSpend_ConvertsTheRunningTotalNotEachCharge(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	counter, client := newCounter(t, repo)
	ws := uuid.MustParse(counterWorkspaceID)

	// A tenth of a paisa each, ten of them, so the month totals exactly one
	// paisa. Per-charge conversion rounds each to zero and reports nothing.
	perCharge := spendCredits(1).Int64() / 10
	for i := 0; i < 10; i++ {
		if err := counter.RecordSettledSpend(context.Background(), ws, perCharge, counterAt); err != nil {
			t.Fatalf("RecordSettledSpend %d: %v", i, err)
		}
	}

	if got := mustGet(t, client, counterMTDKey); got != "1" {
		t.Fatalf("counter holds %q after ten tenth-of-a-paisa charges, want \"1\"", got)
	}
}

// TestRecordSettledSpend_ReseedsFromTheLedgerWhenTheCounterIsMissing covers the
// two ways Redis loses the month: a restart or eviction mid-month, and the
// deploy that first ships this code into a month already part spent. The ledger
// is the authority, so the first settlement after the key goes missing takes
// the month's real total from it rather than restarting the customer's cap at
// zero. It also republishes the cap, which is what an existing customer's
// budget needs after a Redis restart drops it.
func TestRecordSettledSpend_ReseedsFromTheLedgerWhenTheCounterIsMissing(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	counter, client := newCounter(t, repo)
	ws := uuid.MustParse(counterWorkspaceID)

	// The ledger says two thousand five hundred taka have been spent this
	// month, including the charge being settled right now.
	repo.mtd[ws] = spendCredits(250_000)
	repo.budgets[ws] = &budgets.Budget{
		WorkspaceID: ws,
		PeriodStart: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		SoftCap:     big.NewInt(400_000),
		HardCap:     big.NewInt(500_000),
		Currency:    "BDT",
	}

	if err := counter.RecordSettledSpend(context.Background(), ws, spendCredits(100).Int64(), counterAt); err != nil {
		t.Fatalf("RecordSettledSpend: %v", err)
	}

	if got := mustGet(t, client, counterMTDKey); got != "250000" {
		t.Fatalf("counter holds %q, want the ledger's 250000 paisa", got)
	}
	if got := mustGet(t, client, counterHardCapKey); got != "500000" {
		t.Fatalf("hard cap republished as %q, want 500000", got)
	}
}

// TestRecordSettledSpend_KeepsAccumulatingAfterTheReseed proves the reseed does
// not fire on every charge: once the counter exists it is the accumulator, and
// the ledger is consulted no further.
func TestRecordSettledSpend_KeepsAccumulatingAfterTheReseed(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	counter, client := newCounter(t, repo)
	ws := uuid.MustParse(counterWorkspaceID)
	repo.mtd[ws] = spendCredits(100_000)

	ctx := context.Background()
	if err := counter.RecordSettledSpend(ctx, ws, spendCredits(100_000).Int64(), counterAt); err != nil {
		t.Fatalf("first settle: %v", err)
	}
	// The ledger stops moving (a stale fake), so a counter that re-read it on
	// every charge would freeze at a thousand taka.
	if err := counter.RecordSettledSpend(ctx, ws, spendCredits(50_000).Int64(), counterAt); err != nil {
		t.Fatalf("second settle: %v", err)
	}

	if got := mustGet(t, client, counterMTDKey); got != "150000" {
		t.Fatalf("counter holds %q, want 150000 paisa", got)
	}
}

// TestRecordSettledSpend_SeparatesPeriods proves the month suffix does the
// resetting, so a new billing period starts at zero with nothing to run.
func TestRecordSettledSpend_SeparatesPeriods(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	counter, client := newCounter(t, repo)
	ws := uuid.MustParse(counterWorkspaceID)

	ctx := context.Background()
	if err := counter.RecordSettledSpend(ctx, ws, spendCredits(100_000).Int64(), counterAt); err != nil {
		t.Fatalf("september settle: %v", err)
	}
	if err := counter.RecordSettledSpend(ctx, ws, spendCredits(700).Int64(), counterAt.AddDate(0, 1, 0)); err != nil {
		t.Fatalf("october settle: %v", err)
	}

	if got := mustGet(t, client, counterMTDKey); got != counterMTDSubunits {
		t.Fatalf("september counter moved to %q", got)
	}
	october := "budget:mtd_spend:{" + counterWorkspaceID + "}:2026-10"
	if got := mustGet(t, client, october); got != "700" {
		t.Fatalf("october counter holds %q, want 700", got)
	}
}

// TestRecordSettledSpend_IgnoresNonPositiveCharges keeps a released or
// zero-credit settlement from creating an empty counter, and from spending two
// Redis round trips to record nothing.
func TestRecordSettledSpend_IgnoresNonPositiveCharges(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	counter, client := newCounter(t, repo)
	ws := uuid.MustParse(counterWorkspaceID)

	ctx := context.Background()
	for _, credits := range []int64{0, -5} {
		if err := counter.RecordSettledSpend(ctx, ws, credits, counterAt); err != nil {
			t.Fatalf("RecordSettledSpend(%d): %v", credits, err)
		}
	}

	if _, err := client.Get(ctx, counterMTDKey).Result(); !errors.Is(err, goredis.Nil) {
		t.Fatalf("a non-charge created the counter key: %v", err)
	}
}

// TestRecordSettledSpend_ReportsRedisFailure fixes the failure posture in the
// test suite rather than only in a comment. The counter reports the failure to
// its caller; the caller (accounting.finalizeLocked) logs it and settles the
// charge anyway, which is asserted in the accounting package. Silence here
// would make an unenforced cap invisible.
func TestRecordSettledSpend_ReportsRedisFailure(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	counter, client := newCounter(t, repo)
	if err := client.Close(); err != nil {
		t.Fatalf("close client: %v", err)
	}

	err := counter.RecordSettledSpend(context.Background(), uuid.MustParse(counterWorkspaceID), spendCredits(100).Int64(), counterAt)
	if err == nil {
		t.Fatal("a dead Redis was reported as a recorded spend")
	}
}

// TestSetBudget_PublishesTheHardCapWellBeyondTheSyncInterval is the second half
// of why the gate never blocked. The cap was published with a thirty second TTL
// under a comment claiming the gate would read through on a miss; the gate does
// no such thing, it treats a missing cap as "no budget configured", and nothing
// rewrote the key. So a saved cap stopped being enforced thirty seconds after
// the customer typed it.
//
// A TTL is correct now only because the spend-alert pass restates every live cap
// every sixty seconds, which makes the expiry reachable only by a key nothing is
// refreshing: an orphan left by a delete whose Redis DEL failed. The margin
// between the two is the whole safety argument, so this asserts the margin and
// not merely that some TTL is set.
func TestSetBudget_PublishesTheHardCapWellBeyondTheSyncInterval(t *testing.T) {
	mr := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	repo := newFakeWorkspaceRepo()
	svc := budgets.NewServiceWithWorkspace(&mockRepo{}, &mockNotifier{}, repo, &fakeAlertNotifier{}, client)

	ws := uuid.MustParse(counterWorkspaceID)
	if _, err := svc.SetBudget(context.Background(), budgets.SetBudgetInput{
		WorkspaceID: ws,
		SoftCap:     big.NewInt(400_000),
		HardCap:     big.NewInt(500_000),
	}); err != nil {
		t.Fatalf("SetBudget: %v", err)
	}

	if got := mustGet(t, client, counterHardCapKey); got != "500000" {
		t.Fatalf("published cap %q, want 500000", got)
	}
	// The pass runs every 60 seconds. Anything close to that turns a couple of
	// missed passes into a cap that silently stops being enforced, which is the
	// defect this issue is about.
	const syncInterval = 60 * time.Second
	ttl := mr.TTL(counterHardCapKey)
	if ttl < 100*syncInterval {
		t.Fatalf("published cap expires in %s, less than 100 sync intervals; a cap that evaporates stops being enforced", ttl)
	}
	if ttl == 0 {
		t.Fatal("published cap never expires; an orphan left by a failed delete would block a customer forever")
	}

	if err := svc.DeleteBudget(context.Background(), ws); err != nil {
		t.Fatalf("DeleteBudget: %v", err)
	}
	if _, err := client.Get(context.Background(), counterHardCapKey).Result(); !errors.Is(err, goredis.Nil) {
		t.Fatalf("cap survived DeleteBudget: %v", err)
	}
}

// TestRecordSettledSpend_ClearsACapWithNoBudgetRow is the repair for the race
// the adversarial review on PR #1677 raised. The publish on upsert and the
// removal on delete are unordered against each other, and now that the key
// carries no expiry, a publish landing after a removal would leave a customer
// refused under a cap they had taken off, with nothing to ever clear it. The
// row is authoritative, so the period rebuild drops a cap the database does not
// have.
func TestRecordSettledSpend_ClearsACapWithNoBudgetRow(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	counter, client := newCounter(t, repo)
	ws := uuid.MustParse(counterWorkspaceID)

	ctx := context.Background()
	// A cap left behind by a publish that outlived its row.
	if err := client.Set(ctx, counterHardCapKey, "50000", 0).Err(); err != nil {
		t.Fatalf("seed stale cap: %v", err)
	}

	if err := counter.RecordSettledSpend(ctx, ws, spendCredits(100).Int64(), counterAt); err != nil {
		t.Fatalf("RecordSettledSpend: %v", err)
	}

	if _, err := client.Get(ctx, counterHardCapKey).Result(); !errors.Is(err, goredis.Nil) {
		t.Fatalf("a cap with no budget row survived the period rebuild: %v", err)
	}
}

// TestRecordSettledSpend_IsBoundedWhenRedisHangs is the regression guard for the
// gap between "Redis errors" and "Redis stops answering". The fail-open posture
// handles the first: a refused connection fails immediately and the caller logs
// and continues. The second does not fail for tens of seconds, because
// platform/redis.NewClient sets no timeouts and go-redis then defaults to a 3
// second read with retries, and every one of those seconds would be spent
// holding the per-account advisory lock this runs inside.
//
// The server below accepts the connection and never answers, which is exactly
// that case. Remove the bound in RecordSettledSpend and this test stops
// finishing inside its own threshold.
func TestRecordSettledSpend_IsBoundedWhenRedisHangs(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Hold it open, answer nothing, close only when the test ends.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	// Built the way production builds it, because the bound depends on how the
	// client is configured and not only on the deadline the counter sets: a
	// go-redis client without ContextTimeoutEnabled ignores the context for
	// command reads entirely. Constructing a bare client here would have tested
	// a client this product does not use.
	client := platformredis.NewClient(listener.Addr().String())
	t.Cleanup(func() { _ = client.Close() })
	rate, err := payments.PlatformUSDBDTRate()
	if err != nil {
		t.Fatalf("resolve rate: %v", err)
	}
	counter := budgets.NewMTDSpendCounter(client, newFakeWorkspaceRepo(), rate, nil)

	start := time.Now()
	err = counter.RecordSettledSpend(context.Background(), uuid.MustParse(counterWorkspaceID), spendCredits(100).Int64(), counterAt)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a Redis that never answers was reported as a recorded spend")
	}
	// Two seconds is well inside the go-redis defaults an unbounded call would
	// wait (a 3 second read, retried), and far outside the 500ms bound.
	if elapsed > 2*time.Second {
		t.Fatalf("settlement waited %s on a hung Redis while holding the account lock", elapsed)
	}
}

// TestSyncWorkspace_RestatesBothKeysAndTheCap covers what the settlement path
// cannot: a counter write that failed while its key was alive is not a missing
// key, so no rebuild fires and that charge is gone from the cap for the rest of
// the month. The spend-alert pass calls this once a minute for every workspace
// with a budget, so the gate's view converges on the ledger on a schedule.
func TestSyncWorkspace_RestatesBothKeysAndTheCap(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	counter, client := newCounter(t, repo)
	ws := uuid.MustParse(counterWorkspaceID)
	ctx := context.Background()

	// The counter is behind: it holds one hundred taka where the ledger says
	// three thousand, which is what a dropped write leaves.
	if err := counter.RecordSettledSpend(ctx, ws, spendCredits(10_000).Int64(), counterAt); err != nil {
		t.Fatalf("settle: %v", err)
	}

	if err := counter.SyncWorkspace(ctx, ws, spendCredits(300_000), big.NewInt(500_000), counterAt); err != nil {
		t.Fatalf("SyncWorkspace: %v", err)
	}

	if got := mustGet(t, client, counterMTDKey); got != "300000" {
		t.Fatalf("gate counter holds %q, want the ledger's 300000 paisa", got)
	}
	if got := mustGet(t, client, counterHardCapKey); got != "500000" {
		t.Fatalf("cap republished as %q, want 500000", got)
	}
	// Refreshing the expiry is the point of republishing it: the TTL is what
	// collects a cap orphaned by a failed delete, and the pass is what keeps
	// every live cap well inside it.
	if ttl := client.TTL(ctx, counterHardCapKey).Val(); ttl <= time.Hour {
		t.Fatalf("republished cap carries %s of life; the pass must refresh it far ahead of its expiry", ttl)
	}

	// The accumulator has to move too, or the next settlement increments the
	// stale figure and undoes the correction on its own re-render.
	if err := counter.RecordSettledSpend(ctx, ws, spendCredits(100).Int64(), counterAt); err != nil {
		t.Fatalf("settle after sync: %v", err)
	}
	if got := mustGet(t, client, counterMTDKey); got != "300100" {
		t.Fatalf("counter holds %q after a charge on top of the synced total, want 300100", got)
	}
}

// TestSyncWorkspace_RefusesAnUnusableTotal keeps a corrupt ledger read from
// silently zeroing a customer's counted spend.
func TestSyncWorkspace_RefusesAnUnusableTotal(t *testing.T) {
	counter, client := newCounter(t, newFakeWorkspaceRepo())
	ws := uuid.MustParse(counterWorkspaceID)
	ctx := context.Background()

	tooBig := new(big.Int).Lsh(big.NewInt(1), 70)
	for _, total := range []*big.Int{nil, big.NewInt(-1), tooBig} {
		if err := counter.SyncWorkspace(ctx, ws, total, big.NewInt(500_000), counterAt); err == nil {
			t.Fatalf("SyncWorkspace accepted %v", total)
		}
	}
	if _, err := client.Get(ctx, counterMTDKey).Result(); !errors.Is(err, goredis.Nil) {
		t.Fatal("a refused sync wrote the counter anyway")
	}
}
