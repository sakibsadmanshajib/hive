package grants

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/google/uuid"
)

// AdminChecker is the narrow port the grant service uses to verify the
// caller is a platform admin. Mirrors platform.RoleService.IsPlatformAdmin
// without importing the package directly (avoids cyclic test setup; matches
// accept-interfaces-return-structs).
type AdminChecker interface {
	IsPlatformAdmin(ctx context.Context, userID uuid.UUID) (bool, error)
}

// Service encapsulates the owner-discretionary grant business logic:
//
//   - owner gate (platform admin only)
//   - amount validation (positive, BDT subunits)
//   - same-tx ledger append (delegated to Repository.CreateWithLedger)
//   - immutable audit row (the credit_grants row itself; no separate
//     audit pipeline because the schema-level append-only trigger gives
//     us the durability guarantee).
type Service struct {
	repo  Repository
	admin AdminChecker
}

// NewService constructs the grant service.
func NewService(repo Repository, admin AdminChecker) *Service {
	return &Service{repo: repo, admin: admin}
}

// Create validates input, enforces the owner gate, and atomically inserts
// the grant + ledger entry. Returns ErrForbidden when the granter is not a
// platform admin. Returns ErrInvalidAmount / ErrInvalidGrantee on bad input.
//
// The handler is expected to have already resolved the grantee (email |
// phone) into a (userID, workspaceID) pair. The service does NOT touch
// lookups — single responsibility.
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	if in.GrantedByUserID == uuid.Nil {
		return CreateResult{}, fmt.Errorf("grants: granter user_id required")
	}
	if in.GrantedToUserID == uuid.Nil || in.GrantedToWorkspaceID == uuid.Nil {
		return CreateResult{}, ErrInvalidGrantee
	}
	if in.AmountBDTSubunits == nil || in.AmountBDTSubunits.Sign() <= 0 {
		return CreateResult{}, ErrInvalidAmount
	}

	// Trim & cap reason note (free-form; no enforced format).
	in.ReasonNote = strings.TrimSpace(in.ReasonNote)
	if len(in.ReasonNote) > 1000 {
		in.ReasonNote = in.ReasonNote[:1000]
	}

	isAdmin, err := s.admin.IsPlatformAdmin(ctx, in.GrantedByUserID)
	if err != nil {
		return CreateResult{}, fmt.Errorf("grants: admin check: %w", err)
	}
	if !isAdmin {
		return CreateResult{}, ErrForbidden
	}

	return s.repo.CreateWithLedger(ctx, in)
}

// Get returns a grant by id. Authorisation must be enforced by the caller
// (handler layer): admin OR self-as-grantee.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (CreditGrant, error) {
	return s.repo.Get(ctx, id)
}

// ListAll returns all grants (admin-only; handler enforces).
func (s *Service) ListAll(ctx context.Context, cursor *uuid.UUID, limit int) ([]CreditGrant, error) {
	return s.repo.List(ctx, ListFilter{Cursor: cursor, Limit: limit})
}

// ListByGrantor returns grants issued by the given user (admin-only).
func (s *Service) ListByGrantor(ctx context.Context, grantorID uuid.UUID, cursor *uuid.UUID, limit int) ([]CreditGrant, error) {
	if grantorID == uuid.Nil {
		return nil, fmt.Errorf("grants: grantor id required")
	}
	return s.repo.List(ctx, ListFilter{GrantorUserID: grantorID, Cursor: cursor, Limit: limit})
}

// ListForGrantee returns grants received by the given user — used by
// `GET /v1/credit-grants/me` (read-only, any authenticated user).
func (s *Service) ListForGrantee(ctx context.Context, granteeUserID uuid.UUID, cursor *uuid.UUID, limit int) ([]CreditGrant, error) {
	if granteeUserID == uuid.Nil {
		return nil, fmt.Errorf("grants: grantee id required")
	}
	return s.repo.List(ctx, ListFilter{GranteeUserID: granteeUserID, Cursor: cursor, Limit: limit})
}

// AmountString returns the grant amount as a decimal string of BDT
// subunits — preserves the math/big invariant across wire/round-trip.
func AmountString(amount *big.Int) string {
	if amount == nil {
		return "0"
	}
	return amount.String()
}
