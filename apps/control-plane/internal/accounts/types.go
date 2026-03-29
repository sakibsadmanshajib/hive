package accounts

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Account is the top-level workspace entity.
type Account struct {
	ID          uuid.UUID
	Slug        string
	DisplayName string
	AccountType string // "personal" or "business"
	OwnerUserID uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Membership links a Supabase user to an account with a role and status.
type Membership struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	UserID    uuid.UUID
	Role      string // "owner" or "member"
	Status    string // "active" or "invited"
	CreatedAt time.Time
}

// Invitation tracks a pending email invitation.
type Invitation struct {
	ID              uuid.UUID
	AccountID       uuid.UUID
	Email           string
	Role            string
	TokenHash       string
	ExpiresAt       time.Time
	AcceptedAt      *time.Time
	InvitedByUserID uuid.UUID
	CreatedAt       time.Time
}

// InvitationResult is what callers receive when creating an invitation.
// Token is the plaintext token (returned once, not stored).
type InvitationResult struct {
	ID        uuid.UUID
	Email     string
	Token     string
	ExpiresAt time.Time
}

// AccountProfile holds core pre-billing profile data.
type AccountProfile struct {
	AccountID            uuid.UUID
	OwnerName            string
	LoginEmail           string
	ProfileSetupComplete bool
}

// Member is a projection of a membership used in list responses.
type Member struct {
	UserID uuid.UUID
	Role   string
	Status string
}

// ViewerContext is the full viewer response returned by the viewer API.
type ViewerContext struct {
	User           ViewerUser
	CurrentAccount AccountSummary
	Memberships    []MembershipSummary
	Gates          Gates
}

// ViewerUser is the user portion of the viewer context.
type ViewerUser struct {
	ID            uuid.UUID
	Email         string
	EmailVerified bool
}

// AccountSummary is the account portion of the viewer context.
type AccountSummary struct {
	ID          uuid.UUID
	DisplayName string
	AccountType string
	Role        string
}

// MembershipSummary is the membership portion of the viewer context.
type MembershipSummary struct {
	AccountID   uuid.UUID
	DisplayName string
	Role        string
	Status      string
}

// Gates holds the capability flags computed for the viewer.
type Gates struct {
	CanInviteMembers bool
	CanManageAPIKeys bool
}

// GateError is returned when a policy gate blocks an operation.
type GateError struct {
	Code    string
	Message string
}

func (e *GateError) Error() string {
	return e.Code + ": " + e.Message
}

// AsGateError is a helper for errors.As with GateError.
func AsGateError(err error, target **GateError) bool {
	return errors.As(err, target)
}

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("accounts: not found")

// ErrExpired is returned when an invitation token has expired.
var ErrExpired = errors.New("accounts: invitation expired")

// ErrEmailMismatch is returned when the accepting user email does not match the invitation.
var ErrEmailMismatch = errors.New("accounts: email mismatch")
