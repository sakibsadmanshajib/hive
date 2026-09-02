package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/signupguard"
)

// The invitation send cap (issue #1745).
//
// An invitation is the only message this product sends to an address the caller
// types, which makes it the one path that can reach anybody at all. Password
// recovery can only reach an address that already holds an account; this one
// reaches wherever it is pointed. Uncapped, an authenticated account is an open
// relay, and an open relay is what got the sending domain suspended once
// already.
//
// One dimension is not a cap, it is a speed bump, because each one is evaded by
// varying the value it counts:
//
//   - per inviter, evaded by holding two accounts;
//   - per tenant, evaded by inviting from two workspaces;
//   - per recipient, evaded by inviting a thousand different people.
//
// Together they leave no free direction: an attacker must vary account, tenant
// and target at once, and each of those is separately bounded.
//
// The numbers are constants rather than environment variables. Every one of
// them is a ceiling far above real product usage, none of them is deployment
// specific, and an abuse control with a runtime knob is an abuse control with a
// runtime off switch.
//
// Read each number as a rate, not as a hard maximum over any interval you
// choose. signupguard.RateLimiter buckets by now/window, a fixed window rather
// than a sliding one, so a caller who spends a full budget at the end of one
// bucket and another at the start of the next gets up to twice the stated
// number across that boundary. Inherited behaviour, tolerable on ceilings with
// this much headroom, and worth knowing before sizing these against a relay
// allowance.
const (
	// A department onboarding burst is the legitimate shape this has to
	// survive: an administrator adding a team in one sitting. Thirty in an
	// hour clears that, and a genuinely larger rollout continues into the next
	// hour rather than failing.
	//
	// It is not related to GOTRUE_RATE_LIMIT_EMAIL_SENT. Invitation mail goes
	// straight from this service to the relay and never touches GoTrue, so the
	// two counters are independent and only happen to share a number. What the
	// two paths do share is the relay account itself, which is what the
	// transport ceiling in mailer/throttle.go bounds.
	InviteCapPerInviter       = 30
	InviteCapPerInviterWindow = time.Hour

	// Per workspace, over a day. Two hundred covers a two-hundred-seat rollout
	// landing in a single day, which is past every tenant this product has, and
	// bounds one workspace well under a relay's daily allowance.
	InviteCapPerTenant       = 200
	InviteCapPerTenantWindow = 24 * time.Hour

	// Per target address, deployment wide. The first is a cooldown: re-inviting
	// somebody is normal (they lost the mail), so a resend has to work, but not
	// instantly and not in a loop. The second is the day ceiling on how much
	// mail one person can be made to receive from this system no matter how
	// many workspaces point at them.
	InviteCapPerRecipientBurst       = 1
	InviteCapPerRecipientBurstWindow = 5 * time.Minute
	InviteCapPerRecipientDaily       = 3
	InviteCapPerRecipientDailyWindow = 24 * time.Hour
)

// AuditInvitationRateLimited is the audit action recorded when the cap refuses
// an invitation.
const AuditInvitationRateLimited = "invitation.rate_limited"

// AuditFunc records an invitation-cap decision for operators. The detail map
// carries classification strings and ids only, never the invited address: an
// audit trail that names who was invited hands the reader the address list the
// cap exists to stop being built. Optional; nil disables auditing.
type AuditFunc func(ctx context.Context, action string, detail map[string]string)

// InvitationLimit is one dimension of the cap. Allow records an attempt against
// a subject and reports whether it is within quota (signupguard.RateLimiter
// satisfies it). Window is that limiter's window, used to tell the caller when
// the refusal lifts. A zero Allow disables the dimension.
type InvitationLimit struct {
	Allow  func(ctx context.Context, subject string) error
	Window time.Duration
}

// InvitationLimits is the set of dimensions checked before an invitation is
// stored or mailed. The zero value caps nothing, which is what every caller
// that never wires it (and every existing test) gets.
type InvitationLimits struct {
	Inviter        InvitationLimit // subject: inviting user id
	Tenant         InvitationLimit // subject: workspace account id
	RecipientBurst InvitationLimit // subject: hashed target address, cooldown
	RecipientDaily InvitationLimit // subject: hashed target address, day ceiling
	Audit          AuditFunc
}

// InvitationCapError reports an invitation refused before anything was stored,
// rendered or sent.
type InvitationCapError struct {
	// Dimension names the cap that tripped. It is for logs and the audit trail
	// only and is never sent to the caller: a per-recipient refusal would
	// otherwise tell whoever asked that somebody, somewhere, recently invited
	// that address.
	Dimension string
	// RetryAfter is how long until the window that refused this rolls over.
	RetryAfter time.Duration
	// Unavailable distinguishes a counter that could not be reached from a
	// quota that ran out. Both refuse (the #51 policy is fail closed), but they
	// are not the same thing and the caller is not told they are.
	Unavailable bool
}

