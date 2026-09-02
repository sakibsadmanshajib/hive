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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/mail"
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
	// Validate the recipient once, here, and use the validated value for both
	// the envelope and the header. Validating inside render and then passing
	// msg.To straight to client.Rcpt would leave two paths for one value, and
	// only one of them checked. CodeQL flagged exactly that shape.
	to, err := validRecipient(msg.To)
	if err != nil {
		return err
	}
	body, err := s.render(msg, to)
	if err != nil {
		return err
	}

	client, err := s.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Mail(s.cfg.FromAddress); err != nil {
		return fmt.Errorf("mailer: sender refused: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
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

// validRecipient returns the address to use in both the envelope and the To
// header, or refuses.
//
// A header line ends at a CRLF, so an address carrying one appends headers of
// its own, including a second Bcc. The same value is also written into the SMTP
// RCPT command, where a CRLF injects a command. This address originates in user
// input, so both are live concerns rather than theoretical ones.
//
// Rejecting CRLF is not on its own enough, which is the second thing CodeQL was
// right about. mail.ParseAddress accepts far more than an addr-spec and hands
// back a value that is not what was parsed: it strips the quotes from a quoted
// local part, so `"a,b"@example.test` comes back as the bare a,b@example.test,
// and a display name is dropped entirely, so `Bob <bob@example.test>` comes back
// as somebody else's address. Written unquoted into a To header, the first is
// two mailboxes rather than one, which is content spoofing of the header a
// recipient reads to decide whether the message was meant for them; written into
// RCPT TO it is not a valid envelope either.
//
// So the parse has to round-trip: the address is accepted only when the
// canonical form the parser produces is exactly the string that was asked for.
// Everything this file then writes is a plain addr-spec, which is why the To
// header and the RCPT command can concatenate it with no re-quoting at either
// site. An address that needs quoting is refused rather than rewritten, because
// the invitation it belongs to is matched on accept against a verified GoTrue
// address (accounts.AcceptInvitation), which is never a quoted or display-name
// form, so a rewritten one could not have been redeemed anyway.
//
// Equality also subsumes the old headerSafe check on the parsed value: `to` has
// already been checked, and the two are now required to be identical.
func validRecipient(raw string) (string, error) {
	to := strings.TrimSpace(raw)
	if to == "" || !headerSafe(to) {
		return "", fmt.Errorf("%w: recipient address", ErrInvalidMessage)
	}
	parsed, err := mail.ParseAddress(to)
	if err != nil {
		return "", fmt.Errorf("%w: recipient address", ErrInvalidMessage)
	}
	if parsed.Address != to {
		return "", fmt.Errorf("%w: recipient address", ErrInvalidMessage)
	}
	return to, nil
}

// render builds a multipart/alternative message. Plain text first, HTML second:
// a client picks the last part it can display, so this order gives HTML to
// clients that render it and text to clients that do not.
//
// to must already have come through validRecipient. The To header below
// concatenates it unquoted, which is only correct because that function refuses
// anything whose canonical form differs from what it was given.
func (s *SMTPSender) render(msg Message, to string) ([]byte, error) {
	if !headerSafe(msg.Subject) {
		return nil, fmt.Errorf("%w: subject", ErrInvalidMessage)
	}
	if strings.TrimSpace(msg.Text) == "" {
		return nil, fmt.Errorf("%w: a plain-text part is required", ErrInvalidMessage)
	}

	from := (&mail.Address{Name: s.cfg.FromName, Address: s.cfg.FromAddress}).String()

	// A boundary that appears anywhere in a body truncates the message at that
	// point, and both parts carry user-influenced text (a workspace name, an
	// inviter address). Random rather than derived from the clock, so it cannot
	// be predicted and planted, and then checked against what was actually
	// rendered, so the guarantee is structural rather than probabilistic.
	boundary, err := randomBoundary()
	if err != nil {
		return nil, err
	}
	if strings.Contains(msg.Text, boundary) || strings.Contains(msg.HTML, boundary) {
		return nil, fmt.Errorf("%w: body collides with the MIME boundary", ErrInvalidMessage)
	}

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

// randomBoundary returns a MIME boundary with 128 bits of entropy.
func randomBoundary() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("mailer: generate boundary: %w", err)
	}
	return "hive-" + hex.EncodeToString(raw[:]), nil
}

// normalizeCRLF converts bare newlines to CRLF. SMTP line endings are CRLF, and
// a bare LF in the body is what makes a message arrive as one run-on line in
// some clients and get rejected outright by strict relays.
func normalizeCRLF(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}
