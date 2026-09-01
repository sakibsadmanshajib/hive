package budgets

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/sakibsadmanshajib/hive/packages/budgetkeys"
)

// =============================================================================
// Phase 14 — Workspace budget service.
//
// Layered on top of the legacy account-threshold Service to keep backwards
// compatibility with existing console pages while exposing the new owner-gated
// budget surface.
//
// math/big policy: every monetary value passed in or returned is *big.Int.
// Caller-side conversions (HTTP / cron / edge-api gate) are responsible for
// stable JSON encoding (int64 form — fits, see types.go documentation).
// =============================================================================

// Service provides budget threshold management and alert notification.
//
// Phase 14 extension: Service now also owns the workspace-level Budget and
// SpendAlert surface plus a Redis hard-cap broadcast for the edge-api budget
// gate's invalidation channel.
type Service struct {
	repo         ThresholdRepository
	notifier     EmailNotifier
	logger       *slog.Logger
	workspaceCtx *workspaceServiceContext // nil when only legacy threshold surface is wired
}

// workspaceServiceContext bundles the Phase 14 dependencies in a single nilable
// struct so existing callers (NewService) keep their tight constructor while
// the new wiring path uses NewServiceWithWorkspace.
type workspaceServiceContext struct {
	wrepo         WorkspaceBudgetRepository
	alertNotifier AlertNotifier
	redis         *goredis.Client // optional — nil disables hard-cap broadcast
}

// NewService creates a new Service with the legacy threshold repository and
// email notifier. Phase 14 workspace budget endpoints are NOT wired through
// this constructor; use NewServiceWithWorkspace for that.
func NewService(repo ThresholdRepository, notifier EmailNotifier) *Service {
	return &Service{
		repo:     repo,
		notifier: notifier,
		logger:   slog.Default(),
	}
}

// NewServiceWithWorkspace creates a Service with both the legacy threshold
// surface and the Phase 14 workspace budget + spend-alert surface.
//
// `redis` is optional: if non-nil, hard-cap upserts publish the cap to Redis
// where the edge-api budget gate reads it. If nil, nothing publishes it and the
// gate stays pass-through for every workspace, since it has no other source for
// a cap.
func NewServiceWithWorkspace(
	repo ThresholdRepository,
	notifier EmailNotifier,
	wrepo WorkspaceBudgetRepository,
	alertNotifier AlertNotifier,
	redis *goredis.Client,
) *Service {
	return &Service{
		repo:     repo,
		notifier: notifier,
		logger:   slog.Default(),
		workspaceCtx: &workspaceServiceContext{
			wrepo:         wrepo,
			alertNotifier: alertNotifier,
			redis:         redis,
		},
	}
}

// =============================================================================
// Legacy threshold surface (preserved verbatim)
// =============================================================================

// GetThreshold returns the budget threshold for the given account, or nil if none is set.
func (s *Service) GetThreshold(ctx context.Context, accountID uuid.UUID) (*BudgetThreshold, error) {
	t, err := s.repo.GetThreshold(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("budgets: get threshold: %w", err)
	}
	return t, nil
}

// UpsertThreshold creates or updates the budget threshold for the given account.
func (s *Service) UpsertThreshold(ctx context.Context, accountID uuid.UUID, input UpsertThresholdInput) (*BudgetThreshold, error) {
	if input.ThresholdCredits <= 0 {
		return nil, &ValidationError{Field: "threshold_credits", Message: "threshold_credits must be greater than zero"}
	}
	t, err := s.repo.UpsertThreshold(ctx, accountID, input.ThresholdCredits)
	if err != nil {
		return nil, fmt.Errorf("budgets: upsert threshold: %w", err)
	}
	return t, nil
}

// DismissAlert dismisses the budget alert for the given account.
func (s *Service) DismissAlert(ctx context.Context, accountID uuid.UUID) error {
	if err := s.repo.DismissAlert(ctx, accountID); err != nil {
		return fmt.Errorf("budgets: dismiss alert: %w", err)
	}
	return nil
}

