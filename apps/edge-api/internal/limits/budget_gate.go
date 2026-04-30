// Package limits implements edge-api hot-path quota gates that sit between the
// authz pipeline and the inference dispatchers.
//
// Phase 14 ships the BudgetGate: a workspace hard-cap enforcer that reads its
// authoritative hard_cap from Redis (key written by the control-plane budget
// service on every Set/DeleteBudget) and the workspace's month-to-date BDT
// subunit spend from a Redis incr-on-spend counter. When MTD ≥ hard_cap, the
// gate replies with **402 Payment Required** and a `Retry-After` header
// pointing at the start of the next monthly billing period.
//
// Design contracts (locked by Phase 14 AUDIT D.5):
//   - **BDT subunits only** in 402 body — no `amount_usd`, no FX language.
//   - **math/big** for cap comparisons; cache values are decimal strings to
//     preserve exact arithmetic across the wire.
//   - **Soft-cap is non-blocking** — only the hard cap rejects. Soft-cap
//     crossings are surfaced via the `budget_soft_cap_crossed_total` Prometheus
//     counter and the spend-alert cron's user-visible notifications.
//   - **Cache invalidation strategy**: control-plane PUSHES the new hard_cap
//     to Redis on every upsert (`budget:hard_cap:{workspaceID}`). The gate
//     READS with a brief TTL (~30s) so a missed publish heals on the next
//     read; meanwhile the per-workspace MTD counter is INCRed inline as
//     usage settles, so eventual consistency stays bounded.
//
// The middleware extracts workspace identity by calling the supplied
// `WorkspaceFromRequest` resolver — keeps this package decoupled from the
// edge-api authz module while still letting main.go wire authz.Authorize.
package limits

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// =============================================================================
// CacheReader — redis seam (interface-where-used)
// =============================================================================

// CacheReader is the minimal Redis-shaped surface the gate uses. The
// production wiring satisfies it via `*redis.Client.Get`; tests pass a
// hand-rolled fake to avoid taking a hard dep on miniredis or a docker fixture.
type CacheReader interface {
	Get(ctx context.Context, key string) (value string, ok bool, err error)
}

// redisCacheReader adapts *redis.Client to CacheReader. Returns ok=false on
// redis.Nil so callers can short-circuit cleanly.
type redisCacheReader struct {
	client *redis.Client
}

// NewRedisCacheReader wraps a redis client for use as a CacheReader.
func NewRedisCacheReader(client *redis.Client) CacheReader {
	return &redisCacheReader{client: client}
}

