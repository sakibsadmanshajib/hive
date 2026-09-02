package mailer

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrThrottled is returned when a send is refused by the transport-wide
// ceiling. It is a refusal, never a silent drop: the caller is told the message
// did not go out so it can report that rather than a delivery it never made.
var ErrThrottled = errors.New("mailer: send cap reached")

// The transport-wide ceiling, in two windows.
//
// It sits above every per-caller cap that exists (a single inviter is bounded
// at 30 an hour, a workspace at 200 a day), so reaching it means either several
// workspaces onboarding at once or a caller with no cap of its own, and
// stopping is right in both cases.
//
// The day window is not redundant with the hour: per-caller caps aggregate, so
// four inviters each comfortably inside 30 an hour reach 120, and an hourly
// ceiling alone would permit that rate all day. A relay account's allowance is
// quoted per day, so the day is the window that has to hold.
//
// Constants rather than environment variables: an abuse ceiling with a runtime
// knob is an abuse ceiling with a runtime off switch.
const (
	RelayCapPerWindow = 100
	RelayCapWindow    = time.Hour
	RelayCapPerDay    = 300
	RelayCapDayWindow = 24 * time.Hour
)

// AllowFunc records one attempt against subject and reports whether it is
// within quota. signupguard.RateLimiter.Allow satisfies it; a nil AllowFunc
// disables the ceiling.
type AllowFunc func(ctx context.Context, subject string) error

// ThrottledSender wraps a Sender with a ceiling on how much mail this process
// may emit, counted across every caller.
//
// The dimensions that actually bind an abuser (which account, which tenant,
// which recipient) live where those values exist, which is the caller. This
// layer knows none of them, and deliberately caps the one thing it does know:
// the relay itself. That matters because the relay is the shared asset. A
// suspended sending domain takes account verification, one-time codes, invoices
// and balance alerts down for every customer, so the boundary every send routes
// through holds a ceiling even for the caller nobody has written yet.
//
// The ceiling sits above every legitimate per-caller cap on purpose. It is a
// backstop, not the primary control: a deployment that hits it has either grown
// past its configuration or is being abused through a path with no cap of its
// own, and both are worth stopping.
type ThrottledSender struct {
	inner Sender
	allow []AllowFunc
}

// NewThrottledSender returns inner wrapped in the transport-wide ceiling. Each
// allow is one window over the same subject, and every one of them has to admit
// the send. No allow (or only nil ones) yields a transparent pass-through,
// which is what an unlimited transport was before this existed.
func NewThrottledSender(inner Sender, allow ...AllowFunc) *ThrottledSender {
	return &ThrottledSender{inner: inner, allow: allow}
}

// Send refuses before dialling anything when the ceiling is reached.
func (t *ThrottledSender) Send(ctx context.Context, msg Message) error {
	for _, allow := range t.allow {
		if allow == nil {
			continue
		}
		// One subject, because the ceiling counts this process's whole output.
		// A per-recipient or per-caller key belongs to whoever knows those
		// values, and the invitation path already carries both.
		if err := allow(ctx, "relay"); err != nil {
			return fmt.Errorf("%w: %v", ErrThrottled, err)
		}
	}
	return t.inner.Send(ctx, msg)
}