// CheckThresholds evaluates the current balance against the account's threshold and
// sends a budget alert email when the balance drops below the threshold and the
// alert has not been dismissed or recently sent (within 24h).
// Notification failure is non-fatal and is logged without returning an error.
func (s *Service) CheckThresholds(ctx context.Context, accountID uuid.UUID, currentBalance int64) error {
	threshold, err := s.repo.GetThreshold(ctx, accountID)
	if err != nil {
		return fmt.Errorf("budgets: check thresholds: %w", err)
	}
	if threshold == nil {
		return nil
	}

	if currentBalance > threshold.ThresholdCredits || threshold.AlertDismissed {
		return nil
	}

	// Check 24-hour notification cooldown.
	if threshold.LastNotifiedAt != nil && time.Since(*threshold.LastNotifiedAt) < 24*time.Hour {
		return nil
	}

	s.logger.InfoContext(ctx, "budget threshold breached",
		"account_id", accountID,
		"threshold_credits", threshold.ThresholdCredits,
		"current_balance", currentBalance,
	)

	if err := s.notifier.SendBudgetAlert(ctx, accountID, *threshold, currentBalance); err != nil {
		s.logger.ErrorContext(ctx, "budget alert email failed",
			"account_id", accountID,
			"error", err,
		)
		// Non-fatal: do not block caller on notification failure.
		return nil
	}

	if err := s.repo.MarkNotified(ctx, accountID); err != nil {
		s.logger.ErrorContext(ctx, "mark notified failed after budget alert",
			"account_id", accountID,
			"error", err,
		)
	}

	return nil
}

// ValidationError is a field-level validation error returned by service methods.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// =============================================================================
// Phase 14 — Workspace Budget API
// =============================================================================

// hardCapRedisKey returns the Redis key the edge-api budget gate reads.
//
// Cache invalidation strategy:
//   - Control-plane WRITES the key on every SetBudget call (push-on-write) and
//     DELETES it on DeleteBudget, so Redis follows the row.
//   - The key encodes only hard_cap (the only value the hot-path needs); soft
//     cap stays control-plane-internal and is consulted by the alert cron.
//
// Worst-case staleness is one publish: a workspace whose owner just lowered the
// cap keeps the old one until the SET lands. That is bounded by a round trip,
// not by a clock.
func hardCapRedisKey(workspaceID uuid.UUID) string {
	return budgetkeys.HardCap(workspaceID.String())
}

// hardCapRedisNoExpiry is the TTL the published cap carries: none.
//
// It used to be thirty seconds, under a comment claiming the edge-api gate
// would read through on a miss so a lost publish healed quickly. The gate does
// no such thing and never did: a missing key reads as "no budget configured"
// and the gate becomes pass-through. So a cap stopped being enforced thirty
// seconds after the customer typed it, which is the second half of why the hard
// cap never blocked anything (issue #1651). Redis follows the row on upsert and
// delete.
//
// WHAT PUTS IT BACK, and this is the common path rather than the rare one. The
// redis service declares no volume in deploy/docker/docker-compose.yml, so every
// stack recreate starts with an empty keyspace, and a push to main auto-deploys.
// After every deploy, every workspace's cap key is gone. Two things republish
// it: the settlement counter, when it rebuilds a workspace's period keys, and
// the spend-alert pass, which walks every workspace with a budget once a minute
// and restates the cap whether or not that workspace has settled anything. The
// second is what covers a workspace whose next requests all fail, which settles
// nothing and would otherwise never trigger the first. So the exposure after a
// deploy, and after a cap saved while Redis was unreachable, is bounded by the
// pass interval rather than by a customer happening to re-save their budget.
const hardCapRedisNoExpiry time.Duration = 0

// GetBudget returns the workspace budget or (nil, nil) when none is set.
func (s *Service) GetBudget(ctx context.Context, workspaceID uuid.UUID) (*Budget, error) {
	if s.workspaceCtx == nil {
		return nil, fmt.Errorf("budgets: workspace surface not wired")
	}
	b, err := s.workspaceCtx.wrepo.GetBudget(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("budgets: get budget: %w", err)
	}
	return b, nil
}

// SetBudget upserts the workspace's soft + hard caps (math/big.Int).
// Validates hard >= soft via *big.Int.Cmp. On success, publishes the new
// hard_cap value to Redis (key: budget:hard_cap:{ws}), which is the only place
// the edge-api gate can read it.
func (s *Service) SetBudget(ctx context.Context, in SetBudgetInput) (*Budget, error) {
	if s.workspaceCtx == nil {
		return nil, fmt.Errorf("budgets: workspace surface not wired")
	}
	if in.SoftCap == nil || in.HardCap == nil {
		return nil, ErrInvalidCaps
	}
	if in.SoftCap.Sign() < 0 || in.HardCap.Sign() < 0 {
		return nil, ErrInvalidCaps
	}
	// Hard cap must be >= soft cap (DB CHECK also enforces).
	if in.HardCap.Cmp(in.SoftCap) < 0 {
		return nil, ErrInvalidCaps
	}
	if in.PeriodStart.IsZero() {
		in.PeriodStart = startOfMonthUTC(time.Now().UTC())
	}

	b, err := s.workspaceCtx.wrepo.UpsertBudget(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("budgets: upsert budget: %w", err)
	}

	// Publish the new hard_cap for the edge-api gate. A Redis error is logged
	// and non-fatal, but it is not harmless: until the next publish, or until
	// the settlement counter republishes the cap while rebuilding this
	// workspace's period keys, the gate sees no cap and stays pass-through.
	if s.workspaceCtx.redis != nil {
		key := hardCapRedisKey(in.WorkspaceID)
		if rerr := s.workspaceCtx.redis.Set(ctx, key, b.HardCap.String(), hardCapRedisNoExpiry).Err(); rerr != nil {
			s.logger.WarnContext(ctx, "budget hard_cap redis broadcast failed",
				"workspace_id", in.WorkspaceID, "error", rerr)
		}
	}

	return b, nil
}

