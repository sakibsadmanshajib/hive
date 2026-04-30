package budgets

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// =============================================================================
// Phase 14 — Workspace Budgets (soft/hard caps in BDT subunits, math/big)
// =============================================================================
//
// These types layer on top of the legacy single-threshold-per-account model
// (BudgetThreshold below). The legacy surface is preserved for backwards
// compatibility with existing console pages while Phase 14 introduces the new
// Budget + SpendAlert + Cron evaluator + edge-api hard-cap gate.
//
// All money values use math/big.Int; the underlying BIGINT column stores the
// exact subunit count (paisa, 1 BDT = 100 paisa). The math/big API is enforced
// across every service / cron / gate path. float64 is banned in this package
// (verified by grep in PLAN verify block).
// =============================================================================

// Budget is a per-workspace soft + hard cap row.
// SoftCap < HardCap is enforced both in DB CHECK and at the service layer.
type Budget struct {
	WorkspaceID uuid.UUID
	PeriodStart time.Time
	SoftCap     *big.Int
	HardCap     *big.Int
	Currency    string // always "BDT" — DB CHECK enforces
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SpendAlert is a threshold-based notification config for a workspace.
// ThresholdPct must be one of (50, 80, 100) — DB CHECK enforces.
type SpendAlert struct {
	ID              uuid.UUID
	WorkspaceID     uuid.UUID
	ThresholdPct    int
	Email           *string
	WebhookURL      *string
	WebhookSecret   *string
	LastFiredAt     *time.Time
	LastFiredPeriod *time.Time
	CreatedAt       time.Time
}

// SetBudgetInput carries the inputs to upsert a budget for a workspace.
type SetBudgetInput struct {
	WorkspaceID uuid.UUID
	PeriodStart time.Time
	SoftCap     *big.Int
	HardCap     *big.Int
}

// CreateAlertInput carries the inputs to create a spend alert.
type CreateAlertInput struct {
	WorkspaceID   uuid.UUID
	ThresholdPct  int
	Email         *string
	WebhookURL    *string
	WebhookSecret *string
}

// UpdateAlertInput carries the inputs to update an existing spend alert.
type UpdateAlertInput struct {
	ID            uuid.UUID
	Email         *string
	WebhookURL    *string
	WebhookSecret *string
}

// =============================================================================
// Phase 14 sentinel errors
// =============================================================================

// ErrInvalidCaps is returned when soft >= hard or either cap is negative.
var ErrInvalidCaps = errors.New("budgets: hard_cap must be >= soft_cap and both non-negative")

// ErrInvalidThreshold is returned when threshold_pct is not in (50, 80, 100).
var ErrInvalidThreshold = errors.New("budgets: threshold_pct must be one of 50, 80, 100")

// ErrHardCapExceeded is returned by the edge-api budget gate when MTD spend has
// crossed the workspace's hard_cap.
var ErrHardCapExceeded = errors.New("budgets: workspace hard cap exceeded")

// ErrBudgetNotFound is returned when no budget row exists for a workspace.
var ErrBudgetNotFound = errors.New("budgets: budget not found")

// =============================================================================
// Phase 14 repository surface (extends ThresholdRepository semantics)
// =============================================================================

// WorkspaceBudgetRepository is the data-access surface for workspace budgets,
// spend alerts, and MTD spend aggregation. Defined where it's used per Go
// interface-placement convention.
type WorkspaceBudgetRepository interface {
	// Budget CRUD
	GetBudget(ctx context.Context, workspaceID uuid.UUID) (*Budget, error)
	UpsertBudget(ctx context.Context, in SetBudgetInput) (*Budget, error)
	DeleteBudget(ctx context.Context, workspaceID uuid.UUID) error

	// Spend alert CRUD
	ListAlerts(ctx context.Context, workspaceID uuid.UUID) ([]SpendAlert, error)
	CreateAlert(ctx context.Context, in CreateAlertInput) (*SpendAlert, error)
	UpdateAlert(ctx context.Context, in UpdateAlertInput) (*SpendAlert, error)
	DeleteAlert(ctx context.Context, alertID uuid.UUID) error

	// Cron support
	ListWorkspacesWithBudget(ctx context.Context) ([]uuid.UUID, error)
	StampAlertFired(ctx context.Context, alertID uuid.UUID, firedAt time.Time, period time.Time) error

	// MTD spend aggregation — sums usage_charge entries credits_delta (negative)
	// for the workspace within [periodStart, now). Returns absolute *big.Int
	// (positive value representing total BDT subunits spent).
	MonthToDateSpendBDT(ctx context.Context, workspaceID uuid.UUID, periodStart time.Time) (*big.Int, error)
}

// AlertNotifier dispatches spend alert notifications via configured channels.
// Phase 14 ships email (LogNotifier fallback when SMTP unconfigured) + webhook
// (HMAC-SHA256 signed). Phase 18 may extend with SMS / push.
type AlertNotifier interface {
	NotifySpendAlert(ctx context.Context, alert SpendAlert, workspaceID uuid.UUID, mtd, softCap *big.Int) error
}

// =============================================================================
// Legacy account-budget-threshold surface (preserved for backwards-compat)
// =============================================================================

// BudgetThreshold represents a spend alert threshold for an account.
type BudgetThreshold struct {
	ID               uuid.UUID  `json:"id"`
	AccountID        uuid.UUID  `json:"account_id"`
	ThresholdCredits int64      `json:"threshold_credits"`
	LastNotifiedAt   *time.Time `json:"last_notified_at"`
	AlertDismissed   bool       `json:"alert_dismissed"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// UpsertThresholdInput is the input for creating or updating a budget threshold.
type UpsertThresholdInput struct {
	ThresholdCredits int64 `json:"threshold_credits"`
}

// ThresholdRepository is the minimal repository interface used by Service.
// Production: *pgxRepository. Tests: mockRepo.
type ThresholdRepository interface {
	GetThreshold(ctx context.Context, accountID uuid.UUID) (*BudgetThreshold, error)
	UpsertThreshold(ctx context.Context, accountID uuid.UUID, credits int64) (*BudgetThreshold, error)
	DismissAlert(ctx context.Context, accountID uuid.UUID) error
	MarkNotified(ctx context.Context, accountID uuid.UUID) error
}

// EmailNotifier defines the interface for sending budget alert emails.
// This is an interface so it can be mocked in tests.
type EmailNotifier interface {
	SendBudgetAlert(ctx context.Context, accountID uuid.UUID, threshold BudgetThreshold, currentBalance int64) error
}
