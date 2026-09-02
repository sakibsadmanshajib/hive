package mailer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/mailer"
)

type countingSender struct {
	calls int
}

func (s *countingSender) Send(context.Context, mailer.Message) error {
	s.calls++
	return nil
}

// The relay ceiling exists for the caller nobody has written yet. The
// invitation path carries its own per-inviter and per-tenant caps, but a future
// caller of this transport inherits none of them, so the boundary every send
// routes through holds a ceiling of its own.
func TestThrottledSenderRefusesWithoutReachingTheTransport(t *testing.T) {
	inner := &countingSender{}
	budget := 2
	sender := mailer.NewThrottledSender(inner, mailer.RelayCeiling{
		Window: "hour",
		Allow: func(context.Context, string) error {
			if budget <= 0 {
				return errors.New("over quota")
			}
			budget--
			return nil
		},
	})

	msg := mailer.Message{To: "someone@example.com", Subject: "hi", Text: "hi"}
	for i := 0; i < 2; i++ {
		if err := sender.Send(context.Background(), msg); err != nil {
			t.Fatalf("send %d inside the ceiling: %v", i, err)
		}
	}

	err := sender.Send(context.Background(), msg)
	if !errors.Is(err, mailer.ErrThrottled) {
		t.Fatalf("err = %v, want ErrThrottled", err)
	}
	if inner.calls != 2 {
		t.Fatalf("transport ran %d times, want 2", inner.calls)
	}
}

// Every window has to admit the send, not just the first. Without this a day
// ceiling added beside an hour ceiling would be decorative: the hourly limiter
// answers first and its verdict would stand for both.
func TestThrottledSenderConsultsEveryWindow(t *testing.T) {
	inner := &countingSender{}
	hourly := mailer.RelayCeiling{Window: "hour", Allow: func(context.Context, string) error { return nil }}
	daily := mailer.RelayCeiling{
		Window: "day",
		Allow:  func(context.Context, string) error { return errors.New("day quota exhausted") },
	}
	sender := mailer.NewThrottledSender(inner, hourly, daily)

	err := sender.Send(context.Background(), mailer.Message{To: "a@example.com", Text: "x"})
	if !errors.Is(err, mailer.ErrThrottled) {
		t.Fatalf("err = %v, want ErrThrottled from the second window", err)
	}
	if inner.calls != 0 {
		t.Fatalf("transport ran %d times, want 0", inner.calls)
	}
}

// The day window is the one that decides whether a shared relay account
// survives, so it must have a default even when nothing sets it.
func TestRelayCapsFromEnvDefaultsToTheDailySafeNumber(t *testing.T) {
	t.Setenv("HIVE_MAIL_RELAY_CAP_PER_HOUR", "")
	t.Setenv("HIVE_MAIL_RELAY_CAP_PER_DAY", "not a number")
	caps := mailer.RelayCapsFromEnv()
	if caps.PerHour != mailer.DefaultRelayCapPerHour || caps.PerDay != mailer.DefaultRelayCapPerDay {
		t.Fatalf("caps = %+v, want the defaults (%d/hour, %d/day)",
			caps, mailer.DefaultRelayCapPerHour, mailer.DefaultRelayCapPerDay)
	}
	t.Setenv("HIVE_MAIL_RELAY_CAP_PER_DAY", "600")
	if caps := mailer.RelayCapsFromEnv(); caps.PerDay != 600 {
		t.Fatalf("PerDay = %d, want the configured 600", caps.PerDay)
	}
}

// A ceiling with no limiter is a transport with no ceiling, which is what every
// existing construction of a plain sender is.
func TestThrottledSenderWithoutLimiterIsATransparentPassThrough(t *testing.T) {
	inner := &countingSender{}
	sender := mailer.NewThrottledSender(inner)
	if err := sender.Send(context.Background(), mailer.Message{To: "a@example.com", Text: "x"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("transport ran %d times, want 1", inner.calls)
	}
}
