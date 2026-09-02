package mailer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
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
// The day window is the one that decides whether the relay survives. Per-caller
// caps aggregate, so an hourly ceiling alone permits its own rate every hour:
// 100 an hour is 2400 a day. Relay accounts meter by the day, not the hour
// (Brevo's free tier is 300 a day and its entry paid tier is near 660), and
// these credentials are shared with GoTrue, which adds its own auth mail from
// the same sender. So the daily default is 250: it sits under the smallest
// allowance this deployment could be on, which makes it safe on every plan
// without knowing which one, and leaves the rest of that allowance for the
// account verification, one-time codes, invoices and balance alerts that fail
// with the relay if it is exhausted.
//
// This is one of two ceilings on that account, not the only one: GoTrue emits
// auth mail from the same credentials, bounded by its own
// GOTRUE_RATE_LIMIT_EMAIL_SENT. Neither number means anything on its own, which
// is why Budget adds them up at boot and says whether the total fits. Read this
// 250 as "the invitation half of the allowance", never as "the relay is safe".
//
// Both are environment variables rather than constants precisely because the
// safe default is the pessimistic one: raise them once the plan's real daily
// allowance is confirmed.
const (
	DefaultRelayCapPerHour = 100
	DefaultRelayCapPerDay  = 250

	RelayCapWindow    = time.Hour
	RelayCapDayWindow = 24 * time.Hour
)

// RelayCaps is how much mail this process may emit in total.
type RelayCaps struct {
	PerHour int
	PerDay  int
}

// RelayCapsFromEnv reads HIVE_MAIL_RELAY_CAP_PER_HOUR and
// HIVE_MAIL_RELAY_CAP_PER_DAY. An unset, unparseable or non-positive value
// keeps the default rather than disabling the ceiling: this is the backstop
// that protects a shared relay account, so a typo must not switch it off.
func RelayCapsFromEnv() RelayCaps {
	return RelayCaps{
		PerHour: capFromEnv("HIVE_MAIL_RELAY_CAP_PER_HOUR", DefaultRelayCapPerHour),
		PerDay:  capFromEnv("HIVE_MAIL_RELAY_CAP_PER_DAY", DefaultRelayCapPerDay),
	}
}

func capFromEnv(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

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
// RelayCeiling is one window of the ceiling. Window labels it in the log line
// an operator reads when a send is refused.
type RelayCeiling struct {
	Window string
	Allow  AllowFunc
}

type ThrottledSender struct {
	inner    Sender
	ceilings []RelayCeiling
}

// NewThrottledSender returns inner wrapped in the transport-wide ceiling. Each
// ceiling is one window over the same subject, and every one of them has to
// admit the send. No ceilings (or only empty ones) yields a transparent
// pass-through, which is what an unlimited transport was before this existed.
func NewThrottledSender(inner Sender, ceilings ...RelayCeiling) *ThrottledSender {
	return &ThrottledSender{inner: inner, ceilings: ceilings}
}

// Send refuses before dialling anything when the ceiling is reached.
func (t *ThrottledSender) Send(ctx context.Context, msg Message) error {
	for _, ceiling := range t.ceilings {
		if ceiling.Allow == nil {
			continue
		}
		// One subject, because the ceiling counts this process's whole output.
		// A per-recipient or per-caller key belongs to whoever knows those
		// values, and the invitation path already carries both.
		if err := ceiling.Allow(ctx, "relay"); err != nil {
			// Loud, because this is exhaustion of a resource every other
			// product email shares. Whatever is emitting this volume is also
			// about to break account verification and one-time codes.
			log.Printf("WARN mailer: relay send cap reached (%s window), message not sent: %v",
				ceiling.Window, err)
			return fmt.Errorf("%w (%s window): %v", ErrThrottled, ceiling.Window, err)
		}
	}
	return t.inner.Send(ctx, msg)
}
