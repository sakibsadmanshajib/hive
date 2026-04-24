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
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

// Service encapsulates all accounts business logic.
type Service struct {
	repo Repository
}

// NewService returns a new accounts Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
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

	gates := Gates{
		CanInviteMembers: viewer.EmailVerified && chosen.Role == "owner",
		CanManageAPIKeys: viewer.EmailVerified && chosen.Role == "owner",
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
		Gates:       gates,
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

// CreateInvitation creates a new invitation for email on accountID.
// The viewer must be a verified owner of the account.
func (s *Service) CreateInvitation(ctx context.Context, accountID uuid.UUID, viewer auth.Viewer, email string) (InvitationResult, error) {
	if !viewer.EmailVerified {
		return InvitationResult{}, &GateError{
			Code:    "email_verification_required",
			Message: "email must be verified before inviting members",
		}
	}

	// Verify the viewer is an owner of this account.
	memberships, err := s.repo.ListMembershipsByUserID(ctx, viewer.UserID)
	if err != nil {
		return InvitationResult{}, fmt.Errorf("accounts: list memberships: %w", err)
	}
	isOwner := false
	for _, m := range memberships {
		if m.AccountID == accountID && m.Role == "owner" && m.Status == "active" {
			isOwner = true
			break
		}
	}
	if !isOwner {
		return InvitationResult{}, fmt.Errorf("accounts: viewer is not an owner of account %s", accountID)
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
		Role:            "member",
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
		return uuid.Nil, fmt.Errorf("accounts: invitation already accepted")
	}

	now := time.Now()
	if err := s.repo.AcceptInvitation(ctx, inv.ID, now); err != nil {
		return uuid.Nil, fmt.Errorf("accounts: accept invitation: %w", err)
	}

	membership := Membership{
		ID:        uuid.New(),
		AccountID: inv.AccountID,
		UserID:    viewer.UserID,
		Role:      "member",
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
