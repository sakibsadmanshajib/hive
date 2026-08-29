package accounts_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/mailer"
)

// recordingMailer captures what was asked of it and returns whatever was set up.
type recordingMailer struct {
	err  error
	sent []accounts.InvitationEmail
}

func (m *recordingMailer) SendInvitation(_ context.Context, inv accounts.InvitationEmail) error {
	m.sent = append(m.sent, inv)
	return m.err
}

func inviteFixture(t *testing.T) (*stubRepo, uuid.UUID, auth.Viewer) {
	t.Helper()
	repo := newStubRepo()
	ownerUserID := uuid.New()
	accountID := uuid.New()
	repo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "acme",
		DisplayName: "Acme Legal",
		AccountType: "personal",
		OwnerUserID: ownerUserID,
	}
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    ownerUserID,
		Role:      accounts.RoleOwner,
		Status:    accounts.StatusActive,
	})
	return repo, accountID, auth.Viewer{
		UserID:        ownerUserID,
		Email:         "owner@example.com",
		EmailVerified: true,
	}
}

// THE GUARD FOR ISSUE #1440.
//
// The defect was not a broken mailer. It was a success claim with nothing behind
// it: no transport existed anywhere in the backend, and the console reported
// "Invitation sent" every time regardless. So the invariant worth enforcing is
// narrow and absolute: Delivered may be true, and Delivery may be
// DeliverySent, only when a transport ran and returned success. Any other
// state of the world must be reported as something else.
func TestCreateInvitation_DeliveryReportsWhatActuallyHappened(t *testing.T) {
	cases := []struct {
		name         string
		mailer       accounts.InvitationMailer
		wantDelivery string
	}{
		{
			// The state of every deployment that has no relay, and the exact
			// state the whole product was in when the console claimed a send.
			name:         "no mailer wired at all",
			mailer:       nil,
			wantDelivery: accounts.DeliveryNotConfigured,
		},
		{
			name:         "mailer reports it is not configured",
			mailer:       &recordingMailer{err: mailer.ErrNotConfigured},
			wantDelivery: accounts.DeliveryNotConfigured,
		},
		{
			// What the demo box does today: the relay accepts the envelope and
			// then refuses the message body because the account is not activated.
			name:         "relay refuses the message",
			mailer:       &recordingMailer{err: errors.New("mailer: relay refused the message: 502 5.7.0 not activated")},
			wantDelivery: accounts.DeliveryFailed,
		},
		{
			name:         "relay accepts the message",
			mailer:       &recordingMailer{},
			wantDelivery: accounts.DeliverySent,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, accountID, viewer := inviteFixture(t)
			svc := accounts.NewService(repo)
			if tc.mailer != nil {
				svc = svc.WithInvitationMailer(tc.mailer)
			}

			result, err := svc.CreateInvitation(context.Background(), accountID, viewer, "invitee@example.com", accounts.RoleMember)
			if err != nil {
				t.Fatalf("CreateInvitation: %v", err)
			}

			if result.Delivery != tc.wantDelivery {
				t.Errorf("Delivery = %q, want %q", result.Delivery, tc.wantDelivery)
			}
			wantDelivered := tc.wantDelivery == accounts.DeliverySent
			if result.Delivered != wantDelivered {
				t.Errorf("Delivered = %v, want %v", result.Delivered, wantDelivered)
			}
			// The invitation itself always survives. A failed send must never
			// cost the workspace the link, because the link is the fallback.
			if result.Token == "" {
				t.Error("invitation carries no token, so no fallback link can be offered")
			}
		})
	}
}

// The message the invitee receives has to carry enough to act on: who invited
// them, into what, and a token that actually resolves.
func TestCreateInvitation_MailerReceivesUsableInvitation(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	sender := &recordingMailer{}
	svc := accounts.NewService(repo).WithInvitationMailer(sender)

	result, err := svc.CreateInvitation(context.Background(), accountID, viewer, "invitee@example.com", accounts.RoleOwner)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("mailer called %d times, want 1", len(sender.sent))
	}
	sent := sender.sent[0]
	if sent.To != "invitee@example.com" {
		t.Errorf("To = %q", sent.To)
	}
	if sent.WorkspaceName != "Acme Legal" {
		t.Errorf("WorkspaceName = %q, want the account display name", sent.WorkspaceName)
	}
	if sent.InviterEmail != "owner@example.com" {
		t.Errorf("InviterEmail = %q", sent.InviterEmail)
	}
	if sent.Role != accounts.RoleOwner {
		t.Errorf("Role = %q, want the role that was actually granted", sent.Role)
	}
	if sent.Token != result.Token {
		t.Error("the token mailed to the invitee is not the token the invitation was created with")
	}
	if sent.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is zero, so the email cannot state an expiry")
	}
}

