package accounts_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// This file covers issues #534 and #536: role selection at invite time, role
// editing for existing members, and the two authorization invariants that
// protect a workspace from losing control of itself (no self role change, and
// the last owner can never be demoted).

// --- helpers ---

// workspaceFixture seeds one account plus the given memberships and returns the
// account id. Emails are seeded so member listings can carry a human identity.
func workspaceFixture(repo *stubRepo, members ...accounts.Membership) uuid.UUID {
	accountID := uuid.New()
	if len(members) > 0 {
		accountID = members[0].AccountID
	}
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "role-workspace",
		DisplayName: "Role Workspace",
		AccountType: "business",
		OwnerUserID: members[0].UserID,
	}
	repo.memberships = append(repo.memberships, members...)
	return accountID
}

func membership(accountID, userID uuid.UUID, role string) accounts.Membership {
	return accounts.Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    userID,
		Role:      role,
		Status:    "active",
	}
}

func verifiedViewer(userID uuid.UUID, email string) auth.Viewer {
	return auth.Viewer{UserID: userID, Email: email, EmailVerified: true}
}

func memberRole(t *testing.T, repo *stubRepo, accountID, userID uuid.UUID) string {
	t.Helper()
	members, err := repo.ListMembersByAccountID(context.Background(), accountID)
	if err != nil {
		t.Fatalf("ListMembersByAccountID error: %v", err)
	}
	for _, m := range members {
		if m.UserID == userID {
			return m.Role
		}
	}
	t.Fatalf("user %s is not a member of account %s", userID, accountID)
	return ""
}

// --- invitation role selection (#536) ---

func TestCreateInvitation_StoresTheSelectedOwnerRole(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	accountID := workspaceFixture(repo, membership(uuid.New(), ownerID, "owner"))
	svc := accounts.NewService(repo)

	result, err := svc.CreateInvitation(context.Background(), accountID,
		verifiedViewer(ownerID, "owner@example.com"), "coowner@example.com", "owner")
	if err != nil {
		t.Fatalf("CreateInvitation error: %v", err)
	}

	inv, err := repo.FindInvitationByTokenHash(context.Background(), accounts.HashToken(result.Token))
	if err != nil {
		t.Fatalf("FindInvitationByTokenHash error: %v", err)
	}
	if inv.Role != "owner" {
		t.Errorf("expected stored invitation role owner, got %q", inv.Role)
	}
}

func TestCreateInvitation_DefaultsToMemberWhenRoleOmitted(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	accountID := workspaceFixture(repo, membership(uuid.New(), ownerID, "owner"))
	svc := accounts.NewService(repo)

	result, err := svc.CreateInvitation(context.Background(), accountID,
		verifiedViewer(ownerID, "owner@example.com"), "teammate@example.com", "")
	if err != nil {
		t.Fatalf("CreateInvitation error: %v", err)
	}

	inv, err := repo.FindInvitationByTokenHash(context.Background(), accounts.HashToken(result.Token))
	if err != nil {
		t.Fatalf("FindInvitationByTokenHash error: %v", err)
	}
	if inv.Role != "member" {
		t.Errorf("expected stored invitation role member, got %q", inv.Role)
	}
}