func (r *redisCacheReader) Get(ctx context.Context, key string) (string, bool, error) {
	val, err := r.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

// =============================================================================
// Public surface
// =============================================================================

// BDTSubunitsCurrency is the only currency the gate emits in its 402 body.
const BDTSubunitsCurrency = "BDT"

// HardCapRedisKeyPattern returns the Redis key the control-plane writes and
// the gate reads. Kept in sync with the control-plane's
// `budgets.hardCapRedisKey` — see budgets/service.go.
func HardCapRedisKeyPattern(workspaceID string) string {
	return fmt.Sprintf("budget:hard_cap:{%s}", workspaceID)
}

// MTDSpendRedisKeyPattern returns the Redis key holding the workspace's
// month-to-date spend (BDT subunits, integer string). Period-suffixed so a
// new month starts at zero without explicit reset.
func MTDSpendRedisKeyPattern(workspaceID string, period time.Time) string {
	return fmt.Sprintf("budget:mtd_spend:{%s}:%s", workspaceID, period.Format("2006-01"))
}

// SoftCapCrossedMetric reports the Prometheus counter name the gate increments
// when MTD crosses (but does not exceed) the soft cap. The actual *prometheus
// .CounterVec* lives in proxy.EdgeMetrics; the gate accepts any object that
// matches `WithLabelValues(...).Inc()` via the SoftCapMetric interface below.
const SoftCapCrossedMetric = "budget_soft_cap_crossed_total"

// SoftCapMetric is a tiny seam allowing the gate to bump a Prometheus counter
// without forcing this package to import prometheus_client_golang directly.
type SoftCapMetric interface {
	Inc(workspaceID string)
}

// noopSoftCapMetric is the default; main.go can replace it with a real
// prometheus counter wrapper.
type noopSoftCapMetric struct{}

// NewNoopSoftCapMetric returns a metric that drops Inc calls. Useful for tests.
func NewNoopSoftCapMetric() SoftCapMetric { return noopSoftCapMetric{} }

func (noopSoftCapMetric) Inc(string) {}

// WorkspaceFromRequest resolves the active workspace ID for an inbound
// request. Production wiring forwards to authz.Authorize; tests pass a stub.
//
// Returning ("", false) means the gate is *bypassed* — typically because the
// request is unauthenticated and the auth layer has already rejected (or will
// reject) it. The gate must NEVER overwrite an unauthenticated 401 with 402.
type WorkspaceFromRequest func(r *http.Request) (workspaceID string, ok bool)

// SoftCapResolver returns the workspace soft cap (BDT subunits). Optional —
// nil disables soft-cap-cross metric emission. Returning (nil, nil) means the
// workspace has no soft cap configured.
type SoftCapResolver func(ctx context.Context, workspaceID string) (*big.Int, error)

// Config controls BudgetGate behaviour.
type Config struct {
	// Cache reads hard cap + MTD spend. Required.
	Cache CacheReader
	// Resolver returns the workspace ID for an inbound request. Required.
	WorkspaceFromRequest WorkspaceFromRequest
	// Optional soft-cap resolver. nil disables soft-cap metric.
	SoftCapResolver SoftCapResolver
	// SoftCapMetric is the counter incremented on soft-cap crossings.
	SoftCapMetric SoftCapMetric
	// HardCapTTL bounds drift from a missed control-plane publish. Default 30s.
	HardCapTTL time.Duration
	// Now is a clock seam for tests.
	Now func() time.Time
}

// BudgetGate is the middleware factory. The returned `Wrap(next)` produces an
// http.Handler that fronts `next` with the 402 hard-cap check.
type BudgetGate struct {
	cache   CacheReader
	resolve WorkspaceFromRequest
	soft    SoftCapResolver
	metric  SoftCapMetric
	ttl     time.Duration
	now     func() time.Time
}

// New constructs a BudgetGate with the supplied config. Returns an error when
// required dependencies are missing.
func New(cfg Config) (*BudgetGate, error) {
	if cfg.Cache == nil {
		return nil, errors.New("limits: nil cache reader")
	}
	if cfg.WorkspaceFromRequest == nil {
		return nil, errors.New("limits: nil WorkspaceFromRequest")
	}
	ttl := cfg.HardCapTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	now := cfg.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	metric := cfg.SoftCapMetric
	if metric == nil {
		metric = NewNoopSoftCapMetric()
	}
	return &BudgetGate{
		cache:   cfg.Cache,
		resolve: cfg.WorkspaceFromRequest,
		soft:    cfg.SoftCapResolver,
		metric:  metric,
		ttl:     ttl,
		now:     now,
	}, nil
}

// Wrap returns an http.Handler that gates `next` on the workspace hard cap.
func (g *BudgetGate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := g.resolve(r)
		if !ok || workspaceID == "" {
			next.ServeHTTP(w, r)
			return
		}

		decision, err := g.evaluate(r.Context(), workspaceID)
		if err != nil {
			// Fail-open on Redis errors — better to bill a few extra requests
			// than crash the hot path. The 24-hour budget cron settles below
			// the cap on the next pass.
			next.ServeHTTP(w, r)
			return
		}
		if decision.Soft && g.metric != nil {
			g.metric.Inc(workspaceID)
		}
		if decision.HardBlocked {
			writeHardCapExceeded(w, decision)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// =============================================================================
// Evaluation logic
// =============================================================================

type decision struct {
	HardBlocked bool
	Soft        bool      // mtd >= soft AND not hard-blocked
	HardCap     *big.Int  // populated when known
	MTD         *big.Int  // current MTD spend
	NextPeriod  time.Time // start of next billing period (UTC)
}

func (g *BudgetGate) evaluate(ctx context.Context, workspaceID string) (decision, error) {
	d := decision{NextPeriod: startOfNextMonthUTC(g.now())}

	hardCap, err := g.fetchHardCap(ctx, workspaceID)
	if err != nil {
		return d, err
	}
	if hardCap == nil {
		// No budget configured — gate is pass-through.
		return d, nil
	}
	d.HardCap = hardCap

	mtd, err := g.fetchMTDSpend(ctx, workspaceID)
	if err != nil {
		return d, err
	}
	d.MTD = mtd

	if mtd.Cmp(hardCap) >= 0 {
		d.HardBlocked = true
		return d, nil
	}

	if g.soft != nil {
		softCap, serr := g.soft(ctx, workspaceID)
		if serr == nil && softCap != nil && softCap.Sign() > 0 && mtd.Cmp(softCap) >= 0 {
			d.Soft = true
		}
	}
	return d, nil
}

func (g *BudgetGate) fetchHardCap(ctx context.Context, workspaceID string) (*big.Int, error) {
	key := HardCapRedisKeyPattern(workspaceID)
	val, ok, err := g.cache.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("limits: read hard cap: %w", err)
	}
	if !ok {
		return nil, nil
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return nil, nil
	}
	hardCap, parsed := new(big.Int).SetString(val, 10)
	if !parsed {
		return nil, fmt.Errorf("limits: malformed hard cap %q", val)
	}
	return hardCap, nil
}

// fetchMTDSpend reads the workspace's BDT-subunit spend counter. The counter
// is INCRed inline by the control-plane settlement path; missing keys mean
// "no spend yet this period".
func (g *BudgetGate) fetchMTDSpend(ctx context.Context, workspaceID string) (*big.Int, error) {
	period := startOfMonthUTC(g.now())
	key := MTDSpendRedisKeyPattern(workspaceID, period)
	val, ok, err := g.cache.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("limits: read mtd spend: %w", err)
	}
	if !ok {
		return big.NewInt(0), nil
	}
	val = strings.TrimSpace(val)
	if val == "" {
		return big.NewInt(0), nil
	}
	mtd, parsed := new(big.Int).SetString(val, 10)
	if !parsed {
		return nil, fmt.Errorf("limits: malformed mtd spend %q", val)
	}
	if mtd.Sign() < 0 {
		mtd = big.NewInt(0)
	}
	return mtd, nil
}

// =============================================================================
// 402 response — BDT-only body, Retry-After header
// =============================================================================

// hardCapExceededBody is the 402 JSON envelope. Phase 17 mandate: BDT subunits
// only. No `amount_usd`. No FX strings. No provider names.
type hardCapExceededBody struct {
	Error hardCapExceededError `json:"error"`
}

type hardCapExceededError struct {
	Type                string `json:"type"`
	Code                string `json:"code"`
	Message             string `json:"message"`
	Currency            string `json:"currency"`
	HardCapBDTSubunits  string `json:"hard_cap_bdt_subunits"`
	MTDBDTSubunits      string `json:"mtd_bdt_subunits"`
	NextPeriodStartUTC  string `json:"next_period_start_utc"`
	RetryAfterSeconds   int64  `json:"retry_after_seconds"`
}

func writeHardCapExceeded(w http.ResponseWriter, d decision) {
	now := time.Now().UTC()
	retrySec := int64(d.NextPeriod.Sub(now).Seconds())
	if retrySec < 1 {
		retrySec = 1
	}

	body := hardCapExceededBody{
		Error: hardCapExceededError{
			Type:               "insufficient_quota",
			Code:               "budget_hard_cap_exceeded",
			Message:            "Workspace monthly hard cap reached. New requests resume at the start of the next billing period.",
			Currency:           BDTSubunitsCurrency,
			HardCapBDTSubunits: bigIntString(d.HardCap),
			MTDBDTSubunits:     bigIntString(d.MTD),
			NextPeriodStartUTC: d.NextPeriod.Format(time.RFC3339),
			RetryAfterSeconds:  retrySec,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.FormatInt(retrySec, 10))
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(body)
}

func bigIntString(v *big.Int) string {
	if v == nil {
		return "0"
	}
	return v.String()
}

// =============================================================================
// Time helpers — keep UTC discipline
// =============================================================================

func startOfMonthUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func startOfNextMonthUTC(t time.Time) time.Time {
	first := startOfMonthUTC(t)
	return first.AddDate(0, 1, 0)
}
