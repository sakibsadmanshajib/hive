package mailer_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/mailer"
)

func TestProbe_UnconfiguredReportsNotConfigured(t *testing.T) {
	sender := mailer.NewSMTPSender(mailer.Config{})
	if err := sender.Probe(context.Background()); !errors.Is(err, mailer.ErrNotConfigured) {
		t.Fatalf("Probe() = %v, want mailer.ErrNotConfigured", err)
	}
}

// A healthy relay is reported healthy, and nothing is mailed to anybody.
//
// The second half is the invariant that matters. This probe runs on a timer for
// the life of the process, so a version of it that delivered a message would
// mail whatever address it was pointed at every ten minutes, forever.
func TestProbe_AcceptsAHealthyRelayWithoutSendingAnything(t *testing.T) {
	captured := make(chan string, 1)
	host, port := splitHostPort(t, startFakeRelay(t, captured))
	sender := mailer.NewSMTPSender(mailer.Config{
		Host:        host,
		Port:        port,
		FromAddress: "no_reply@test.invalid",
		Timeout:     5 * time.Second,
	})

	if err := sender.Probe(context.Background()); err != nil {
		t.Fatalf("Probe() = %v, want nil", err)
	}

	select {
	case body := <-captured:
		t.Fatalf("the probe delivered a message; it must never send one. body: %q", body)
	case <-time.After(200 * time.Millisecond):
	}
}

// A relay that is not listening is a verdict, not a hang. This is the state the
// gauge exists to make visible: with the relay down, POST /auth/v1/recover
// still answers 200 {} and the user is told to check an inbox nothing will
// arrive in.
func TestProbe_ReportsARelayThatRefusesConnections(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	host, port := splitHostPort(t, listener.Addr().String())
	// Closed before the probe runs, so the port is dead but was real.
	listener.Close()

	sender := mailer.NewSMTPSender(mailer.Config{
		Host:        host,
		Port:        port,
		FromAddress: "no_reply@test.invalid",
		Timeout:     2 * time.Second,
	})
	err = sender.Probe(context.Background())
	if err == nil {
		t.Fatal("Probe() = nil, want an error for a relay that is not listening")
	}
	if !strings.Contains(err.Error(), "dial relay") {
		t.Fatalf("Probe() = %v, want the error to name the dial", err)
	}
}

// The probe fails on exactly what a send fails on, because both go through
// connect. A relay that stops offering STARTTLS while credentials are
// configured is refused rather than downgraded to a cleartext AUTH, and the
// probe reports that as unusable rather than as healthy.
func TestProbe_RefusesARelayThatDropsSTARTTLSWithCredentialsSet(t *testing.T) {
	captured := make(chan string, 1)
	host, port := splitHostPort(t, startFakeRelay(t, captured))
	sender := mailer.NewSMTPSender(mailer.Config{
		Host:        host,
		Port:        port,
		Username:    "relay-login",
		Password:    "relay-secret",
		FromAddress: "no_reply@test.invalid",
		Timeout:     5 * time.Second,
	})

	err := sender.Probe(context.Background())
	if err == nil {
		t.Fatal("Probe() = nil, want a refusal when STARTTLS is absent and credentials are set")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("Probe() = %v, want the error to name STARTTLS", err)
	}
	if strings.Contains(err.Error(), "relay-secret") {
		t.Fatalf("Probe() leaked the relay password into its error: %v", err)
	}
}