func TestCreateInvitation_RejectsAnUnknownRole(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	accountID := workspaceFixture(repo, membership(uuid.New(), ownerID, "owner"))
	svc := accounts.NewService(repo)

	_, err := svc.CreateInvitation(context.Background(), accountID,
		verifiedViewer(ownerID, "owner@example.com"), "teammate@example.com", "superadmin")
	if !errors.Is(err, accounts.ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
	if len(repo.invitations) != 0 {
		t.Error("an invitation was stored for an invalid role")
	}
}

func TestAcceptInvitation_GrantsTheInvitedOwnerRole(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	inviteeID := uuid.New()
	accountID := workspaceFixture(repo, membership(uuid.New(), ownerID, "owner"))

	rawToken := "owner-invite-token"
	repo.invitations[accounts.HashToken(rawToken)] = &accounts.Invitation{
		ID:              uuid.New(),
		AccountID:       accountID,
		Email:           "coowner@example.com",
		Role:            "owner",
		TokenHash:       accounts.HashToken(rawToken),
		ExpiresAt:       time.Now().Add(72 * time.Hour),
		InvitedByUserID: ownerID,
	}

	svc := accounts.NewService(repo)
	if _, err := svc.AcceptInvitation(context.Background(),
		verifiedViewer(inviteeID, "coowner@example.com"), rawToken); err != nil {
		t.Fatalf("AcceptInvitation error: %v", err)
	}

	if got := memberRole(t, repo, accountID, inviteeID); got != "owner" {
		t.Errorf("expected the accepted membership role to be owner, got %q", got)
	}
}

// --- invitation lifecycle errors (#534) ---

func TestAcceptInvitation_UnknownTokenIsNotFound(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)

	_, err := svc.AcceptInvitation(context.Background(),
		verifiedViewer(uuid.New(), "nobody@example.com"), "no-such-token")
	if !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestAcceptInvitation_AlreadyAcceptedTokenIsRejected(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	accountID := workspaceFixture(repo, membership(uuid.New(), ownerID, "owner"))

	acceptedAt := time.Now().Add(-time.Hour)
	rawToken := "used-invite-token"
	repo.invitations[accounts.HashToken(rawToken)] = &accounts.Invitation{
		ID:              uuid.New(),
		AccountID:       accountID,
		Email:           "invitee@example.com",
		Role:            "member",
		TokenHash:       accounts.HashToken(rawToken),
		ExpiresAt:       time.Now().Add(72 * time.Hour),
		AcceptedAt:      &acceptedAt,
		InvitedByUserID: ownerID,
	}

	svc := accounts.NewService(repo)
	_, err := svc.AcceptInvitation(context.Background(),
		verifiedViewer(uuid.New(), "invitee@example.com"), rawToken)
	if !errors.Is(err, accounts.ErrAlreadyAccepted) {
		t.Fatalf("expected ErrAlreadyAccepted, got %v", err)
	}
}

// Accepting an invitation for a workspace the user already belongs to used to
// surface the membership unique-constraint violation as an opaque server error.
// The reason is knowable, so it must be stated.
func TestAcceptInvitation_AlreadyAMemberIsRejectedTruthfully(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	memberID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, memberID, "member"),
	)

	rawToken := "second-invite-token"
	repo.invitations[accounts.HashToken(rawToken)] = &accounts.Invitation{
		ID:              uuid.New(),
		AccountID:       accountID,
		Email:           "member@example.com",
		Role:            "member",
		TokenHash:       accounts.HashToken(rawToken),
		ExpiresAt:       time.Now().Add(72 * time.Hour),
		InvitedByUserID: ownerID,
	}

	svc := accounts.NewService(repo)
	_, err := svc.AcceptInvitation(context.Background(),
		verifiedViewer(memberID, "member@example.com"), rawToken)
	if !errors.Is(err, accounts.ErrAlreadyMember) {
		t.Fatalf("expected ErrAlreadyMember, got %v", err)
	}
}

// --- role editing invariants (#536) ---

func TestUpdateMemberRole_OwnerPromotesAMember(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	memberID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, memberID, "member"),
	)

	svc := accounts.NewService(repo)
	if err := svc.UpdateMemberRole(context.Background(), accountID,
		verifiedViewer(ownerID, "owner@example.com"), memberID, "owner"); err != nil {
		t.Fatalf("UpdateMemberRole error: %v", err)
	}

	if got := memberRole(t, repo, accountID, memberID); got != "owner" {
		t.Errorf("expected member promoted to owner, got %q", got)
	}
}

func TestUpdateMemberRole_RejectsChangingYourOwnRole(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	secondOwnerID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, secondOwnerID, "owner"),
	)

	svc := accounts.NewService(repo)
	err := svc.UpdateMemberRole(context.Background(), accountID,
		verifiedViewer(ownerID, "owner@example.com"), ownerID, "member")
	if !errors.Is(err, accounts.ErrSelfRoleChange) {
		t.Fatalf("expected ErrSelfRoleChange, got %v", err)
	}
	if got := memberRole(t, repo, accountID, ownerID); got != "owner" {
		t.Errorf("role changed despite the self-change guard: %q", got)
	}
}

