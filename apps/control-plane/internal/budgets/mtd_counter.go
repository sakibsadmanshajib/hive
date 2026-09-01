package budgets

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// =============================================================================
// Month-to-date spend counter (issue #1651)
//
// The edge-api budget gate refuses a request once a workspace's month-to-date
// spend reaches the hard cap its owner set on /console/billing/budget. It reads
// both numbers out of Redis. The cap has been written since Phase 14; the spend
// counter was written by nobody, so the comparison was always zero against the
// cap, no request was ever refused, and a console control labelled "Requests
// blocked beyond this" did nothing at all.
//
// This is the writer. It hangs off the settlement path
// (accounting.finalizeLocked, the single chokepoint every settled charge routes
// through) so the counter moves with the ledger rather than on a schedule.
//
// UNITS, which is the part that has to be right. The ledger stores credits, one
// billionth of a USD each (payments.CreditsPerUSD, D-031 and D-046). The cap is
// stored in BDT subunits, because that is what the customer types. Those are
// different units and the step between them is an exchange rate. Writing raw
// credits into the key the gate reads would trip the cap at roughly one
// ten-millionth of the intended spend, which is issue #1648 pointed the other
// way and, unlike #1648, would reject live traffic rather than misprint a
// document.
//
// TWO KEYS, deliberately:
//
//   - budget:mtd_credits:{ws}:YYYY-MM is the accumulator, in ledger credits,
//     moved by INCRBY. Exact, because integers, and never rounded.
//   - budget:mtd_spend:{ws}:YYYY-MM is the gate's view, in BDT subunits,
//     rewritten from the running total after every settlement.
//
// Accumulating in subunits instead would round every settlement to zero and the
// counter would never leave the floor: one request costs a small fraction of a
// paisa, so round(each) is 0 while round(sum) is not. Converting the running
// total once per settlement also means no cumulative rounding drift.
//
// WHICH RATE. The platform rate, the same choice and for the same reason as the
// spend-alert cron in cron.go: a cap comparison is a threshold on a mid-month
// running total, not a document that has to reconcile against a receipt, and
// one rate keeps every workspace mutually comparable. An invoice, which is such
// a document, resolves the account's own FX snapshot instead.
// =============================================================================

// mtdCounterTTL garbage-collects a finished period. The keys are period
// suffixed, so a new month starts at zero on its own and this TTL never
// participates in resetting anything: it only stops last year's keys living in
// Redis forever. It is reissued on every increment, so it measures from the
// last settlement of that month, and it is generous enough that a settlement
// arriving late (a reaper release, a reconciliation) still lands on a live key.
const mtdCounterTTL = 45 * 24 * time.Hour

// MTDSpendRedisKey returns the key the edge-api budget gate reads for the
// workspace's month-to-date spend, in BDT subunits.
//
// Kept in sync BY HAND with limits.MTDSpendRedisKeyPattern in edge-api: neither
// module can import the other, since both live under an internal/ tree rooted
// at their own app. Both sides pin this literal in their tests
// (TestGateKeysMatchTheControlPlaneWriter there, the counter tests here), which
// is the only mechanical link between them.
func MTDSpendRedisKey(workspaceID uuid.UUID, period time.Time) string {
	return fmt.Sprintf("budget:mtd_spend:{%s}:%s", workspaceID.String(), period.UTC().Format("2006-01"))
}

// mtdCreditsRedisKey returns the accumulator key. Control-plane internal: the
// gate never reads it, because the gate has no rate with which to convert it.
func mtdCreditsRedisKey(workspaceID uuid.UUID, period time.Time) string {
	return fmt.Sprintf("budget:mtd_credits:{%s}:%s", workspaceID.String(), period.UTC().Format("2006-01"))
}

// MTDSpendCounter records settled spend against the Redis counters above.
type MTDSpendCounter struct {
	redis  *goredis.Client
	repo   WorkspaceBudgetRepository
	rate   payments.USDBDTRate
	logger *slog.Logger
}

// NewMTDSpendCounter builds the counter. The rate is resolved once by the
// caller (see main.go) rather than per settlement, so a malformed operator
// override is a boot failure instead of a per-request one.
func NewMTDSpendCounter(client *goredis.Client, repo WorkspaceBudgetRepository, rate payments.USDBDTRate, logger *slog.Logger) *MTDSpendCounter {
	if logger == nil {
		logger = slog.Default()
	}
	return &MTDSpendCounter{redis: client, repo: repo, rate: rate, logger: logger}
}

