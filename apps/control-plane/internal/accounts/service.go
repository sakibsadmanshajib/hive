package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// Service encapsulates all accounts business logic.
type Service struct {
	repo    Repository
	policy  authz.Policy
	roleSvc *platform.RoleService // optional — see WithRoleService
}

// NewService returns a new accounts Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo, policy: authz.NewPolicy()}
}

// WithRoleService returns a copy of the Service wired with the platform role
// service so EnsureViewerContext can resolve the real platform-admin overlay
// for the viewer's Permissions[]. Without it, isAdmin is always false and a
// real platform admin never sees platform.admin in permissions[], even though
// admin-gated routes (which resolve their own Actor via roleSvc directly)
// correctly allow them. That mismatch is what made Feature Gates and
// Marketplace in web-console refuse to render for real admins.
func (s *Service) WithRoleService(roleSvc *platform.RoleService) *Service {
	cloned := *s
	cloned.roleSvc = roleSvc
	return &cloned
}

// EnsureViewerContext returns the full viewer context for the given viewer.
// If requestedAccountID is non-nil and the viewer is an active member, that
// account is used as current_account; otherwise the first membership is used.
// On first visit (no memberships) a default workspace is provisioned.
func (s *Service) EnsureViewerContext(ctx context.Context, viewer auth.Viewer, requestedAccountID uuid.UUID) (ViewerContext, error) {
	memberships, err := s.repo.ListMembershipsByUserID(ctx, viewer.UserID)
	if err != nil {
		return ViewerContext{}, fmt.Errorf("accounts: list memberships: %w", err)
	}

	// Provision default workspace on first login.
	if len(memberships) == 0 {
		if err := s.provisionDefaultWorkspace(ctx, viewer); err != nil {
			return ViewerContext{}, err
		}
		memberships, err = s.repo.ListMembershipsByUserID(ctx, viewer.UserID)
		if err != nil {
			return ViewerContext{}, fmt.Errorf("accounts: list memberships after bootstrap: %w", err)
		}
	}

	// Resolve current account.
	chosen := memberships[0]
	if requestedAccountID != uuid.Nil {
		for _, m := range memberships {
			if m.AccountID == requestedAccountID && m.Status == "active" {
				chosen = m
				break
			}
		}
	}

	currentAcct, err := s.repo.GetAccountByID(ctx, chosen.AccountID)
	if err != nil {
		return ViewerContext{}, fmt.Errorf("accounts: get current account: %w", err)
	}

	// Build membership summaries.
	summaries := make([]MembershipSummary, 0, len(memberships))
	for _, m := range memberships {
		acct, err := s.repo.GetAccountByID(ctx, m.AccountID)
		if err != nil {
			continue
		}
		summaries = append(summaries, MembershipSummary{
			AccountID:   m.AccountID,
			DisplayName: acct.DisplayName,
			Role:        m.Role,
			Status:      m.Status,
		})
	}

	// Phase 18 Plan 04: emit permissions[] — the full granted-permission set for
	// the chosen workspace actor. Gates fields removed from response wire shape.
	// isAdmin resolves the real platform_admin overlay when roleSvc is wired
	// (see WithRoleService); nil roleSvc (most unit tests, and any caller that
	// has not wired it) keeps the prior workspace-scoped false behaviour.
	isAdmin := false
	if s.roleSvc != nil {
		admin, err := s.roleSvc.IsPlatformAdmin(ctx, viewer.UserID)
		if err != nil {
			return ViewerContext{}, fmt.Errorf("accounts: platform admin lookup: %w", err)
		}
		isAdmin = admin
	}
	chosenActor := ActorFor(viewer, chosen, isAdmin)
	permissions := s.policy.AllGranted(chosenActor)
	if permissions == nil {
		permissions = []string{}
	}

	return ViewerContext{
		User: ViewerUser{
			ID:            viewer.UserID,
			Email:         viewer.Email,
			EmailVerified: viewer.EmailVerified,
		},
		CurrentAccount: AccountSummary{
			ID:          currentAcct.ID,
			DisplayName: currentAcct.DisplayName,
			AccountType: currentAcct.AccountType,
			Role:        chosen.Role,
		},
		Memberships: summaries,
		Permissions: permissions,
	}, nil
}