// DeleteBudget removes the workspace's budget (hard cap removed; gate becomes
// pass-through). The Redis key is also deleted so edge-api stops gating.
func (s *Service) DeleteBudget(ctx context.Context, workspaceID uuid.UUID) error {
	if s.workspaceCtx == nil {
		return fmt.Errorf("budgets: workspace surface not wired")
	}
	if err := s.workspaceCtx.wrepo.DeleteBudget(ctx, workspaceID); err != nil {
		return fmt.Errorf("budgets: delete budget: %w", err)
	}
	if s.workspaceCtx.redis != nil {
		_ = s.workspaceCtx.redis.Del(ctx, hardCapRedisKey(workspaceID)).Err()
	}
	return nil
}

// ListAlerts returns the alerts configured on a workspace.
func (s *Service) ListAlerts(ctx context.Context, workspaceID uuid.UUID) ([]SpendAlert, error) {
	if s.workspaceCtx == nil {
		return nil, fmt.Errorf("budgets: workspace surface not wired")
	}
	return s.workspaceCtx.wrepo.ListAlerts(ctx, workspaceID)
}

// CreateAlert validates threshold_pct and creates a new alert.
func (s *Service) CreateAlert(ctx context.Context, in CreateAlertInput) (*SpendAlert, error) {
	if s.workspaceCtx == nil {
		return nil, fmt.Errorf("budgets: workspace surface not wired")
	}
	if !validThreshold(in.ThresholdPct) {
		return nil, ErrInvalidThreshold
	}
	a, err := s.workspaceCtx.wrepo.CreateAlert(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("budgets: create alert: %w", err)
	}
	return a, nil
}

// UpdateAlert updates email / webhook fields on an existing alert.
func (s *Service) UpdateAlert(ctx context.Context, in UpdateAlertInput) (*SpendAlert, error) {
	if s.workspaceCtx == nil {
		return nil, fmt.Errorf("budgets: workspace surface not wired")
	}
	a, err := s.workspaceCtx.wrepo.UpdateAlert(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("budgets: update alert: %w", err)
	}
	return a, nil
}

// DeleteAlert removes an alert. workspaceID + alertID together constrain the
// data-layer mutation so tenant isolation holds even if a caller guesses an
// alert UUID belonging to another workspace.
func (s *Service) DeleteAlert(ctx context.Context, workspaceID, alertID uuid.UUID) error {
	if s.workspaceCtx == nil {
		return fmt.Errorf("budgets: workspace surface not wired")
	}
	if err := s.workspaceCtx.wrepo.DeleteAlert(ctx, workspaceID, alertID); err != nil {
		return fmt.Errorf("budgets: delete alert: %w", err)
	}
	return nil
}

// HardCapForWorkspace returns the workspace hard_cap as *big.Int — used by the
// edge-api gate when it falls through to the control-plane internal endpoint.
// Returns (nil, nil) when no budget is set (gate is pass-through).
func (s *Service) HardCapForWorkspace(ctx context.Context, workspaceID uuid.UUID) (*big.Int, error) {
	b, err := s.GetBudget(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return nil, nil
	}
	return new(big.Int).Set(b.HardCap), nil
}

// MonthToDateSpendCredits returns the workspace's month-to-date spend in
// credits, the unit the ledger stores. Callers that render or compare taka
// convert through payments.CreditsToBDTSubunits first (issue #1648).
func (s *Service) MonthToDateSpendCredits(ctx context.Context, workspaceID uuid.UUID, periodStart time.Time) (*big.Int, error) {
	if s.workspaceCtx == nil {
		return nil, fmt.Errorf("budgets: workspace surface not wired")
	}
	return s.workspaceCtx.wrepo.MonthToDateSpendCredits(ctx, workspaceID, periodStart)
}

// validThreshold checks whether a threshold percentage is in the allow-list.
func validThreshold(pct int) bool {
	return pct == 50 || pct == 80 || pct == 100
}

// startOfMonthUTC returns the first instant of the month containing t.
func startOfMonthUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
