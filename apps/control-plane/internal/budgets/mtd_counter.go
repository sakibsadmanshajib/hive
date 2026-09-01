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
	"github.com/sakibsadmanshajib/hive/packages/budgetkeys"
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
// Accumulating in subunits instead would round on every settlement instead of
// once. That is wrong in two magnitudes rather than one, so the reason is worth
// stating precisely. For a charge worth less than half a paisa, round(each) is 0
// while round(sum) is not, and the counter never leaves the floor no matter how
// much is spent; a small embedding call at the platform rate is in that range,
// since one paisa is about 81,216 credits at 123.13 BDT per USD. For charges
// above that the counter does move, and instead carries up to half a paisa of
// error per settlement with a consistent sign. Converting the running total once
// has neither failure.
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

// mtdCounterBudget caps how long one settlement may spend maintaining these
// counters, including the two database reads on the rebuild path.
//
// It exists because of where this runs, not because Redis is expected to be
// slow: inside the per-account advisory lock held by accounting.finalizeLocked,
// on a connection from the same pool the rest of the product shares. Overrunning
// it is a fail-open, which is the posture this whole path already takes, so the
// only cost of the bound being hit is a cap left briefly unenforced and an ERROR
// line saying so. Generous by two orders of magnitude for the happy path, where
// every operation here is a single indexed round trip.
const mtdCounterBudget = 500 * time.Millisecond

// MTDSpendRedisKey returns the key the edge-api budget gate reads for the
// workspace's month-to-date spend, in BDT subunits.
//
// The shape lives in packages/budgetkeys, which the gate imports too, so the
// writer and the reader share one definition instead of two literals that agree
// by convention. Neither app can import the other's internal tree, which is the
// structural reason issue #1651 was possible.
func MTDSpendRedisKey(workspaceID uuid.UUID, period time.Time) string {
	return budgetkeys.MTDSpend(workspaceID.String(), period)
}

// mtdCreditsRedisKey returns the accumulator key. Control-plane internal: the
// gate never reads it, because the gate has no rate with which to convert it.
func mtdCreditsRedisKey(workspaceID uuid.UUID, period time.Time) string {
	return budgetkeys.MTDCredits(workspaceID.String(), period)
}

// FailureCounter is the metric seam for writes that did not land. Satisfied by
// prometheus.Counter without this package importing prometheus.
//
// It matters more than a log line here: every failure it counts is a charge
// missing from a customer's hard cap, and the edge-api counter cannot see any of
// them, because it only observes the reader failing open.
type FailureCounter interface {
	Inc()
}

// MTDSpendCounter records settled spend against the Redis counters above.
type MTDSpendCounter struct {
	redis    *goredis.Client
	repo     WorkspaceBudgetRepository
	rate     payments.USDBDTRate
	logger   *slog.Logger
	failures FailureCounter
}

// NewMTDSpendCounter builds the counter. The rate is resolved once by the
// caller (see main.go) rather than per settlement, so a malformed operator
// override is caught at boot instead of on every request.
func NewMTDSpendCounter(client *goredis.Client, repo WorkspaceBudgetRepository, rate payments.USDBDTRate, logger *slog.Logger) *MTDSpendCounter {
	if logger == nil {
		logger = slog.Default()
	}
	return &MTDSpendCounter{redis: client, repo: repo, rate: rate, logger: logger}
}

// WithFailureCounter installs the metric bumped on every write that did not
// land. Returns the counter for chaining.
func (c *MTDSpendCounter) WithFailureCounter(failures FailureCounter) *MTDSpendCounter {
	if c != nil && failures != nil {
		c.failures = failures
	}
	return c
}

