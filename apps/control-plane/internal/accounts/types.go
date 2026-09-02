package accounts

import (
	"errors"
	"strings"
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
//
// Delivered and Delivery report what actually happened to the message, and they
// are the only permitted source for what the interface tells the user. Before
// this existed the console reported "Invitation sent" on every successful write
// while no transport existed at all (issue #1440).
type InvitationResult struct {
	ID        uuid.UUID
	Email     string
	Role      string
	Token     string
	ExpiresAt time.Time
	Delivered bool
	// Delivery is one of DeliverySent, DeliveryNotConfigured, DeliveryFailed.
	Delivery string
}

// InvitationSummary is the customer-safe projection of an outstanding
// invitation. It deliberately carries no token and no hash: the raw token
// exists once, in the create response, and the stored hash is not something any
// listing needs.
type InvitationSummary struct {
	ID        uuid.UUID
	Email     string
	Role      string
	// Status is InvitationStatusPending or InvitationStatusExpired.
	Status    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Statuses an outstanding invitation can be listed under. An accepted
// invitation is not outstanding and is never listed.
const (
	InvitationStatusPending = "pending"
	InvitationStatusExpired = "expired"
)

// AccountProfile holds core pre-billing profile data.
type AccountProfile struct {
	AccountID            uuid.UUID
	OwnerName            string
	LoginEmail           string
	ProfileSetupComplete bool
}

// Member is a projection of a membership used in list responses. Email is the
// member's auth.users email, carried so member listings can show a human
// identity instead of a bare UUID. It is empty when the auth row is missing.
type Member struct {
	UserID uuid.UUID
	Email  string
	Role   string
	Status string
}

// ViewerContext is the full viewer response returned by the viewer API.
type ViewerContext struct {
	User           ViewerUser
	CurrentAccount AccountSummary
	Memberships    []MembershipSummary
	Permissions    []string
	// WorkspaceAdmin reports whether this caller administers the workspace in
	// scope, resolved from public.tenant_users for the tenant they have
	// selected and widened by the platform-admin overlay: the same two reads,
	// in the same order, that platform.WorkspaceAdminGate performs before it
	// admits a request.
	//
	// It exists because CurrentAccount.Role answers a different question.
	// That role comes from public.account_memberships, the billing-account
	// scope, and a personal tenant's sole owner is 'owner' there while
	// deliberately staying 'MEMBER' in tenant_users
	// (signup.insertPersonalMembership). A console that gated the
	// workspace-administration surfaces on the account role therefore rendered
	// pages whose data fetch this gate then answered 403, leaving that user on
	// an empty state telling them to ask an administrator who does not exist
	// (issue #1660).
	WorkspaceAdmin bool
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

// ErrAlreadyAccepted is returned when an invitation token was already redeemed.
// A fresh link is required; retrying the same one can never succeed.
var ErrAlreadyAccepted = errors.New("accounts: invitation already accepted")

// ErrAlreadyMember is returned when the accepting user already holds a
// membership on the invited workspace. The invitation is moot rather than
// broken, and asking for a fresh link would not help.
var ErrAlreadyMember = errors.New("accounts: already a member of this workspace")

// ErrInvalidRole is returned when a caller supplies a membership role outside
// the supported set (see RoleOwner, RoleMember).
var ErrInvalidRole = errors.New("accounts: invalid role")

// ErrSelfRoleChange is returned when an actor tries to change their own
// membership role. Nobody may grant or revoke their own privileges.
var ErrSelfRoleChange = errors.New("accounts: cannot change your own role")

// ErrLastOwner is returned when a role change would leave a workspace with no
// active owner.
var ErrLastOwner = errors.New("accounts: workspace must keep at least one owner")

// ErrNoActiveWorkspace is returned when a viewer holds no active membership and
// workspace provisioning did not produce one.
var ErrNoActiveWorkspace = errors.New("accounts: no active workspace available")

// ErrSlugTaken is returned when creating an account collides with an
// existing accounts.slug value. buildSlug has no per-user uniqueifier, so
// this fires in two distinct situations that provisionDefaultWorkspace tells
// apart: two concurrent requests provisioning the *same* viewer's first
// workspace (Next.js Server Components call getViewer() unmemoized per
// component, so a brand-new user's first page load can race this insert),
// and two different viewers whose display names happen to collapse to the
// same slug.
var ErrSlugTaken = errors.New("accounts: workspace slug already taken")

// ErrEmailNotVerified is returned when a viewer whose email address is not
// verified tries to accept an invitation. Accepting is what grants the role, so
// it carries at least the verification requirement that issuing an invitation
// already carries.
var ErrEmailNotVerified = errors.New("accounts: email address must be verified before accepting an invitation")

// ErrMembershipActivation is returned when accepting an invitation fails to
// activate the invitee's pending membership row. It is deliberately distinct
// from ErrNotFound, which the HTTP layer reports as an invalid invitation link:
// the link was valid, the write behind it was not.
var ErrMembershipActivation = errors.New("accounts: could not activate the invited membership")

// Supported membership roles. The database enforces the same set via a CHECK
// constraint on public.account_memberships.role.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Supported membership statuses, matching the CHECK constraint on
// public.account_memberships.status. Only StatusActive carries authority:
// StatusInvited records an offered seat that nobody has accepted yet, so every
// authorization decision reads the status as well as the role.
const (
	StatusActive  = "active"
	StatusInvited = "invited"
)

// NormalizeRole lower-cases and trims a caller-supplied role, defaulting an
// empty value to RoleMember. Anything outside the supported set is rejected
// with ErrInvalidRole rather than silently coerced.
func NormalizeRole(role string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "":
		return RoleMember, nil
	case RoleMember:
		return RoleMember, nil
	case RoleOwner:
		return RoleOwner, nil
	default:
		return "", ErrInvalidRole
	}
}