// Re-inviting is how a workspace re-sends. It must replace the outstanding
// invitation rather than leave two live tokens for one address, because with
// two live tokens revoking either one leaves the other working.
func TestCreateInvitation_SupersedesAnOutstandingInvitationForTheSameAddress(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	svc := accounts.NewService(repo)

	first, err := svc.CreateInvitation(context.Background(), accountID, viewer, "invitee@example.com", accounts.RoleMember)
	if err != nil {
		t.Fatalf("first CreateInvitation: %v", err)
	}
	// Deliberately different casing. Acceptance compares addresses case
	// insensitively, so superseding must too or the old link survives.
	second, err := svc.CreateInvitation(context.Background(), accountID, viewer, "Invitee@Example.com", accounts.RoleMember)
	if err != nil {
		t.Fatalf("second CreateInvitation: %v", err)
	}
	if first.Token == second.Token {
		t.Fatal("re-inviting reused the token")
	}

	outstanding, err := svc.ListInvitations(context.Background(), accountID, viewer)
	if err != nil {
		t.Fatalf("ListInvitations: %v", err)
	}
	if len(outstanding) != 1 {
		t.Fatalf("outstanding invitations = %d, want 1 after a re-invitation", len(outstanding))
	}
	if outstanding[0].ID != second.ID {
		t.Error("the surviving invitation is not the newest one")
	}

	// The superseded token must be dead, not merely hidden from the listing.
	if _, err := svc.AcceptInvitation(context.Background(), auth.Viewer{
		UserID:        uuid.New(),
		Email:         "invitee@example.com",
		EmailVerified: true,
	}, first.Token); err == nil {
		t.Fatal("the superseded invitation token still accepts")
	}
}

func TestListInvitations_MarksExpiredWithoutHidingThem(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	svc := accounts.NewService(repo)

	live, err := svc.CreateInvitation(context.Background(), accountID, viewer, "live@example.com", accounts.RoleMember)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	stale, err := svc.CreateInvitation(context.Background(), accountID, viewer, "stale@example.com", accounts.RoleMember)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}
	// Age the second one past its expiry.
	for _, inv := range repo.invitations {
		if inv.ID == stale.ID {
			inv.ExpiresAt = time.Now().Add(-time.Hour)
		}
	}

	listed, err := svc.ListInvitations(context.Background(), accountID, viewer)
	if err != nil {
		t.Fatalf("ListInvitations: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed %d invitations, want 2; an expired invitation is still the reason nobody joined", len(listed))
	}
	byID := map[uuid.UUID]accounts.InvitationSummary{}
	for _, item := range listed {
		byID[item.ID] = item
	}
	if got := byID[live.ID].Status; got != accounts.InvitationStatusPending {
		t.Errorf("live invitation status = %q, want %q", got, accounts.InvitationStatusPending)
	}
	if got := byID[stale.ID].Status; got != accounts.InvitationStatusExpired {
		t.Errorf("stale invitation status = %q, want %q", got, accounts.InvitationStatusExpired)
	}
}

func TestRevokeInvitation_KillsTheTokenAndIsScopedToTheAccount(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	svc := accounts.NewService(repo)

	created, err := svc.CreateInvitation(context.Background(), accountID, viewer, "invitee@example.com", accounts.RoleMember)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}

	// An invitation belonging to some other workspace is not revocable through
	// this account, and the refusal leaks nothing about whether it exists.
	if err := svc.RevokeInvitation(context.Background(), uuid.New(), viewer, created.ID); err == nil {
		t.Fatal("revoke succeeded against an account the viewer does not own")
	}

	if err := svc.RevokeInvitation(context.Background(), accountID, viewer, created.ID); err != nil {
		t.Fatalf("RevokeInvitation: %v", err)
	}
	if _, err := svc.AcceptInvitation(context.Background(), auth.Viewer{
		UserID:        uuid.New(),
		Email:         "invitee@example.com",
		EmailVerified: true,
	}, created.Token); err == nil {
		t.Fatal("a revoked invitation still accepts")
	}
	if err := svc.RevokeInvitation(context.Background(), accountID, viewer, created.ID); !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("second revoke = %v, want ErrNotFound", err)
	}
}