// fail records a write failure on the metric and returns the error unchanged,
// so no return path can count one and skip the other.
func (c *MTDSpendCounter) fail(err error) error {
	if c.failures != nil {
		c.failures.Inc()
	}
	return err
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
// money path. The ledger is the authority here; Redis is a cache in front of it
// that happens to be readable from the edge, and the spend-alert pass restates
// both keys from that ledger every minute, so a failure here is corrected on a
// schedule rather than only when a key happens to be missing.
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

	// Bounded, because the fail-open posture above only covers a Redis that
	// ERRORS. A Redis that accepts connections and stops answering does not
	// error for tens of seconds: platform/redis.NewClient sets no timeouts, so
	// this inherits the go-redis defaults of a 5 second dial and a 3 second read
	// with 3 retries. Every one of those seconds would be spent holding the
	// per-account Postgres advisory lock this runs inside, plus its pool
	// connection, on a deployment where one pool has already turned into a chat
	// outage. The bound turns a stall into the error the caller already logs and
	// swallows, so nothing downstream behaves differently.
	ctx, cancel := context.WithTimeout(ctx, mtdCounterBudget)
	defer cancel()

	// The period is a UTC calendar month, matching the invoice period, the alert
	// pass and the gate, so a Dhaka customer's cap resets at 06:00 local on the
	// first rather than at midnight. Product fact, not a divergence.
	//
	// Note also that this anchors on the application clock while the reseed
	// filters on Postgres created_at. For a settlement within a second of a
	// month boundary the two can disagree about which month owns the charge, so
	// it could be counted in one month's key and summed into the other's window
	// by a later reseed. One settlement at one instant per month, bounded by
	// that charge, and worth revisiting only if this stops being the sole route
	// into the counter.
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
		return c.fail(fmt.Errorf("budgets: increment mtd credits: %w", err))
	}
	total := incr.Val()

	if total == credits {
		// The key did not exist before this increment. Three ways that happens:
		// the first settlement of a new month (where the ledger agrees and this
		// costs one query), a Redis restart or eviction mid-month, and the
		// first settlement after this code was deployed into a month already
		// part spent. In the last two the accumulator would otherwise restart
		// the customer's cap at zero, silently, for the rest of the month.
		total = c.rebuildPeriodKeys(ctx, workspaceID, period, creditsKey, total)
	}

	subunits, err := payments.CreditsToBDTSubunits(big.NewInt(total), c.rate.Rate)
	if err != nil {
		return c.fail(fmt.Errorf("budgets: convert mtd credits: %w", err))
	}
	// SET rather than INCRBY: this key is a rendering of the accumulator, not
	// an accumulator itself.
	//
	// An unconditional SET would be a lost update if two settlements for one
	// workspace could interleave, since the later increment could be rendered
	// first and then overwritten by the earlier one. They cannot: the only
	// caller is accounting.finalizeLocked, which runs inside a per-account lock
	// that is a Postgres advisory lock in production, so settlements for one
	// account are serialized across every control-plane instance for the whole
	// sequence including this write (issue #106). If a settlement path is ever
	// added outside that lock, this needs a compare-and-set.
	if err := c.redis.Set(ctx, MTDSpendRedisKey(workspaceID, period), subunits.String(), mtdCounterTTL).Err(); err != nil {
		return c.fail(fmt.Errorf("budgets: write mtd spend: %w", err))
	}
	return nil
}

// SyncWorkspace restates a workspace's two period keys and its cap from figures
// the caller has already read out of the database. It is what the spend-alert
// pass calls, once a minute, for every workspace that has a budget.
//
// The settlement path alone is not enough to keep Redis true, and the two gaps
// it leaves are both ordinary rather than exotic:
//
//   - A write that fails while the accumulator key still exists loses that
//     charge for the rest of the month. The rebuild does not fire, because the
//     rebuild fires on a MISSING key, and a transient failure does not remove
//     one. The same hole swallows a charge when the process dies or the request
//     context is cancelled between the row transition and the counter write.
//   - Redis in this deployment has no volume, so every deploy starts with an
//     empty keyspace and every workspace's cap is gone. The settlement path
//     republishes it only when that workspace next settles a positive charge,
//     which a workspace whose next requests all fail never does.
//
// Both are closed by restating the values on a schedule, and the schedule
// already exists: the alert pass walks ListWorkspacesWithBudget every minute
// and already reads each workspace's month-to-date credits for its own
// comparison. This is three SETs inside a loop that is already running.
//
// Convergence, not authority: a settlement landing between the caller's read
// and this write can be overwritten, leaving the counter one charge low until
// the next pass reads a ledger that includes it. Bounded by the pass interval,
// self-correcting, and low rather than high, which is the fail-open direction
// this path takes everywhere else.
func (c *MTDSpendCounter) SyncWorkspace(ctx context.Context, workspaceID uuid.UUID, ledgerCredits, hardCap *big.Int, at time.Time) error {
	if c == nil || c.redis == nil {
		return nil
	}
	if ledgerCredits == nil || !ledgerCredits.IsInt64() || ledgerCredits.Sign() < 0 {
		return c.fail(fmt.Errorf("budgets: sync workspace: unusable ledger total %v", ledgerCredits))
	}
	ctx, cancel := context.WithTimeout(ctx, mtdCounterBudget)
	defer cancel()

	period := startOfMonthUTC(at.UTC())
	subunits, err := payments.CreditsToBDTSubunits(ledgerCredits, c.rate.Rate)
	if err != nil {
		return c.fail(fmt.Errorf("budgets: sync workspace: convert: %w", err))
	}

	// The accumulator first, then its rendering. Restating the rendering alone
	// would be undone by the next settlement, which increments the accumulator
	// and re-renders from it.
	pipe := c.redis.TxPipeline()
	pipe.Set(ctx, mtdCreditsRedisKey(workspaceID, period), ledgerCredits.Int64(), mtdCounterTTL)
	pipe.Set(ctx, MTDSpendRedisKey(workspaceID, period), subunits.String(), mtdCounterTTL)
	if hardCap != nil {
		pipe.Set(ctx, hardCapRedisKey(workspaceID), hardCap.String(), hardCapRedisNoExpiry)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return c.fail(fmt.Errorf("budgets: sync workspace: %w", err))
	}
	return nil
}

