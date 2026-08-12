package accounts_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
)

// ActorFor is where a membership's status is read on the way into an actor.
//
// This guard is defence in depth, not the protection for the thirteen call
// sites: every one of them passes StatusActive as a literal or has already
// filtered to an active row, so the branch is unreachable in production today.
// EnsureViewerContext and GetMembershipRole are what actually protect those
// paths. What this pins is the behaviour a future handler gets when it starts
// passing a row straight from the database, and it fails against the code
// before this change.
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

	// The platform-admin overlay comes from its own query, which applies its own
	// active-membership predicate, so blanking the workspace role must not cost
	// a real platform operator the overlay.
	adminActor := accounts.ActorFor(viewer, membership, true)
	if !authz.NewPolicy().Can(adminActor, authz.PermPlatformAdmin) {
		t.Fatal("the platform-admin overlay must survive a non-active workspace membership")
	}

	// The same row, accepted, is the ordinary owner it always was.
	membership.Status = accounts.StatusActive
	actor = accounts.ActorFor(viewer, membership, false)
	if !authz.NewPolicy().Can(actor, authz.PermBillingWrite) {
		t.Fatal("an active owner must still hold billing.write")
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
	// Built from the stored row rather than a hardcoded status, so a failed
	// activation shows up here as a denial instead of passing on a fixture.
	stored := membershipRow(t, repo, accountID, invitee.UserID)
	actor := accounts.ActorFor(invitee, stored, false)
	if !authz.NewPolicy().Can(actor, authz.PermLedgerView) {
		t.Fatalf("an accepted member must hold the member-level permissions (stored row: role=%s status=%s)", stored.Role, stored.Status)
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

// membershipRow returns the single stored membership row for (account, user),
// so assertions read what acceptance actually wrote instead of restating it.
func membershipRow(t *testing.T, repo *stubRepo, accountID, userID uuid.UUID) accounts.Membership {
	t.Helper()
	for _, m := range repo.memberships {
		if m.AccountID == accountID && m.UserID == userID {
			return m
		}
	}
	t.Fatalf("no membership row for user %s on account %s", userID, accountID)
	return accounts.Membership{}
}

// failingActivationRepo makes the activation write fail the way a lost race
// does, so the error the invitee is shown can be asserted.
type failingActivationRepo struct {
	*stubRepo
}

func (r *failingActivationRepo) ActivateMembership(context.Context, uuid.UUID, uuid.UUID, string) error {
	return accounts.ErrNotFound
}

// A failed activation must never be reported as an invalid invitation link. The
// HTTP layer maps ErrNotFound to "this invitation link is not valid", so
// propagating the repository's ErrNotFound would tell someone with a perfectly
// good invitation to go find a better one.
func TestAcceptInvitation_ActivationFailureIsNotReportedAsABadLink(t *testing.T) {
	base := newStubRepo()
	repo := &failingActivationRepo{stubRepo: base}
	svc := accounts.NewService(repo)
	ctx := context.Background()

	inviterID, accountID := uuid.New(), uuid.New()
	base.accountsMap[accountID] = &accounts.Account{
		ID: accountID, Slug: "activation-failure", DisplayName: "Activation Failure",
		AccountType: "business", OwnerUserID: inviterID,
	}
	base.memberships = append(base.memberships, accounts.Membership{
		ID: uuid.New(), AccountID: accountID, UserID: inviterID,
		Role: accounts.RoleOwner, Status: accounts.StatusActive,
	})

	inviter := auth.Viewer{UserID: inviterID, Email: "owner@example.com", EmailVerified: true}
	invitee := auth.Viewer{UserID: uuid.New(), Email: "invitee@example.com", EmailVerified: true}

	result, err := svc.CreateInvitation(ctx, accountID, inviter, invitee.Email, accounts.RoleMember)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	base.memberships = append(base.memberships, accounts.Membership{
		ID: uuid.New(), AccountID: accountID, UserID: invitee.UserID,
		Role: accounts.RoleMember, Status: accounts.StatusInvited,
	})

	_, err = svc.AcceptInvitation(ctx, invitee, result.Token)
	if !errors.Is(err, accounts.ErrMembershipActivation) {
		t.Fatalf("AcceptInvitation error = %v, want ErrMembershipActivation", err)
	}
	if errors.Is(err, accounts.ErrNotFound) {
		t.Fatal("the invitee must not be told their invitation link is invalid")
	}

	// The invitation is untouched, so a retry once the write recovers still works.
	if base.acceptCalled {
		t.Fatal("the invitation must not be consumed when the membership write failed")
	}
}
