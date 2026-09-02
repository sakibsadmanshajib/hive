package accounts_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signupguard"
)

// THE GUARD FOR ISSUE #1745.
//
// The invitation endpoint was the only path in this product that sends mail to
// an address the caller types, and nothing bounded it. One authenticated
// account could loop it and turn the Brevo relay into an open one, which is the
// thing that had the sending domain suspended once already. So the invariant is
// narrow: past the cap, no transport call happens at all. Not a failed send, not
// a silent success. Nothing reaches the mailer.

// memIncrementer is a process-local stand-in for the Redis counter, so these
// tests exercise the real signupguard.RateLimiter (its keys, its windows, its
// fail-closed behaviour) rather than a hand-rolled substitute.
type memIncrementer struct {
	mu     sync.Mutex
	counts map[string]int64
	err    error
}

func newMemIncrementer() *memIncrementer {
	return &memIncrementer{counts: map[string]int64{}}
}

func (m *memIncrementer) IncrWithExpiry(_ context.Context, key string, _ time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return 0, m.err
	}
	m.counts[key]++
	return m.counts[key], nil
}

// limit builds one dimension of the cap over backend.
func limit(backend signupguard.Incrementer, n int, window time.Duration, subject string) accounts.InvitationLimit {
	return accounts.InvitationLimit{
		Allow: signupguard.NewRateLimiter(backend, signupguard.RateLimitConfig{
			Limit:     n,
			Window:    window,
			Namespace: "invite",
			Subject:   subject,
		}).Allow,
		Window: window,
	}
}

func capError(t *testing.T, err error) *accounts.InvitationCapError {
	t.Helper()
	var capErr *accounts.InvitationCapError
	if !errors.As(err, &capErr) {
		t.Fatalf("error is %v (%T), want *accounts.InvitationCapError", err, err)
	}
	return capErr
}

func TestCreateInvitation_PerInviterCapRefusesBeforeAnySend(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	sender := &recordingMailer{}
	backend := newMemIncrementer()
	svc := accounts.NewService(repo).
		WithInvitationMailer(sender).
		WithInvitationLimits(accounts.InvitationLimits{
			Inviter: limit(backend, 3, time.Hour, "user"),
		})

	for i := 0; i < 3; i++ {
		to := fmt.Sprintf("colleague%d@example.com", i)
		if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, to, accounts.RoleMember); err != nil {
			t.Fatalf("invitation %d inside the cap was refused: %v", i, err)
		}
	}

	_, err := svc.CreateInvitation(context.Background(), accountID, viewer, "one-too-many@example.com", accounts.RoleMember)
	if err == nil {
		t.Fatal("the invitation past the cap succeeded; the cap does not bind")
	}
	capErr := capError(t, err)
	if capErr.Unavailable {
		t.Error("refusal reports a backend outage, but the caller ran out of quota")
	}
	if capErr.RetryAfter <= 0 || capErr.RetryAfter > time.Hour {
		t.Errorf("RetryAfter = %v, want a positive duration inside the window", capErr.RetryAfter)
	}
	if len(sender.sent) != 3 {
		t.Fatalf("transport ran %d times, want 3: the refusal must happen before the send", len(sender.sent))
	}
}

func TestCreateInvitation_PerTenantCapBindsAcrossInviters(t *testing.T) {
	repo, accountID, owner := inviteFixture(t)
	// A second owner in the same workspace. A per-inviter cap alone would let a
	// tenant multiply its send budget by the number of accounts it holds.
	secondOwner := auth.Viewer{UserID: uuid.New(), Email: "second@example.com", EmailVerified: true}
	repo.memberships = append(repo.memberships, accounts.Membership{
		ID:        uuid.New(),
		AccountID: accountID,
		UserID:    secondOwner.UserID,
		Role:      accounts.RoleOwner,
		Status:    accounts.StatusActive,
	})

	sender := &recordingMailer{}
	backend := newMemIncrementer()
	svc := accounts.NewService(repo).
		WithInvitationMailer(sender).
		WithInvitationLimits(accounts.InvitationLimits{
			Inviter: limit(backend, 10, time.Hour, "user"),
			Tenant:  limit(backend, 2, 24*time.Hour, "account"),
		})

	if _, err := svc.CreateInvitation(context.Background(), accountID, owner, "a@example.com", accounts.RoleMember); err != nil {
		t.Fatalf("first invitation: %v", err)
	}
	if _, err := svc.CreateInvitation(context.Background(), accountID, secondOwner, "b@example.com", accounts.RoleMember); err != nil {
		t.Fatalf("second invitation: %v", err)
	}

	_, err := svc.CreateInvitation(context.Background(), accountID, secondOwner, "c@example.com", accounts.RoleMember)
	if err == nil {
		t.Fatal("the workspace sent past its own cap by switching inviter")
	}
	capError(t, err)
	if len(sender.sent) != 2 {
		t.Fatalf("transport ran %d times, want 2", len(sender.sent))
	}
}

func TestCreateInvitation_RecipientCooldownStopsRepeatInvites(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	sender := &recordingMailer{}
	backend := newMemIncrementer()
	svc := accounts.NewService(repo).
		WithInvitationMailer(sender).
		WithInvitationLimits(accounts.InvitationLimits{
			RecipientBurst: limit(backend, 1, 5*time.Minute, "recipient"),
		})

	if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, "target@example.com", accounts.RoleMember); err != nil {
		t.Fatalf("first invitation: %v", err)
	}
	if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, "TARGET@example.com", accounts.RoleMember); err == nil {
		t.Fatal("the same address was mailed twice inside the cooldown (address casing evaded the cap)")
	}
	// A different address is unaffected: the cooldown is per target, not a
	// global stop-the-world.
	if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, "someone-else@example.com", accounts.RoleMember); err != nil {
		t.Fatalf("an unrelated address was refused by another address's cooldown: %v", err)
	}
	if len(sender.sent) != 2 {
		t.Fatalf("transport ran %d times, want 2", len(sender.sent))
	}
}