// rebuildPeriodKeys runs when the accumulator key was just created: a new
// month, an eviction, a Redis restart, or the first settlement after this code
// was deployed into a month already part spent. It restates both of the
// workspace's Redis values from the database, which is the authority, and
// returns the credit total to convert.
//
// Best effort by design. Every failure here leaves the accumulator holding this
// settlement's credits, which is what it would have held without this function
// at all, so a rebuild that cannot run degrades to the un-healed behaviour
// rather than losing the charge.
func (c *MTDSpendCounter) rebuildPeriodKeys(ctx context.Context, workspaceID uuid.UUID, period time.Time, creditsKey string, fallback int64) int64 {
	total := c.reseedTotal(ctx, workspaceID, period, creditsKey, fallback)
	c.republishHardCap(ctx, workspaceID)
	return total
}

// reseedTotal replaces the just-created accumulator with the month's real total
// from the ledger, so an eviction does not restart a customer's cap at zero.
func (c *MTDSpendCounter) reseedTotal(ctx context.Context, workspaceID uuid.UUID, period time.Time, creditsKey string, fallback int64) int64 {
	ledgerTotal, err := c.repo.MonthToDateSpendCredits(ctx, workspaceID, period)
	if err != nil {
		c.logger.WarnContext(ctx, "budget mtd counter: ledger reseed failed, counting from this charge only",
			"workspace_id", workspaceID, "period", period.Format("2006-01"), "error", err)
		return fallback
	}
	if !ledgerTotal.IsInt64() {
		// Unreachable short of a corrupt ledger, since the column this sums is
		// a bigint that the repository already scans into an int64 before it
		// reaches here, and int64 credits is nine billion USD of monthly spend.
		// Refuse to narrow it rather than wrap.
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
	}
	return total
}

// republishHardCap restates the cap from the budget row, or clears it when
// there is no row.
//
// Publishing again matters because the cap is otherwise written only on upsert,
// and a Redis restart drops it with nothing to put it back, so an existing
// customer's cap would stop being enforced until they happened to re-save it.
//
// Clearing matters for the opposite reason: the publish on upsert and the
// removal on delete are unordered against each other, so a delayed publish can
// land after a removal, and the key now carries no expiry, so nothing else
// would ever clear it and the customer would be refused under a cap they had
// taken off. This makes the row authoritative in both directions, once per
// period rather than immediately. The residual window between the two is real,
// and a revision stamped on each publish would be the complete answer if it
// ever bites; that is a schema column and a publish protocol for a race that
// needs two budget mutations on one workspace inside a round trip.
//
// The clear is only safe while GetBudget reads the primary. A (nil, nil) answer
// is taken as authority to remove a live cap, and once the accumulator key
// exists no further rebuild runs for that workspace this period, so a stale read
// from a replica would take a spend control off with nothing to put it back
// until the alert pass or a re-save. Note the asymmetry with reseedTotal above,
// which keeps the larger figure when the ledger looks behind precisely because
// under-counting stops enforcement: this function takes the opposite risk on a
// read of the same freshness, and that is only acceptable because every read in
// this process goes through the one primary pool. A read replica added later
// must not quietly include this query.
func (c *MTDSpendCounter) republishHardCap(ctx context.Context, workspaceID uuid.UUID) {
	budget, err := c.repo.GetBudget(ctx, workspaceID)
	if err != nil {
		c.logger.WarnContext(ctx, "budget mtd counter: could not read the budget row to republish its cap",
			"workspace_id", workspaceID, "error", err)
		return
	}
	if budget == nil || budget.HardCap == nil {
		if err := c.redis.Del(ctx, hardCapRedisKey(workspaceID)).Err(); err != nil {
			c.logger.WarnContext(ctx, "budget mtd counter: could not clear a cap with no budget row",
				"workspace_id", workspaceID, "error", err)
		}
		return
	}
	if err := c.redis.Set(ctx, hardCapRedisKey(workspaceID), budget.HardCap.String(), hardCapRedisNoExpiry).Err(); err != nil {
		c.logger.WarnContext(ctx, "budget mtd counter: hard cap republish failed",
			"workspace_id", workspaceID, "error", err)
	}
}
