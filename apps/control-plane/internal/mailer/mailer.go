// Package mailer is the control plane's outbound email transport.
//
// It exists because there was none. Every "send" in this service was a
// structured log line, which is how workspace invitations came to mint a token,
// discard it, and report success (issue #1440).
//
// The transport is deliberately small: one SMTP dialogue over the standard
// library, no queue, no retry, no template engine. Callers render their own
// message and decide what a failure means. Nothing here is asynchronous, so a
// caller can tell a user what actually happened rather than what was hoped for.
package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// ErrNotConfigured is returned when no SMTP host is configured. It is a normal
// operating state, not a fault: a deployment without a relay still runs, and its
// callers are expected to surface the absence to the user rather than pretend a
// message was delivered.
var ErrNotConfigured = errors.New("mailer: SMTP is not configured")

// ErrInvalidMessage is returned when a message cannot be rendered safely, which
// today means an address or subject carrying a line break. A header value is
// terminated by CRLF, so an unfiltered one lets a caller-supplied address append
// headers of its own, including extra recipients.
var ErrInvalidMessage = errors.New("mailer: invalid message")

// Message is one outbound email. HTML is optional; Text is not, because a
// message with no plain-text alternative is unreadable in a client that refuses
// HTML and scores worse with spam filters.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers a message, or explains why it could not.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// Config describes the relay and the identity mail is sent as.
//
// FromAddress is not the SMTP login and must not be set to it. A relay that
// authenticates a domain rejects a From it has not verified, which is exactly
// the state this deployment was in: mail was sent as an address on a domain
// whose SPF record excluded the relay and which published no DKIM key at all.
type Config struct {
	Host        string
	Port        int
	Username    string
	Password    string
	FromAddress string
	FromName    string
	Timeout     time.Duration
}

// Configured reports whether there is a relay to talk to.
func (c Config) Configured() bool {
	return strings.TrimSpace(c.Host) != "" && strings.TrimSpace(c.FromAddress) != ""
}

// ConfigFromEnv reads the relay settings.
//
// HIVE_MAIL_FROM is the sender identity for every product email and is shared
// with GoTrue's own mailer (deploy/docker/docker-compose.enterprise.yml), so
// auth mail and product mail come from one address on one authenticated domain.
// It is deliberately not ENTERPRISE_SMTP_ADMIN_EMAIL, which is the alert
// RECIPIENT and was already doing two incompatible jobs.
func ConfigFromEnv() Config {
	port, err := strconv.Atoi(strings.TrimSpace(os.Getenv("HIVE_SMTP_PORT")))
	if err != nil || port <= 0 {
		port = 587
	}
	timeout := 15 * time.Second
	if raw := strings.TrimSpace(os.Getenv("HIVE_SMTP_TIMEOUT")); raw != "" {
		if parsed, perr := time.ParseDuration(raw); perr == nil && parsed > 0 {
			timeout = parsed
		}
	}
	return Config{
		Host:        strings.TrimSpace(os.Getenv("HIVE_SMTP_HOST")),
		Port:        port,
		Username:    strings.TrimSpace(os.Getenv("HIVE_SMTP_USER")),
		Password:    os.Getenv("HIVE_SMTP_PASS"),
		FromAddress: strings.TrimSpace(os.Getenv("HIVE_MAIL_FROM")),
		FromName:    strings.TrimSpace(os.Getenv("HIVE_MAIL_FROM_NAME")),
		Timeout:     timeout,
	}
}

// SMTPSender delivers over one SMTP submission dialogue per message.
type SMTPSender struct {
	cfg Config
}

// NewSMTPSender returns a Sender for cfg. It does not dial; a relay that is down
// is a per-message failure, not a startup failure, because refusing to boot over
// it would take the whole control plane down for an email problem.
func NewSMTPSender(cfg Config) *SMTPSender {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.Port <= 0 {
		cfg.Port = 587
	}
	return &SMTPSender{cfg: cfg}
}

