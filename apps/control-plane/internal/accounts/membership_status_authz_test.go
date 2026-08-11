package accounts_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
)

// ActorFor is the single funnel every workspace-scoped handler builds its actor
// through, so it is where a membership's status has to be read. Nine call sites
// across budgets, apikeys, ledger, usage, accounting, profiles and accounts pass
// a membership they assembled themselves; guarding them one at a time leaves the
// next handler free to reopen the escalation by passing a raw row.
func TestActorFor_InvitedMembershipGrantsNothing(t *testing.T) {
	viewer := auth.Viewer{UserID: uuid.New(), Email: "invitee@example.com", EmailVerified: true}
	membership := accounts.Membership{
		AccountID: uuid.New(),
		UserID:    viewer.UserID,
		Role:      accounts.RoleOwner,
		Status:    accounts.StatusInvited,
	}

	actor := accounts.ActorFor(viewer, membership, false)
	if granted := authz.NewPolicy().AllGranted(actor); len(granted) != 0 {
		t.Fatalf("an invited membership granted %v, want nothing", granted)
	}

	// The same row, accepted, is the ordinary owner it always was.
	membership.Status = accounts.StatusActive
	actor = accounts.ActorFor(viewer, membership, false)
	if !authz.NewPolicy().Can(actor, authz.PermBillingWrite) {
		t.Fatal("an active owner must still hold billing.write")
	}
}

// The platform-admin overlay is resolved by its own query, which applies its own
// active-membership predicate, so an invited workspace membership must not strip
// it from a real platform operator.
func TestActorFor_InvitedMembershipKeepsPlatformAdminOverlay(t *testing.T) {
	viewer := auth.Viewer{UserID: uuid.New(), Email: "operator@example.com", EmailVerified: true}
	actor := accounts.ActorFor(viewer, accounts.Membership{
		AccountID: uuid.New(),
		UserID:    viewer.UserID,
		Role:      accounts.RoleOwner,
		Status:    accounts.StatusInvited,
	}, true)

	if !authz.NewPolicy().Can(actor, authz.PermPlatformAdmin) {
		t.Fatal("the platform-admin overlay must survive a non-active workspace membership")
	}
}

// Invite, then accept, then act. A pre-written invited row confers nothing, so
// acceptance has to be able to transition it: refusing it as "already a member"
// would leave the invitee permanently unable to join or to gain any authority.
func TestAcceptInvitation_ActivatesAPreWrittenInvitedRow(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)
	ctx := context.Background()

	inviterID, accountID := uuid.New(), uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "invite-flow",
		DisplayName: "Invite Flow Workspace",
		AccountType: "business",
		OwnerUserID: inviterID,
	}
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    inviterID,
		Role:      accounts.RoleOwner,
		Status:    accounts.StatusActive,
	})

	inviter := auth.Viewer{UserID: inviterID, Email: "owner@example.com", EmailVerified: true}
	invitee := auth.Viewer{UserID: uuid.New(), Email: "invitee@example.com", EmailVerified: true}

	result, err := svc.CreateInvitation(ctx, accountID, inviter, invitee.Email, accounts.RoleMember)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}

	// The seat is written before acceptance, which is the state the escalation
	// fix made powerless and the acceptance path therefore has to handle.
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    invitee.UserID,
		Role:      accounts.RoleMember,
		Status:    accounts.StatusInvited,
	})

	beforeAccept, err := svc.EnsureViewerContext(ctx, invitee, accountID)
	if err != nil {
		t.Fatalf("EnsureViewerContext before accept: %v", err)
	}
	if beforeAccept.CurrentAccount.ID == accountID {
		t.Fatal("an unaccepted invitation must not select the workspace")
	}

	joined, err := svc.AcceptInvitation(ctx, invitee, result.Token)
	if err != nil {
		t.Fatalf("AcceptInvitation: %v", err)
	}
	if joined != accountID {
		t.Fatalf("AcceptInvitation returned %s, want %s", joined, accountID)
	}

	// Accepted: the workspace is now selectable and the actor built from it
	// carries the member's authority.
	afterAccept, err := svc.EnsureViewerContext(ctx, invitee, accountID)
	if err != nil {
		t.Fatalf("EnsureViewerContext after accept: %v", err)
	}
	if afterAccept.CurrentAccount.ID != accountID {
		t.Fatalf("current account = %s, want the newly joined %s", afterAccept.CurrentAccount.ID, accountID)
	}
	actor := accounts.ActorFor(invitee, accounts.Membership{
		AccountID: accountID,
		UserID:    invitee.UserID,
		Role:      afterAccept.CurrentAccount.Role,
		Status:    accounts.StatusActive,
	}, false)
	if !authz.NewPolicy().Can(actor, authz.PermLedgerView) {
		t.Fatal("an accepted member must hold the member-level permissions")
	}

	// Exactly one membership row on that account, still one seat.
	seats := 0
	for _, m := range repo.memberships {
		if m.AccountID == accountID && m.UserID == invitee.UserID {
			seats++
			if m.Status != accounts.StatusActive {
				t.Fatalf("membership status after accept = %q, want active", m.Status)
			}
		}
	}
	if seats != 1 {
		t.Fatalf("membership rows for the invitee = %d, want 1", seats)
	}
}

// A second acceptance of the same invitation is still refused, and the wording
// stays truthful: this one really is already a member.
func TestAcceptInvitation_ActiveMemberIsStillRefused(t *testing.T) {
	repo := newStubRepo()
	svc := accounts.NewService(repo)
	ctx := context.Background()

	inviterID, accountID := uuid.New(), uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID: accountID, Slug: "already-in", DisplayName: "Already In", AccountType: "business", OwnerUserID: inviterID,
	}
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID: uuid.New(), AccountID: accountID, UserID: inviterID, Role: accounts.RoleOwner, Status: accounts.StatusActive,
	})

	inviter := auth.Viewer{UserID: inviterID, Email: "owner@example.com", EmailVerified: true}
	invitee := auth.Viewer{UserID: uuid.New(), Email: "member@example.com", EmailVerified: true}

	result, err := svc.CreateInvitation(ctx, accountID, inviter, invitee.Email, accounts.RoleMember)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID: uuid.New(), AccountID: accountID, UserID: invitee.UserID, Role: accounts.RoleMember, Status: accounts.StatusActive,
	})

	if _, err := svc.AcceptInvitation(ctx, invitee, result.Token); err != accounts.ErrAlreadyMember {
		t.Fatalf("AcceptInvitation error = %v, want ErrAlreadyMember", err)
	}
}