// A member holds neither members.manage nor members.invite, so a member
// escalating themselves (or anyone else) must be refused by the policy before
// any repository write happens.
func TestUpdateMemberRole_RejectsAMemberEscalatingThemselves(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	memberID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, memberID, "member"),
	)

	svc := accounts.NewService(repo)
	err := svc.UpdateMemberRole(context.Background(), accountID,
		verifiedViewer(memberID, "member@example.com"), memberID, "owner")

	var gateErr *accounts.GateError
	if !accounts.AsGateError(err, &gateErr) {
		t.Fatalf("expected a GateError, got %v", err)
	}
	if gateErr.Code != "permission_denied" {
		t.Errorf("expected code permission_denied, got %q", gateErr.Code)
	}
	if got := memberRole(t, repo, accountID, memberID); got != "member" {
		t.Errorf("role changed despite the permission denial: %q", got)
	}
}

// A platform admin holds members.manage through the overlay, so it is the one
// actor that could otherwise demote a workspace's only owner and leave it
// ownerless. The guard must refuse that too.
func TestUpdateMemberRole_RejectsAPlatformAdminDemotingTheLastOwner(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	adminID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, adminID, "member"),
	)

	store := &stubPlatformAdminStore{adminUsers: map[uuid.UUID]bool{adminID: true}}
	svc := accounts.NewService(repo).WithRoleService(platform.NewRoleService(store))

	err := svc.UpdateMemberRole(context.Background(), accountID,
		verifiedViewer(adminID, "admin@example.com"), ownerID, "member")
	if !errors.Is(err, accounts.ErrLastOwner) {
		t.Fatalf("expected ErrLastOwner, got %v", err)
	}
	if got := memberRole(t, repo, accountID, ownerID); got != "owner" {
		t.Errorf("the last owner was demoted: %q", got)
	}
}

// The sole owner demoting themselves is the other way a workspace loses its
// last owner. The last-owner guard is checked before the self-change guard so
// the refusal names the real reason.
func TestUpdateMemberRole_SoleOwnerCannotDemoteThemselves(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	memberID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, memberID, "member"),
	)

	svc := accounts.NewService(repo)
	err := svc.UpdateMemberRole(context.Background(), accountID,
		verifiedViewer(ownerID, "owner@example.com"), ownerID, "member")
	if !errors.Is(err, accounts.ErrLastOwner) {
		t.Fatalf("expected ErrLastOwner, got %v", err)
	}
	if got := memberRole(t, repo, accountID, ownerID); got != "owner" {
		t.Errorf("the last owner was demoted: %q", got)
	}
}

func TestUpdateMemberRole_SecondOwnerMayBeDemoted(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	secondOwnerID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, secondOwnerID, "owner"),
	)

	svc := accounts.NewService(repo)
	if err := svc.UpdateMemberRole(context.Background(), accountID,
		verifiedViewer(ownerID, "owner@example.com"), secondOwnerID, "member"); err != nil {
		t.Fatalf("UpdateMemberRole error: %v", err)
	}
	if got := memberRole(t, repo, accountID, secondOwnerID); got != "member" {
		t.Errorf("expected the second owner demoted to member, got %q", got)
	}
}

func TestUpdateMemberRole_RejectsAnUnknownRole(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	memberID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, memberID, "member"),
	)

	svc := accounts.NewService(repo)
	err := svc.UpdateMemberRole(context.Background(), accountID,
		verifiedViewer(ownerID, "owner@example.com"), memberID, "root")
	if !errors.Is(err, accounts.ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
}

