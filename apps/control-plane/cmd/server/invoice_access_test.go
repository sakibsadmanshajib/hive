package main

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
)

// membershipRepoStub serves a fixed membership list and nothing else. The
// embedded interface is nil on purpose: any other repository call this adapter
// starts making should panic in the test rather than quietly return a zero
// value.
type membershipRepoStub struct {
	accounts.Repository
	memberships []accounts.Membership
}

func (s *membershipRepoStub) ListMembershipsByUserID(context.Context, uuid.UUID) ([]accounts.Membership, error) {
	return s.memberships, nil
}

// Invoices are readable by any member of the workspace. Membership rows come
// back active and invited alike, so an adapter that matched on account id alone
// let a person who was merely invited read the workspace's invoices without
// ever accepting the invitation.
func TestInvoiceAccessRequiresActiveMembership(t *testing.T) {
	workspaceID := uuid.New()
	userID := uuid.New()

	for _, tc := range []struct {
		name   string
		status string
		want   bool
	}{
		{"active member reads invoices", accounts.StatusActive, true},
		{"invited member does not read invoices", accounts.StatusInvited, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checker := newAccountsAccessChecker(&membershipRepoStub{
				memberships: []accounts.Membership{
					{AccountID: workspaceID, UserID: userID, Role: accounts.RoleMember, Status: tc.status},
				},
			})

			got, err := checker.IsWorkspaceMember(context.Background(), userID, workspaceID)
			if err != nil {
				t.Fatalf("IsWorkspaceMember: %v", err)
			}
			if got != tc.want {
				t.Fatalf("IsWorkspaceMember for status=%s = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
