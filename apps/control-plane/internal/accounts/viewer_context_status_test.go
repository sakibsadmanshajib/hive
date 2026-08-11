package accounts_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// A membership row with status 'invited' records an offered seat that nobody
// has accepted. EnsureViewerContext used to take the first membership row of
// any status as the default workspace, and the actor resolver reads the chosen
// membership's role, so a viewer invited as owner operated as owner of that
// workspace before accepting anything. The invitation must still be listed,
// only never selected.
func TestEnsureViewerContext_InvitedMembershipIsNotTheDefaultWorkspace(t *testing.T) {
	repo := newStubRepo()
	invitedAccountID := uuid.New()
	viewer := auth.Viewer{
		UserID:        uuid.New(),
		Email:         "invitee@example.com",
		EmailVerified: true,
		FullName:      "Invitee User",
	}

	repo.accountsMap[invitedAccountID] = &accounts.Account{
		ID:          invitedAccountID,
		Slug:        "someone-elses-workspace",
		DisplayName: "Someone Else's Workspace",
		AccountType: "business",
		OwnerUserID: uuid.New(),
	}
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: invitedAccountID,
		UserID:    viewer.UserID,
		Role:      accounts.RoleOwner,
		Status:    accounts.StatusInvited,
	})

	vc, err := accounts.NewService(repo).EnsureViewerContext(context.Background(), viewer, uuid.Nil)
	if err != nil {
		t.Fatalf("EnsureViewerContext error: %v", err)
	}

	if vc.CurrentAccount.ID == invitedAccountID {
		t.Fatal("an invited membership must never be selected as the current workspace")
	}
	if vc.CurrentAccount.DisplayName != "Invitee User's Workspace" {
		t.Fatalf("expected a freshly provisioned personal workspace, got %q", vc.CurrentAccount.DisplayName)
	}

	// The invitation stays visible so the console can render it.
	listed := false
	for _, m := range vc.Memberships {
		if m.AccountID == invitedAccountID {
			listed = true
			if m.Status != accounts.StatusInvited {
				t.Fatalf("invited membership listed with status %q", m.Status)
			}
		}
	}
	if !listed {
		t.Fatal("the pending invitation must still be listed in memberships")
	}
}