// provisionDefaultWorkspace creates a default personal account, owner membership,
// and core profile row for the viewer.
func (s *Service) provisionDefaultWorkspace(ctx context.Context, viewer auth.Viewer) error {
	displayName := buildDisplayName(viewer.FullName, viewer.Email)
	slug := buildSlug(displayName)

	accountID := uuid.New()
	acct := Account{
		ID:          accountID,
		Slug:        slug,
		DisplayName: displayName,
		AccountType: "personal",
		OwnerUserID: viewer.UserID,
	}
	if err := s.repo.CreateAccount(ctx, acct); err != nil {
		return fmt.Errorf("accounts: create default account: %w", err)
	}

	membership := Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    viewer.UserID,
		Role:      "owner",
		Status:    "active",
	}
	if err := s.repo.CreateMembership(ctx, membership); err != nil {
		return fmt.Errorf("accounts: create owner membership: %w", err)
	}

	ownerName := viewer.FullName
	if ownerName == "" {
		ownerName = emailLocalPart(viewer.Email)
	}
	profile := AccountProfile{
		AccountID:            accountID,
		OwnerName:            ownerName,
		LoginEmail:           viewer.Email,
		ProfileSetupComplete: false,
	}
	if err := s.repo.CreateProfile(ctx, profile); err != nil {
		return fmt.Errorf("accounts: create account profile: %w", err)
	}

	return nil
}

// CreateInvitation creates a new invitation for email on accountID with the
// given membership role ("owner" or "member"; empty defaults to "member").
// The viewer must hold members.invite permission (verified owner, or admin overlay).
func (s *Service) CreateInvitation(ctx context.Context, accountID uuid.UUID, viewer auth.Viewer, email, role string) (InvitationResult, error) {
	// Validate the requested role before any write. An unsupported role is a
	// client error, never a coerced default.
	invitedRole, err := NormalizeRole(role)
	if err != nil {
		return InvitationResult{}, err
	}

	actor, err := s.resolveWorkspaceActor(ctx, accountID, viewer)
	if err != nil {
		return InvitationResult{}, err
	}
	if !s.policy.Can(actor, authz.PermMembersInvite) {
		return InvitationResult{}, &GateError{
			Code:    "permission_denied",
			Message: "members.invite permission required",
		}
	}

	rawToken, tokenHash, err := generateToken()
	if err != nil {
		return InvitationResult{}, fmt.Errorf("accounts: generate token: %w", err)
	}

	expiresAt := time.Now().Add(72 * time.Hour)
	inv := Invitation{
		ID:              uuid.New(),
		AccountID:       accountID,
		Email:           email,
		Role:            invitedRole,
		TokenHash:       tokenHash,
		ExpiresAt:       expiresAt,
		InvitedByUserID: viewer.UserID,
	}
	if err := s.repo.CreateInvitation(ctx, inv); err != nil {
		return InvitationResult{}, fmt.Errorf("accounts: store invitation: %w", err)
	}

	return InvitationResult{
		ID:        inv.ID,
		Email:     email,
		Role:      invitedRole,
		Token:     rawToken,
		ExpiresAt: expiresAt,
	}, nil
}

// AcceptInvitation accepts a pending invitation for the viewer.
// The viewer email must match the invitation email (case-insensitive).
// Returns the joined account ID. Does not alter the current-account selection.
func (s *Service) AcceptInvitation(ctx context.Context, viewer auth.Viewer, rawToken string) (uuid.UUID, error) {
	tokenHash := HashToken(rawToken)

	inv, err := s.repo.FindInvitationByTokenHash(ctx, tokenHash)
	if err != nil {
		return uuid.Nil, fmt.Errorf("accounts: find invitation: %w", err)
	}

	if time.Now().After(inv.ExpiresAt) {
		return uuid.Nil, ErrExpired
	}

	if !strings.EqualFold(viewer.Email, inv.Email) {
		return uuid.Nil, ErrEmailMismatch
	}

	if inv.AcceptedAt != nil {
		return uuid.Nil, ErrAlreadyAccepted
	}

	// The membership carries the role the workspace chose at invite time. An
	// unrecognised stored value falls back to the least-privileged role rather
	// than granting something unintended.
	grantedRole, err := NormalizeRole(inv.Role)
	if err != nil {
		grantedRole = RoleMember
	}

	now := time.Now()
	if err := s.repo.AcceptInvitation(ctx, inv.ID, now); err != nil {
		return uuid.Nil, fmt.Errorf("accounts: accept invitation: %w", err)
	}

	membership := Membership{
		ID:        uuid.New(),
		AccountID: inv.AccountID,
		UserID:    viewer.UserID,
		Role:      grantedRole,
		Status:    "active",
	}
	if err := s.repo.CreateMembership(ctx, membership); err != nil {
		return uuid.Nil, fmt.Errorf("accounts: create member membership: %w", err)
	}

	return inv.AccountID, nil
}

// ListMembers returns all members of the given account.
func (s *Service) ListMembers(ctx context.Context, accountID uuid.UUID) ([]Member, error) {
	return s.repo.ListMembersByAccountID(ctx, accountID)
}

