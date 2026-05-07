package budgets

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
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
// `redis` is optional: if non-nil, hard-cap upserts publish to Redis so the
// edge-api budget gate's cache can refresh within its TTL window. If nil,
// edge-api falls back to its TTL-based read-through and accepts a brief
// staleness window.
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
// Cache invalidation strategy (per AUDIT Section D.5 risk callout):
//   - Control-plane WRITES the key on every SetBudget call (push-on-write).
//   - Edge-api READS with a TTL (ttl ~30s) so a missed publish heals quickly.
//   - The key encodes only hard_cap (the only value the hot-path needs); soft
//     cap stays control-plane-internal and is consulted by the alert cron.
//
// This is "fast path eventually consistent": worst-case staleness window is
// ttl + propagation; under that window a workspace whose owner just lowered
// the cap may briefly remain enabled. Acceptable for v1.1 (no SLA on cap
// changes propagation); Phase 18 may add Redis pub/sub for sub-second.
func hardCapRedisKey(workspaceID uuid.UUID) string {
	return fmt.Sprintf("budget:hard_cap:{%s}", workspaceID.String())
}

// hardCapRedisTTL is the read-through TTL the edge-api gate observes. The
// control-plane SETs the key on every upsert with this TTL so a missed
// invalidate heals on the next read.
const hardCapRedisTTL = 30 * time.Second

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
// Validates hard >= soft via *big.Int.Cmp. On success, broadcasts the new
// hard_cap value to Redis (key: budget:hard_cap:{ws}) so the edge-api gate
// invalidates its cache within ttl.
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

	// Broadcast new hard_cap to edge-api cache. Redis errors are logged but
	// non-fatal: edge-api will read-through within ttl on its next miss.
	if s.workspaceCtx.redis != nil {
		key := hardCapRedisKey(in.WorkspaceID)
		if rerr := s.workspaceCtx.redis.Set(ctx, key, b.HardCap.String(), hardCapRedisTTL).Err(); rerr != nil {
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

// DeleteAlert removes an alert.
func (s *Service) DeleteAlert(ctx context.Context, alertID uuid.UUID) error {
	if s.workspaceCtx == nil {
		return fmt.Errorf("budgets: workspace surface not wired")
	}
	if err := s.workspaceCtx.wrepo.DeleteAlert(ctx, alertID); err != nil {
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

// MonthToDateSpend returns the workspace's month-to-date BDT-subunit spend.
func (s *Service) MonthToDateSpend(ctx context.Context, workspaceID uuid.UUID, periodStart time.Time) (*big.Int, error) {
	if s.workspaceCtx == nil {
		return nil, fmt.Errorf("budgets: workspace surface not wired")
	}
	return s.workspaceCtx.wrepo.MonthToDateSpendBDT(ctx, workspaceID, periodStart)
}

// validThreshold checks whether a threshold percentage is in the allow-list.
func validThreshold(pct int) bool {
	return pct == 50 || pct == 80 || pct == 100
}

// startOfMonthUTC returns the first instant of the month containing t.
func startOfMonthUTC(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}