func TestUpdateMemberRole_RejectsANonMemberTarget(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo, membership(accountID, ownerID, "owner"))

	svc := accounts.NewService(repo)
	err := svc.UpdateMemberRole(context.Background(), accountID,
		verifiedViewer(ownerID, "owner@example.com"), uuid.New(), "owner")
	if !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateMemberRole_RejectsAnUnverifiedOwner(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	memberID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, memberID, "member"),
	)

	svc := accounts.NewService(repo)
	err := svc.UpdateMemberRole(context.Background(), accountID,
		auth.Viewer{UserID: ownerID, Email: "owner@example.com", EmailVerified: false},
		memberID, "owner")

	var gateErr *accounts.GateError
	if !accounts.AsGateError(err, &gateErr) {
		t.Fatalf("expected a GateError, got %v", err)
	}
	if got := memberRole(t, repo, accountID, memberID); got != "member" {
		t.Errorf("role changed for an unverified actor: %q", got)
	}
}

// --- HTTP surface ---

func patchRoleRequest(t *testing.T, accountID, targetUserID uuid.UUID, role string, viewer auth.Viewer) *http.Request {
	t.Helper()
	body := `{"role":"` + role + `"}`
	req := httptest.NewRequest(http.MethodPatch,
		"/api/v1/accounts/current/members/"+targetUserID.String(), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hive-Account-ID", accountID.String())
	return req.WithContext(viewerCtx(viewer))
}

func TestUpdateMemberRoleHandler_OwnerSucceeds(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	memberID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, memberID, "member"),
	)

	rr := httptest.NewRecorder()
	newHandler(repo).ServeHTTP(rr, patchRoleRequest(t, accountID, memberID, "owner",
		verifiedViewer(ownerID, "owner@example.com")))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if got := memberRole(t, repo, accountID, memberID); got != "owner" {
		t.Errorf("expected role owner after the PATCH, got %q", got)
	}
}

func TestUpdateMemberRoleHandler_MemberIsForbidden(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	memberID := uuid.New()
	otherID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, memberID, "member"),
		membership(accountID, otherID, "member"),
	)

	rr := httptest.NewRecorder()
	newHandler(repo).ServeHTTP(rr, patchRoleRequest(t, accountID, otherID, "owner",
		verifiedViewer(memberID, "member@example.com")))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["code"] != "permission_denied" {
		t.Errorf("expected code permission_denied, got %v", resp["code"])
	}
	if got := memberRole(t, repo, accountID, otherID); got != "member" {
		t.Errorf("role changed despite the 403: %q", got)
	}
}

func TestUpdateMemberRoleHandler_SelfChangeIsForbidden(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	secondOwnerID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, secondOwnerID, "owner"),
	)

	rr := httptest.NewRecorder()
	newHandler(repo).ServeHTTP(rr, patchRoleRequest(t, accountID, ownerID, "member",
		verifiedViewer(ownerID, "owner@example.com")))

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["code"] != "self_role_change_forbidden" {
		t.Errorf("expected code self_role_change_forbidden, got %v", resp["code"])
	}
}

func TestUpdateMemberRoleHandler_LastOwnerIsAConflict(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	memberID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo,
		membership(accountID, ownerID, "owner"),
		membership(accountID, memberID, "member"),
	)

	rr := httptest.NewRecorder()
	newHandler(repo).ServeHTTP(rr, patchRoleRequest(t, accountID, ownerID, "member",
		verifiedViewer(ownerID, "owner@example.com")))

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["code"] != "last_owner_required" {
		t.Errorf("expected code last_owner_required, got %v", resp["code"])
	}
	if got := memberRole(t, repo, accountID, ownerID); got != "owner" {
		t.Errorf("the last owner was demoted: %q", got)
	}
}

func TestUpdateMemberRoleHandler_RejectsAMalformedUserID(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo, membership(accountID, ownerID, "owner"))

	req := httptest.NewRequest(http.MethodPatch,
		"/api/v1/accounts/current/members/not-a-uuid", strings.NewReader(`{"role":"owner"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hive-Account-ID", accountID.String())
	req = req.WithContext(viewerCtx(verifiedViewer(ownerID, "owner@example.com")))

	rr := httptest.NewRecorder()
	newHandler(repo).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

// --- member listing carries a human identity (#536) ---

func TestMembersHandler_IncludesMemberEmail(t *testing.T) {
	repo := newStubRepo()
	ownerID := uuid.New()
	accountID := uuid.New()
	workspaceFixture(repo, membership(accountID, ownerID, "owner"))
	repo.emails[ownerID] = "owner@example.com"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/current/members", nil)
	req.Header.Set("X-Hive-Account-ID", accountID.String())
	req = req.WithContext(viewerCtx(verifiedViewer(ownerID, "owner@example.com")))

	rr := httptest.NewRecorder()
	newHandler(repo).ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Members []struct {
			UserID string `json:"user_id"`
			Email  string `json:"email"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(resp.Members))
	}
	if resp.Members[0].Email != "owner@example.com" {
		t.Errorf("expected the member email in the listing, got %q", resp.Members[0].Email)
	}
}