// UpdateMemberRole changes targetUserID's role within accountID. The decision is
// made entirely server-side from the viewer's own membership and the workspace's
// current owner count; nothing about the caller's claimed authority is trusted.
//
// Refusal order, so every rejection names its real reason:
//  1. invalid role -> ErrInvalidRole.
//  2. viewer lacks members.manage (or is not an active member) -> GateError.
//  3. target is not an active member -> ErrNotFound.
//  4. change would leave the workspace with no active owner -> ErrLastOwner.
//  5. viewer is the target -> ErrSelfRoleChange (no self escalation, and no
//     self demotion either: privileges are granted by someone else or not at all).
func (s *Service) UpdateMemberRole(ctx context.Context, accountID uuid.UUID, viewer auth.Viewer, targetUserID uuid.UUID, role string) error {
	newRole, err := NormalizeRole(role)
	if err != nil {
		return err
	}

	actor, err := s.resolveWorkspaceActor(ctx, accountID, viewer)
	if err != nil {
		return err
	}
	if !s.policy.Can(actor, authz.PermMembersManage) {
		return &GateError{
			Code:    "permission_denied",
			Message: "members.manage permission required",
		}
	}

	members, err := s.repo.ListMembersByAccountID(ctx, accountID)
	if err != nil {
		return fmt.Errorf("accounts: list members: %w", err)
	}

	var target Member
	found := false
	activeOwners := 0
	for _, m := range members {
		if m.Status != "active" {
			continue
		}
		if m.Role == RoleOwner {
			activeOwners++
		}
		if m.UserID == targetUserID {
			target = m
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}

	if target.Role == RoleOwner && newRole != RoleOwner && activeOwners <= 1 {
		return ErrLastOwner
	}

	if targetUserID == viewer.UserID {
		return ErrSelfRoleChange
	}

	if target.Role == newRole {
		// Already the requested role: nothing to write.
		return nil
	}

	if err := s.repo.UpdateMembershipRole(ctx, accountID, targetUserID, newRole); err != nil {
		return err
	}
	return nil
}

// resolveWorkspaceActor loads the viewer's active membership for accountID and
// maps it (plus the platform-admin overlay when a role service is wired) into an
// authz.Actor. A viewer with no active membership is an authorization failure,
// not a server error, so the caller can map it to 403.
func (s *Service) resolveWorkspaceActor(ctx context.Context, accountID uuid.UUID, viewer auth.Viewer) (authz.Actor, error) {
	memberships, err := s.repo.ListMembershipsByUserID(ctx, viewer.UserID)
	if err != nil {
		return authz.Actor{}, fmt.Errorf("accounts: list memberships: %w", err)
	}

	var chosen Membership
	found := false
	for _, m := range memberships {
		if m.AccountID == accountID && m.Status == "active" {
			chosen = m
			found = true
			break
		}
	}
	if !found {
		return authz.Actor{}, &GateError{
			Code:    "permission_denied",
			Message: fmt.Sprintf("viewer is not an active member of account %s", accountID),
		}
	}

	// isAdmin resolves the real platform-admin overlay when roleSvc is wired
	// (see WithRoleService): a real platform admin who is not a workspace owner
	// still holds members.* via the overlay instead of being silently denied.
	isAdmin := false
	if s.roleSvc != nil {
		admin, err := s.roleSvc.IsPlatformAdmin(ctx, viewer.UserID)
		if err != nil {
			return authz.Actor{}, fmt.Errorf("accounts: platform admin lookup: %w", err)
		}
		isAdmin = admin
	}

	return ActorFor(viewer, chosen, isAdmin), nil
}

// --- helpers ---

// buildDisplayName returns the workspace display name from the viewer's info.
func buildDisplayName(fullName, email string) string {
	if fullName != "" {
		return fullName + "'s Workspace"
	}
	return emailLocalPart(email) + "'s Workspace"
}

// emailLocalPart returns the part of an email before the @.
func emailLocalPart(email string) string {
	idx := strings.IndexByte(email, '@')
	if idx < 0 {
		return email
	}
	return email[:idx]
}

// buildSlug produces a lowercase kebab-case slug from a display name.
func buildSlug(displayName string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(displayName) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteRune('-')
			prevDash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	return s
}

// generateToken creates a cryptographically random 32-byte token.
// Returns both the raw (plaintext) token and its SHA-256 hex hash.
func generateToken() (rawToken, tokenHash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	rawToken = hex.EncodeToString(b)
	tokenHash = HashToken(rawToken)
	return rawToken, tokenHash, nil
}

// HashToken returns the SHA-256 hex hash of a raw token.
// Exported so tests can pre-compute the hash for known raw tokens.
func HashToken(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:])
}