// RecordSettledSpend adds a settled charge to the workspace's month-to-date
// counters. `credits` is what the ledger actually captured, not what the caller
// estimated, and `at` is when it settled, which decides the period the charge
// lands in.
//
// FAILURE POSTURE. This returns its error and the caller logs it, but the
// caller does NOT fail the settlement over it. By the time this runs the charge
// has already committed to an append-only ledger; returning an error that
// aborted finalization would strand the hold and leave the reservation
// unsettled, turning a Redis blip into the credit leak of issue #616. So the
// cost of a failed write is a cap that goes briefly unenforced, not a broken
// money path, and the reseed below heals it from the ledger on the next
// settlement after Redis returns. The ledger is the authority here; Redis is a
// cache in front of it that happens to be readable from the edge.
func (c *MTDSpendCounter) RecordSettledSpend(ctx context.Context, workspaceID uuid.UUID, credits int64, at time.Time) error {
	if c == nil || c.redis == nil {
		return nil
	}
	if credits <= 0 {
		// A failed request, or a hold released without capture. Nothing was
		// charged, so nothing counts against the cap, and an empty key is not
		// worth two round trips.
		return nil
	}
	period := startOfMonthUTC(at.UTC())
	creditsKey := mtdCreditsRedisKey(workspaceID, period)

	// INCRBY and EXPIRE in one round trip. EXPIRE only matters on the first
	// increment of a period, but reissuing it is idempotent and guarantees a
	// TTL even if the first one was lost to a reconnect (same shape as
	// signupguard.RedisIncrementer).
	pipe := c.redis.TxPipeline()
	incr := pipe.IncrBy(ctx, creditsKey, credits)
	pipe.Expire(ctx, creditsKey, mtdCounterTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("budgets: increment mtd credits: %w", err)
	}
	total := incr.Val()

	if total == credits {
		// The key did not exist before this increment. Three ways that happens:
		// the first settlement of a new month (where the ledger agrees and this
		// costs one query), a Redis restart or eviction mid-month, and the
		// first settlement after this code was deployed into a month already
		// part spent. In the last two the accumulator would otherwise restart
		// the customer's cap at zero, silently, for the rest of the month.
		total = c.reseedFromLedger(ctx, workspaceID, period, creditsKey, total)
	}

	subunits, err := payments.CreditsToBDTSubunits(big.NewInt(total), c.rate.Rate)
	if err != nil {
		return fmt.Errorf("budgets: convert mtd credits: %w", err)
	}
	// SET rather than INCRBY: this key is a rendering of the accumulator, not
	// an accumulator itself.
	//
	// ponytail: two concurrent settlements can convert their own totals and
	// land their SETs out of order, leaving the lower of the two until the next
	// settlement overwrites it. The window is one settlement wide and always
	// errs low (the gate lets a request through rather than refusing one that
	// should pass), and the next charge corrects it. A Lua script that wrote
	// only when the new value is greater would close it, at the cost of
	// shipping and versioning a script for a one-request skew.
	if err := c.redis.Set(ctx, MTDSpendRedisKey(workspaceID, period), subunits.String(), mtdCounterTTL).Err(); err != nil {
		return fmt.Errorf("budgets: write mtd spend: %w", err)
	}
	return nil
}

// reseedFromLedger replaces a just-created accumulator with the month's real
// total from the ledger, and republishes the workspace's hard cap while it has
// the row in hand. Returns the total to convert: the ledger's when it could be
// read, the caller's own increment otherwise.
//
// Best effort by design. Every failure here leaves the accumulator holding this
// settlement's credits, which is what it would have held without this function
// at all, so a reseed that cannot run degrades to the un-healed behaviour
// rather than losing the charge.
func (c *MTDSpendCounter) reseedFromLedger(ctx context.Context, workspaceID uuid.UUID, period time.Time, creditsKey string, fallback int64) int64 {
	ledgerTotal, err := c.repo.MonthToDateSpendCredits(ctx, workspaceID, period)
	if err != nil {
		c.logger.WarnContext(ctx, "budget mtd counter: ledger reseed failed, counting from this charge only",
			"workspace_id", workspaceID, "period", period.Format("2006-01"), "error", err)
		return fallback
	}
	if !ledgerTotal.IsInt64() {
		// Unreachable short of a corrupt ledger: int64 credits is nine billion
		// USD of monthly spend. Refuse to narrow it rather than wrap.
		c.logger.ErrorContext(ctx, "budget mtd counter: ledger month-to-date does not fit int64",
			"workspace_id", workspaceID, "credits", ledgerTotal.String())
		return fallback
	}
	total := ledgerTotal.Int64()
	if total < fallback {
		// The charge that triggered this has committed, so the ledger should
		// already include it. If a read replica or an in-flight transaction
		// says otherwise, keep the larger figure: undercounting is the
		// direction that stops enforcing a cap.
		return fallback
	}
	if err := c.redis.Set(ctx, creditsKey, total, mtdCounterTTL).Err(); err != nil {
		c.logger.WarnContext(ctx, "budget mtd counter: could not store reseeded total",
			"workspace_id", workspaceID, "error", err)
		return total
	}

	// Republish the cap. It is written on every budget upsert, but a Redis
	// restart drops it and nothing else ever puts it back, so without this an
	// existing customer's cap would stop being enforced until they happened to
	// re-save it. This is the moment we know the workspace has spent something
	// and that its Redis state was just rebuilt.
	budget, err := c.repo.GetBudget(ctx, workspaceID)
	if err != nil {
		c.logger.WarnContext(ctx, "budget mtd counter: could not republish hard cap",
			"workspace_id", workspaceID, "error", err)
		return total
	}
	if budget == nil || budget.HardCap == nil {
		return total
	}
	if err := c.redis.Set(ctx, hardCapRedisKey(workspaceID), budget.HardCap.String(), hardCapRedisNoExpiry).Err(); err != nil {
		c.logger.WarnContext(ctx, "budget mtd counter: hard cap republish failed",
			"workspace_id", workspaceID, "error", err)
	}
	return total
}