// A plain member may not read or revoke what the workspace has outstanding.
func TestInvitationListingAndRevokeRequireTheInvitePermission(t *testing.T) {
	repo, accountID, owner := inviteFixture(t)
	svc := accounts.NewService(repo)
	created, err := svc.CreateInvitation(context.Background(), accountID, owner, "invitee@example.com", accounts.RoleMember)
	if err != nil {
		t.Fatalf("CreateInvitation: %v", err)
	}

	memberUserID := uuid.New()
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    memberUserID,
		Role:      accounts.RoleMember,
		Status:    accounts.StatusActive,
	})
	member := auth.Viewer{UserID: memberUserID, Email: "member@example.com", EmailVerified: true}

	if _, err := svc.ListInvitations(context.Background(), accountID, member); err == nil {
		t.Error("a plain member could list outstanding invitations")
	}
	if err := svc.RevokeInvitation(context.Background(), accountID, member, created.ID); err == nil {
		t.Error("a plain member could revoke an invitation")
	}
}

// The rendered message is the product's first artifact for most invitees, and
// several of its properties are load bearing rather than cosmetic.
func TestInvitationEmailRendersBothAlternatives(t *testing.T) {
	capture := &captureSender{}
	m := accounts.NewInvitationMailer(capture, "https://console.example.test/")

	expires := time.Date(2026, time.September, 1, 9, 30, 0, 0, time.UTC)
	err := m.SendInvitation(context.Background(), accounts.InvitationEmail{
		To:            "invitee@example.test",
		WorkspaceName: "Acme Legal",
		InviterEmail:  "owner@example.test",
		Role:          accounts.RoleMember,
		Token:         "tok en/with+specials",
		ExpiresAt:     expires,
	})
	if err != nil {
		t.Fatalf("SendInvitation: %v", err)
	}

	if capture.msg.Subject != "Join Acme Legal on Hive" {
		t.Errorf("Subject = %q", capture.msg.Subject)
	}
	if strings.TrimSpace(capture.msg.Text) == "" {
		t.Error("no plain-text alternative; the message is unreadable in a client that refuses HTML")
	}
	if strings.TrimSpace(capture.msg.HTML) == "" {
		t.Error("no HTML alternative")
	}

	// The token has to survive the URL intact, or the link resolves to a
	// different token and acceptance fails with "not valid".
	wantURL := "https://console.example.test/invitations/accept?token=tok+en%2Fwith%2Bspecials"
	for name, body := range map[string]string{"text": capture.msg.Text, "html": capture.msg.HTML} {
		if !strings.Contains(body, wantURL) {
			t.Errorf("%s part does not carry the accept URL %q", name, wantURL)
		}
	}
	// An expiry the recipient can read, and permission to ignore the message.
	for _, want := range []string{"1 September 2026", "ignore this email"} {
		if !strings.Contains(capture.msg.Text, want) {
			t.Errorf("text part missing %q", want)
		}
	}
	// No remote content, so nothing about the message depends on images loading.
	if strings.Contains(capture.msg.HTML, "<img") {
		t.Error("the HTML part loads an image; the message must survive images being blocked")
	}
	// Dark mode: the card declares its own colours rather than inheriting, so a
	// client synthesising a dark theme cannot leave dark text on a dark card.
	if !strings.Contains(capture.msg.HTML, "prefers-color-scheme: dark") {
		t.Error("the HTML part has no dark-scheme rules")
	}
	// House style, which reviewers enforce on prose and which is easy to
	// regress in a template nobody reads twice.
	if strings.Contains(capture.msg.Text, " - ") || strings.Contains(capture.msg.Text, "—") {
		t.Error("the text part uses dash punctuation between clauses")
	}
}

// An unconfigured mailer must say so rather than silently succeeding, because
// the caller's whole reason for asking is to tell the user the truth.
func TestInvitationMailerWithoutAConsoleOriginIsNotConfigured(t *testing.T) {
	m := accounts.NewInvitationMailer(&captureSender{}, "")
	err := m.SendInvitation(context.Background(), accounts.InvitationEmail{To: "a@b.test", Token: "t"})
	if !errors.Is(err, mailer.ErrNotConfigured) {
		t.Fatalf("SendInvitation = %v, want ErrNotConfigured", err)
	}
}

type captureSender struct {
	msg mailer.Message
}

func (c *captureSender) Send(_ context.Context, msg mailer.Message) error {
	c.msg = msg
	return nil
}