// Send delivers msg, or returns why it could not.
//
// Every stage carries a deadline. The send happens inside a user's request, and
// net/smtp on a bare connection has no timeout of its own, so a relay that
// accepts a connection and then stops talking would hang that request until the
// client gave up.
//
// Errors never include the message body. An invitation body carries a
// bearer-equivalent acceptance token, and an error string is the one part of a
// failed send that reliably reaches a log.
func (s *SMTPSender) Send(ctx context.Context, msg Message) error {
	if !s.cfg.Configured() {
		return ErrNotConfigured
	}
	body, err := s.render(msg)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(s.cfg.Timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}

	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Deadline: deadline}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("mailer: dial relay: %w", err)
	}
	// Covers every read and write for the rest of the dialogue, including the
	// TLS handshake, so no stage can block past the deadline.
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return fmt.Errorf("mailer: set deadline: %w", err)
	}

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mailer: greet relay: %w", err)
	}
	defer client.Close()

	starttls, _ := client.Extension("STARTTLS")
	if starttls {
		if err := client.StartTLS(&tls.Config{
			ServerName: s.cfg.Host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			return fmt.Errorf("mailer: starttls: %w", err)
		}
	} else if s.cfg.Username != "" {
		// Refusing beats downgrading. AUTH over a cleartext session hands the
		// relay login to anything on the path, and a relay that will not offer
		// STARTTLS on the submission port is a relay to fix, not to work around.
		return errors.New("mailer: relay does not offer STARTTLS and credentials are configured")
	}

	if s.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("mailer: authenticate: %w", err)
		}
	}

	if err := client.Mail(s.cfg.FromAddress); err != nil {
		return fmt.Errorf("mailer: sender refused: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("mailer: recipient refused: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer: open data: %w", err)
	}
	if _, err := writer.Write(body); err != nil {
		writer.Close()
		return fmt.Errorf("mailer: write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		// The relay's verdict on the message itself lands here. A Brevo account
		// that has not been activated refuses at exactly this point, after
		// accepting the envelope: "502 5.7.0 Your SMTP account is not yet
		// activated".
		return fmt.Errorf("mailer: relay refused the message: %w", err)
	}
	if err := client.Quit(); err != nil {
		// The message is already accepted by this point, so a botched QUIT is
		// not a delivery failure and must not be reported as one.
		return nil
	}
	return nil
}

// headerSafe rejects the values that could forge headers. A header line ends at
// a CRLF, so a recipient address or subject containing one appends headers of
// its own. Both values here originate in user input.
func headerSafe(value string) bool {
	return !strings.ContainsAny(value, "\r\n")
}

// render builds a multipart/alternative message. Plain text first, HTML second:
// a client picks the last part it can display, so this order gives HTML to
// clients that render it and text to clients that do not.
func (s *SMTPSender) render(msg Message) ([]byte, error) {
	to := strings.TrimSpace(msg.To)
	if to == "" || !headerSafe(to) || !headerSafe(msg.Subject) {
		return nil, ErrInvalidMessage
	}
	if _, err := mail.ParseAddress(to); err != nil {
		return nil, fmt.Errorf("%w: recipient address", ErrInvalidMessage)
	}
	if strings.TrimSpace(msg.Text) == "" {
		return nil, fmt.Errorf("%w: a plain-text part is required", ErrInvalidMessage)
	}

	from := (&mail.Address{Name: s.cfg.FromName, Address: s.cfg.FromAddress}).String()

	// A fixed boundary would be a bug if a body ever contained it. This one is
	// derived from the process clock and the recipient, and the bodies here are
	// rendered by this repository rather than accepted from a user.
	boundary := fmt.Sprintf("hive-%d-%d", time.Now().UnixNano(), len(to))

	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", msg.Subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	// Transactional mail, so an auto-responder must not reply to it and a bulk
	// filter should not treat it as a campaign.
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	if msg.HTML == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(normalizeCRLF(msg.Text))
		return []byte(b.String()), nil
	}
	b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	b.WriteString(normalizeCRLF(msg.Text))
	b.WriteString("\r\n--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
	b.WriteString(normalizeCRLF(msg.HTML))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return []byte(b.String()), nil
}

// normalizeCRLF converts bare newlines to CRLF. SMTP line endings are CRLF, and
// a bare LF in the body is what makes a message arrive as one run-on line in
// some clients and get rejected outright by strict relays.
func normalizeCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}
