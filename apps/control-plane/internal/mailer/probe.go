package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"time"
)

// connect opens one authenticated submission dialogue with the relay and hands
// the caller a client it owns and must Close.
//
// It is the whole of Send's prologue, extracted rather than copied, because the
// probe below has to fail on exactly what a send fails on. A second dialogue
// written alongside this one would drift: it would keep answering "relay fine"
// after a credential expired, or after a relay stopped offering STARTTLS, which
// are two of the three ways this deployment's mail has actually broken. One
// function cannot disagree with itself.
//
// Every stage carries a deadline. A send happens inside a user's request and
// net/smtp on a bare connection has no timeout of its own, so a relay that
// accepts a connection and then stops talking would hang that request until the
// client gave up.
func (s *SMTPSender) connect(ctx context.Context) (*smtp.Client, error) {
	deadline := time.Now().Add(s.cfg.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Deadline: deadline}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("mailer: dial relay: %w", err)
	}
	// Covers every read and write for the rest of the dialogue, including the
	// TLS handshake, so no stage can block past the deadline.
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, fmt.Errorf("mailer: set deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("mailer: greet relay: %w", err)
	}

	starttls, _ := client.Extension("STARTTLS")
	if starttls {
		if err := client.StartTLS(&tls.Config{
			ServerName: s.cfg.Host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			client.Close()
			return nil, fmt.Errorf("mailer: starttls: %w", err)
		}
	} else if s.cfg.Username != "" {
		// Refusing beats downgrading. AUTH over a cleartext session hands the
		// relay login to anything on the path, and a relay that will not offer
		// STARTTLS on the submission port is a relay to fix, not to work around.
		client.Close()
		return nil, errors.New("mailer: relay does not offer STARTTLS and credentials are configured")
	}

	if s.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			client.Close()
			return nil, fmt.Errorf("mailer: authenticate: %w", err)
		}
	}

	return client, nil
}

// Probe reports whether the relay would accept a message right now, without
// sending one.
//
// It exists because of what the /auth/v1/recover gateway route does, and it is
// the only reason that route can do it. To close an account-existence oracle,
// deploy/docker/Caddyfile.supabase answers 200 {} for a 200, a 429 AND a 5xx
// from GoTrue, so a password reset that failed to send is reported to the user
// as one that succeeded. That trade is correct, since the alternative is
// telling anonymous callers which addresses hold accounts, and it leaves a hole
// this fills: with the response identical either way, nothing outside the
// container logs would ever notice that recovery mail had stopped.
//
// GoTrue's relay is this relay. deploy/docker/docker-compose.yml sets
// control-plane's HIVE_SMTP_HOST from ENTERPRISE_SMTP_HOST, which is the same
// variable docker-compose.enterprise.yml gives GOTRUE_SMTP_HOST and the same
// one Alertmanager's smarthost reads, so a verdict here is a verdict about the
// transport every product email shares: password recovery, workspace
// invitations, signup confirmation and email change all stop together.
//
// It deliberately stops short of a send. A probe that delivered a message would
// need a mailbox to deliver to, would cost the account's send quota on every
// interval, and would be one misconfiguration away from mailing a stranger. A
// connect, STARTTLS and AUTH covers the failures that have actually happened
// here (host unresolvable, relay refusing connections, credentials expired) and
// is silent on the one it cannot see: a relay that authenticates and then
// rejects the message itself, which is what mailer.Send's own error path
// reports when a real send hits it.
func (s *SMTPSender) Probe(ctx context.Context) error {
	if !s.cfg.Configured() {
		return ErrNotConfigured
	}
	client, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Quit(); err != nil {
		return fmt.Errorf("mailer: quit: %w", err)
	}
	return nil
}
