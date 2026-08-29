package agentsched

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/ledger"
)

// ErrInsufficientCredits is returned when the tenant's billing account cannot
// cover the launch floor. Distinct from a lookup failure on purpose: a caller
// that cannot tell the two apart has to guess, and the guess that gets made is
// always "let it through", which is the defect this gate exists to close
// (issue #1490).
var ErrInsufficientCredits = errors.New("agentsched: insufficient credits")

// ErrSolvencyUnavailable wraps a solvency lookup that failed outright, so the
// wire layer can answer "ask again" instead of either "you are out of credits"
// or a bare 500. It is always a refusal: the tenant's balance is unknown, and
// the only safe reading of an unknown balance is that work does not start.
var ErrSolvencyUnavailable = errors.New("agentsched: could not determine credit balance")

// launchFloor is the credit balance a tenant must be able to cover before a
// sandbox is launched for it, and before a routine that will launch sandboxes
// is created. 100,000,000 credits is 0.10 USD at the D-046 rate of
// 1 USD = 1,000,000,000 credits, the same figure one chat turn holds
// (apps/edge-api/internal/inference.DefaultHoldText).
//
// It is an authorization floor, never a charge, and never an estimate of what
// the task will go on to spend. What the task spends is billed per model turn
// where those turns are dispatched.
const launchFloor int64 = 100_000_000

// Solvency answers whether one tenant can be allowed to start work that costs
// credits.
//
// Three outcomes, not two. A nil error means solvent. ErrInsufficientCredits
// means the balance is short. Any other error means the lookup itself failed
// and the answer is unknown; a caller must refuse on that too, never pass.
// Folding the third case into either of the first two is precisely how a
// solvency gate fails open under a database blip.
type Solvency interface {
	Check(ctx context.Context, tenantID uuid.UUID, floorCredits int64) error
}

// balanceReader is the slice of ledger.Service that pgxSolvency needs.
// Declared here rather than taking the concrete type so the dependency stays
// one method wide.
type balanceReader interface {
	GetBalance(ctx context.Context, accountID uuid.UUID) (ledger.BalanceSummary, error)
}

// pgxSolvency resolves the tenant's billing account and reads its available
// balance. It performs no arithmetic of its own: the posted, reserved and
// available summation, and the over-release corruption signal that rides with
// it, both belong to ledger.Service.GetBalance.
type pgxSolvency struct {
	pool     *pgxpool.Pool
	balances balanceReader
}

// NewPgxSolvency builds the production Solvency. Both arguments are required;
// a Solvency that cannot read a balance would answer every question the same
// way, and there is no safe direction for that answer.
func NewPgxSolvency(pool *pgxpool.Pool, balances balanceReader) Solvency {
	if pool == nil {
		panic("agentsched: nil pool for solvency")
	}
	if balances == nil {
		panic("agentsched: nil balance reader for solvency")
	}
	return &pgxSolvency{pool: pool, balances: balances}
}

// deploymentEnterpriseEdge mirrors one of the two values allowed by
// public.tenants.deployment's CHECK constraint
// (20260516_01_phase19_tenants.sql), and the edge-api constant
// metering.DeploymentEnterpriseEdge. Restated here rather than imported
// because that constant lives in a different service and a different module.
const deploymentEnterpriseEdge = "ENTERPRISE_EDGE"

func (s *pgxSolvency) Check(ctx context.Context, tenantID uuid.UUID, floorCredits int64) error {
	var deployment string
	var accountID *uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT t.deployment, tba.account_id
		FROM public.tenants t
		LEFT JOIN public.tenant_billing_accounts tba ON tba.tenant_id = t.id
		WHERE t.id = $1
	`, tenantID).Scan(&deployment, &accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The tenant does not exist. Nothing can be charged for what its
		// sandbox spends, so it does not get one. Reported as insufficient
		// rather than as a lookup failure because it is a settled answer, not
		// an unknown one: a retry reads exactly the same thing.
		return ErrInsufficientCredits
	}
	if err != nil {
		return fmt.Errorf("agentsched: resolve tenant billing state: %w", err)
	}

	// Posture before balance, deliberately, and before the account is even
	// looked at. A Hive Enterprise tenant runs its own box and has no prepaid
	// relationship with Hive at all, so it holds no credits and never will.
	// Refusing it for an empty balance would take routines off the air across
	// the entire self-hosted product, which is a mode this repository ships,
	// not a hypothetical. Same precedence and the same reason as the chat
	// path's own posture check (apps/edge-api/internal/chat/billing.go, and
	// the enterprise_shadow rule in metering/precedence.go), so the two
	// surfaces answer an Enterprise tenant the same way.
	if deployment == deploymentEnterpriseEdge {
		return nil
	}
	if accountID == nil {
		// A Hive Cloud tenant with no billing account cannot be charged for
		// what the sandbox goes on to spend. This is the same state, and the
		// same refusal, that the chat path reports as billing not configured.
		return ErrInsufficientCredits
	}

	balance, err := s.balances.GetBalance(ctx, *accountID)
	if err != nil {
		return fmt.Errorf("agentsched: read balance: %w", err)
	}
	if balance.AvailableCredits < floorCredits {
		return ErrInsufficientCredits
	}
	return nil
}