func (e *InvitationCapError) Error() string {
	if e.Unavailable {
		return fmt.Sprintf("accounts: invitation cap unavailable (%s)", e.Dimension)
	}
	return fmt.Sprintf("accounts: invitation cap reached (%s, retry after %s)", e.Dimension, e.RetryAfter)
}

// AsInvitationCapError is a helper for errors.As with InvitationCapError.
func AsInvitationCapError(err error, target **InvitationCapError) bool {
	return errors.As(err, target)
}

// check runs every configured dimension and returns the first refusal.
//
// A refused attempt still counts against the dimensions checked before it. That
// is deliberate: a loop should burn its own budget, not probe for free.
func (l InvitationLimits) check(ctx context.Context, accountID uuid.UUID, viewer auth.Viewer, email string) error {
	recipient := hashRecipient(email)
	dimensions := []struct {
		name    string
		limit   InvitationLimit
		subject string
	}{
		{"inviter", l.Inviter, viewer.UserID.String()},
		{"tenant", l.Tenant, accountID.String()},
		{"recipient_burst", l.RecipientBurst, recipient},
		{"recipient_daily", l.RecipientDaily, recipient},
	}

	for _, d := range dimensions {
		if d.limit.Allow == nil {
			continue
		}
		err := d.limit.Allow(ctx, d.subject)
		if err == nil {
			continue
		}
		capErr := &InvitationCapError{
			Dimension:   d.name,
			RetryAfter:  signupguard.RetryAfter(d.limit.Window, time.Now()),
			Unavailable: !errors.Is(err, signupguard.ErrRateLimited),
		}
		l.audit(ctx, accountID, viewer, capErr)
		return capErr
	}
	return nil
}

func (l InvitationLimits) audit(ctx context.Context, accountID uuid.UUID, viewer auth.Viewer, capErr *InvitationCapError) {
	if l.Audit == nil {
		return
	}
	reason := "over_quota"
	if capErr.Unavailable {
		reason = "counter_unavailable"
	}
	l.Audit(ctx, AuditInvitationRateLimited, map[string]string{
		"dimension":  capErr.Dimension,
		"reason":     reason,
		"account_id": accountID.String(),
		"user_id":    viewer.UserID.String(),
	})
}

// hashRecipient is the subject for the per-address dimensions. Hashed because
// the counter is a shared cache: an address written into a Redis key is a list
// of who this deployment has been asked to mail, sitting in a store nothing
// else about invitations uses. Not a password hash, whatever a scanner makes of
// SHA-256 over an address: it is a key for a counter, and the input is a value
// the caller supplied and already knows.
//
// This normalises the counter key only. What is stored and what is delivered
// stay exactly as the inviter typed them, so the address on the invitation row
// is still the address the recipient sees.
func hashRecipient(email string) string {
	sum := sha256.Sum256([]byte(normalizeMailbox(email)))
	return hex.EncodeToString(sum[:16])
}

// dotFoldingDomains ignore dots in the local part, so every dotting of a name
// is one mailbox. Provider specific and deliberately a short list: dots are
// significant almost everywhere else, and folding them there would let one
// person's cooldown block a different, real mailbox.
var dotFoldingDomains = map[string]bool{
	"gmail.com":      true,
	"googlemail.com": true,
}

// normalizeMailbox reduces an address to the mailbox it actually reaches, so
// the per-address ceiling counts people rather than spellings.
//
// Without this the ceiling is decorative: victim+1@ through victim+200@ are two
// hundred distinct counters and one human inbox, which is exactly the volume
// this cap exists to keep off one person.
func normalizeMailbox(email string) string {
	address := strings.ToLower(strings.TrimSpace(email))
	at := strings.LastIndexByte(address, '@')
	if at < 0 {
		return address
	}
	local, domain := address[:at], address[at+1:]
	// A trailing dot is the same domain, and most relays deliver it.
	domain = strings.TrimSuffix(domain, ".")
	// Sub-addressing: everything from the first plus to the at sign is a label
	// the recipient's provider strips before delivery.
	if plus := strings.IndexByte(local, '+'); plus >= 0 {
		local = local[:plus]
	}
	if dotFoldingDomains[domain] {
		local = strings.ReplaceAll(local, ".", "")
	}
	return local + "@" + domain
}