// --- accept invitation error codes (#534) ---

func acceptRequest(token string, viewer auth.Viewer) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/invitations/accept",
		strings.NewReader(`{"token":"`+token+`"}`))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(viewerCtx(viewer))
}

func TestAcceptInvitationHandler_LifecycleErrorCodes(t *testing.T) {
	ownerID := uuid.New()
	accountID := uuid.New()

	seed := func(t *testing.T, mutate func(inv *accounts.Invitation)) *stubRepo {
		t.Helper()
		repo := newStubRepo()
		workspaceFixture(repo, membership(accountID, ownerID, "owner"))
		hash := accounts.HashToken("lifecycle-token")
		inv := &accounts.Invitation{
			ID:              uuid.New(),
			AccountID:       accountID,
			Email:           "invitee@example.com",
			Role:            "member",
			TokenHash:       hash,
			ExpiresAt:       time.Now().Add(72 * time.Hour),
			InvitedByUserID: ownerID,
		}
		mutate(inv)
		repo.invitations[hash] = inv
		return repo
	}

	acceptedAt := time.Now().Add(-time.Hour)

	cases := []struct {
		name       string
		mutate     func(inv *accounts.Invitation)
		viewer     auth.Viewer
		token      string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "expired",
			mutate:     func(inv *accounts.Invitation) { inv.ExpiresAt = time.Now().Add(-time.Minute) },
			viewer:     verifiedViewer(uuid.New(), "invitee@example.com"),
			token:      "lifecycle-token",
			wantStatus: http.StatusGone,
			wantCode:   "invitation_expired",
		},
		{
			name:       "already accepted",
			mutate:     func(inv *accounts.Invitation) { inv.AcceptedAt = &acceptedAt },
			viewer:     verifiedViewer(uuid.New(), "invitee@example.com"),
			token:      "lifecycle-token",
			wantStatus: http.StatusConflict,
			wantCode:   "invitation_already_accepted",
		},
		{
			name:       "wrong account",
			mutate:     func(inv *accounts.Invitation) {},
			viewer:     verifiedViewer(uuid.New(), "someone.else@example.com"),
			token:      "lifecycle-token",
			wantStatus: http.StatusForbidden,
			wantCode:   "invitation_email_mismatch",
		},
		{
			name:       "already a member",
			mutate:     func(inv *accounts.Invitation) { inv.Email = "owner@example.com" },
			viewer:     verifiedViewer(ownerID, "owner@example.com"),
			token:      "lifecycle-token",
			wantStatus: http.StatusConflict,
			wantCode:   "invitation_already_member",
		},
		{
			name:       "unknown token",
			mutate:     func(inv *accounts.Invitation) {},
			viewer:     verifiedViewer(uuid.New(), "invitee@example.com"),
			token:      "some-other-token",
			wantStatus: http.StatusNotFound,
			wantCode:   "invitation_not_found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := seed(t, tc.mutate)
			rr := httptest.NewRecorder()
			newHandler(repo).ServeHTTP(rr, acceptRequest(tc.token, tc.viewer))

			if rr.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, rr.Code, rr.Body.String())
			}
			var resp map[string]interface{}
			_ = json.Unmarshal(rr.Body.Bytes(), &resp)
			if resp["code"] != tc.wantCode {
				t.Errorf("expected code %q, got %v", tc.wantCode, resp["code"])
			}
			// Internal error text must never reach the customer.
			if strings.Contains(rr.Body.String(), "accounts:") {
				t.Errorf("internal error text leaked to the client: %s", rr.Body.String())
			}
		})
	}
}
