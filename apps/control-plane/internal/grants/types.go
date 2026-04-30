// Package grants — Phase 14 owner-discretionary credit grant primitive.
//
// A grant is an owner-issued (platform admin) credit injection into a target
// workspace. Grants are append-only at both schema and application layers:
//
//   - The `public.credit_grants` table has no `updated_at` column.
//   - A BEFORE UPDATE OR DELETE trigger raises an exception on any mutation
//     attempt (see migration 20260428_01).
//   - This package exposes Create / Get / List APIs only — no Update, no
//     Delete.
//
// Every grant produces exactly one ledger append in the same Postgres
// transaction (atomic commit/rollback). Reuses the ledger primitive's table
// `public.credit_ledger_entries` directly within the grants service tx so
// the two writes commit together. Documented as Rule 1 deviation in the
// Task 5 verification log because the existing ledger.Service.GrantCredits
// API runs its own internal transaction and cannot be composed with an
// outer tx without breaking the public API contract.
package grants

import (
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
)

// CreditGrant is the canonical wire shape for a discretionary credit grant
// row. Money is BDT subunits (paisa). math/big is used at the application
// boundary even though int64 fits — discipline guard for future BDT
// quantities that may exceed int64 capacity (vanishingly unlikely but
// matches Phase 14 conventions across budgets/invoices).
type CreditGrant struct {
	ID                   uuid.UUID  `json:"id"`
	GrantedByUserID      uuid.UUID  `json:"granted_by_user_id"`
	GrantedToUserID      uuid.UUID  `json:"granted_to_user_id"`
	GrantedToWorkspaceID uuid.UUID  `json:"granted_to_workspace_id"`
	AmountBDTSubunits    *big.Int   `json:"-"` // marshalled via wire layer
	ReasonNote           string     `json:"reason_note,omitempty"`
	LedgerEntryID        uuid.UUID  `json:"ledger_entry_id"`
	Currency             string     `json:"currency"`
	CreatedAt            time.Time  `json:"created_at"`
}

// CreateInput is the validated request payload accepted by Service.Create.
//
// The handler resolves the grantee (email | phone) into (userID, workspaceID)
// before invoking the service — the service itself never touches lookups.
// IdempotencyKey is OPTIONAL but recommended; absence yields a generated UUID.
type CreateInput struct {
	GrantedByUserID      uuid.UUID
	GrantedToUserID      uuid.UUID
	GrantedToWorkspaceID uuid.UUID
	AmountBDTSubunits    *big.Int
	ReasonNote           string
	IdempotencyKey       string
}

// CreateResult is what Service.Create returns: the persisted grant row plus
// the ledger entry id (a single grant produces a single ledger append).
type CreateResult struct {
	Grant         CreditGrant
	LedgerEntryID uuid.UUID
}

// Sentinel errors. Callers MUST use errors.Is for comparison.
var (
	// ErrNotFound is returned by Get when the grant id does not resolve.
	ErrNotFound = errors.New("grants: not found")

	// ErrForbidden is returned by Service.Create when the granter is not a
	// platform admin. Surfaced as 403 by the handler. Provider-blind.
	ErrForbidden = errors.New("grants: granter is not platform admin")

	// ErrInvalidAmount is returned when amount_bdt_subunits is nil, zero,
	// or negative.
	ErrInvalidAmount = errors.New("grants: amount must be positive")

	// ErrInvalidGrantee is returned when grantee user/workspace ids are zero.
	ErrInvalidGrantee = errors.New("grants: grantee user_id and workspace_id required")
)

// ListFilter is the cursor-paginated filter used by Service.List* APIs.
type ListFilter struct {
	// One of the two scoping ids must be non-zero.
	GrantorUserID   uuid.UUID
	GranteeUserID   uuid.UUID
	GranteeWorkspaceID uuid.UUID

	// Cursor is the last-seen grant id; nil for first page. Pagination is
	// keyset-based on (created_at DESC, id DESC).
	Cursor *uuid.UUID
	Limit  int
}