func TestCreateInvitation_CapFailsClosedWhenCounterUnavailable(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	sender := &recordingMailer{}
	backend := newMemIncrementer()
	backend.err = errors.New("redis: connection refused")
	svc := accounts.NewService(repo).
		WithInvitationMailer(sender).
		WithInvitationLimits(accounts.InvitationLimits{
			Inviter: limit(backend, 30, time.Hour, "user"),
		})

	_, err := svc.CreateInvitation(context.Background(), accountID, viewer, "colleague@example.com", accounts.RoleMember)
	if err == nil {
		t.Fatal("a counter outage admitted an unmetered invitation; the cap must fail closed (#51)")
	}
	if capErr := capError(t, err); !capErr.Unavailable {
		t.Error("Unavailable = false, want true: a backend outage is not the caller's quota running out")
	}
	if len(sender.sent) != 0 {
		t.Fatalf("transport ran %d times, want 0", len(sender.sent))
	}
}

// The refusal is recorded for operators, and the record carries no invited
// address: which address tripped a cap is exactly the fact an audit trail must
// not hand to whoever reads it later.
func TestCreateInvitation_CapRefusalIsAudited(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	sender := &recordingMailer{}
	backend := newMemIncrementer()

	var actions []string
	var details []map[string]string
	svc := accounts.NewService(repo).
		WithInvitationMailer(sender).
		WithInvitationLimits(accounts.InvitationLimits{
			Inviter: limit(backend, 1, time.Hour, "user"),
			Audit: func(_ context.Context, action string, detail map[string]string) {
				actions = append(actions, action)
				details = append(details, detail)
			},
		})

	if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, "first@example.com", accounts.RoleMember); err != nil {
		t.Fatalf("first invitation: %v", err)
	}
	if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, "second@example.com", accounts.RoleMember); err == nil {
		t.Fatal("the invitation past the cap succeeded")
	}

	if len(actions) != 1 {
		t.Fatalf("audit recorded %d events, want 1 (%v)", len(actions), actions)
	}
	if actions[0] != accounts.AuditInvitationRateLimited {
		t.Errorf("action = %q, want %q", actions[0], accounts.AuditInvitationRateLimited)
	}
	for key, value := range details[0] {
		if value == "second@example.com" {
			t.Errorf("audit detail %q carries the invited address", key)
		}
	}
	if details[0]["dimension"] != "inviter" {
		t.Errorf("audit detail dimension = %q, want %q", details[0]["dimension"], "inviter")
	}
}

// The per-address ceiling counts mailboxes, not spellings. Sub-addressing is
// the evasion that makes it decorative otherwise: one attacker turns three a
// day into whatever the inviter cap allows, all of it landing in one inbox.
func TestCreateInvitation_RecipientCapCountsTheMailboxNotTheSpelling(t *testing.T) {
	for _, tc := range []struct{ name, first, second string }{
		{"plus tagging", "victim@example.com", "victim+42@example.com"},
		{"gmail dots", "victim@gmail.com", "v.i.c.t.i.m@gmail.com"},
		{"trailing dot on the domain", "victim@example.com", "victim@example.com."},
		{"casing", "victim@example.com", "VICTIM@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, accountID, viewer := inviteFixture(t)
			sender := &recordingMailer{}
			backend := newMemIncrementer()
			svc := accounts.NewService(repo).
				WithInvitationMailer(sender).
				WithInvitationLimits(accounts.InvitationLimits{
					RecipientBurst: limit(backend, 1, 5*time.Minute, "recipient"),
				})

			if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, tc.first, accounts.RoleMember); err != nil {
				t.Fatalf("first invitation: %v", err)
			}
			if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, tc.second, accounts.RoleMember); err == nil {
				t.Fatalf("%q evaded the cooldown on %q: same inbox, fresh counter", tc.second, tc.first)
			}
			if len(sender.sent) != 1 {
				t.Fatalf("transport ran %d times, want 1", len(sender.sent))
			}
			// Dots are significant on most providers, so folding them
			// everywhere would let one person's cooldown block a stranger.
			if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, "v.i.c.t.i.m@example.com", accounts.RoleMember); err != nil {
				t.Fatalf("a dotted local part on a non-folding domain was treated as the same mailbox: %v", err)
			}
			// A plus inside a quoted local part is literal, so these are two
			// real mailboxes rather than one address and its tag.
			if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, `"quoted+one"@example.com`, accounts.RoleMember); err != nil {
				t.Fatalf(`"quoted+one"@ was refused before its own first invitation: %v`, err)
			}
			if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, `"quoted+two"@example.com`, accounts.RoleMember); err != nil {
				t.Fatalf(`"quoted+two"@ was folded onto "quoted+one"@, which is a different mailbox: %v`, err)
			}
		})
	}
}

// A Service with no limits wired behaves exactly as before. Every existing
// caller and test constructs one that way.
func TestCreateInvitation_NoLimitsConfiguredStillSends(t *testing.T) {
	repo, accountID, viewer := inviteFixture(t)
	sender := &recordingMailer{}
	svc := accounts.NewService(repo).WithInvitationMailer(sender)

	for i := 0; i < 5; i++ {
		if _, err := svc.CreateInvitation(context.Background(), accountID, viewer, fmt.Sprintf("c%d@example.com", i), accounts.RoleMember); err != nil {
			t.Fatalf("invitation %d: %v", i, err)
		}
	}
	if len(sender.sent) != 5 {
		t.Fatalf("transport ran %d times, want 5", len(sender.sent))
	}
}
