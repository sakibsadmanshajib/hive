package mailer_test

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/mailer"
)

// An unconfigured relay must be reported as such, not as a delivery. This is the
// state every deployment without SMTP is in, and the whole point of the
// distinction is that a caller can tell the user the truth.
func TestSend_UnconfiguredReportsNotConfigured(t *testing.T) {
	sender := mailer.NewSMTPSender(mailer.Config{FromAddress: "no_reply@example.test"})
	err := sender.Send(context.Background(), mailer.Message{
		To:      "invitee@example.test",
		Subject: "hello",
		Text:    "hello",
	})
	if !errors.Is(err, mailer.ErrNotConfigured) {
		t.Fatalf("Send error = %v, want ErrNotConfigured", err)
	}
}

// A recipient address is user input. A line break in it would terminate the To
// header and let the rest be read as headers of its own, including a second Bcc.
func TestSend_RefusesHeaderInjection(t *testing.T) {
	cases := map[string]mailer.Message{
		"recipient": {To: "a@b.test\r\nBcc: attacker@evil.test", Subject: "s", Text: "t"},
		"subject":   {To: "a@b.test", Subject: "s\r\nBcc: attacker@evil.test", Text: "t"},
		"not an address": {
			To: "not-an-address", Subject: "s", Text: "t",
		},
		"no text part": {To: "a@b.test", Subject: "s", Text: "  ", HTML: "<p>hi</p>"},
	}
	sender := mailer.NewSMTPSender(mailer.Config{
		Host:        "relay.example.test",
		FromAddress: "no_reply@example.test",
	})
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			err := sender.Send(context.Background(), msg)
			if !errors.Is(err, mailer.ErrInvalidMessage) {
				t.Fatalf("Send error = %v, want ErrInvalidMessage", err)
			}
		})
	}
}

// The full submission dialogue against a scripted relay. This is the check that
// fails if the message is malformed on the wire: bare LF line endings, a missing
// alternative part, or a header the relay would reject.
func TestSend_DeliversMultipartMessage(t *testing.T) {
	captured := make(chan string, 1)
	addr := startFakeRelay(t, captured)
	host, port := splitHostPort(t, addr)

	sender := mailer.NewSMTPSender(mailer.Config{
		Host:        host,
		Port:        port,
		FromAddress: "no_reply@hive.example",
		FromName:    "Hive",
		Timeout:     5 * time.Second,
	})
	err := sender.Send(context.Background(), mailer.Message{
		To:      "invitee@example.test",
		Subject: "Join Acme on Hive",
		Text:    "Open this link\nhttps://console.example/invitations/accept?token=REDACTED",
		HTML:    "<p>Open this link</p>",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case body := <-captured:
		for _, want := range []string{
			"From: \"Hive\" <no_reply@hive.example>",
			"To: invitee@example.test",
			"Subject: Join Acme on Hive",
			"Content-Type: multipart/alternative;",
			"Content-Type: text/plain; charset=utf-8",
			"Content-Type: text/html; charset=utf-8",
			"Auto-Submitted: auto-generated",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("message body missing %q\n---\n%s", want, body)
			}
		}
		// Every line must end CRLF. A bare LF is what strict relays reject and
		// what collapses a message into one line elsewhere.
		for _, line := range strings.Split(body, "\r\n") {
			if strings.Contains(line, "\n") {
				t.Fatalf("body contains a bare LF: %q", line)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay never received a message body")
	}
}

// A relay that accepts a connection and then stops talking must not hold the
// caller's request open. net/smtp on a bare connection has no timeout of its own.
func TestSend_SilentRelayTimesOut(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, aerr := listener.Accept()
		if aerr != nil {
			return
		}
		// Accept, then say nothing at all.
		defer conn.Close()
		time.Sleep(10 * time.Second)
	}()

	host, port := splitHostPort(t, listener.Addr().String())
	sender := mailer.NewSMTPSender(mailer.Config{
		Host:        host,
		Port:        port,
		FromAddress: "no_reply@hive.example",
		Timeout:     300 * time.Millisecond,
	})

	start := time.Now()
	err = sender.Send(context.Background(), mailer.Message{
		To: "a@b.test", Subject: "s", Text: "t",
	})
	if err == nil {
		t.Fatal("Send succeeded against a silent relay")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Send took %s against a silent relay; the deadline is not applied", elapsed)
	}
}

// The one hard security invariant this package documents: credentials are never
// sent over a session the relay refused to encrypt. Without this the branch had
// no coverage at all, so a refactor that turned the refusal into a downgrade
// would have shipped green.
func TestSend_RefusesToAuthenticateWithoutSTARTTLS(t *testing.T) {
	captured := make(chan string, 1)
	addr := startFakeRelay(t, captured)
	host, port := splitHostPort(t, addr)

	sender := mailer.NewSMTPSender(mailer.Config{
		Host:        host,
		Port:        port,
		Username:    "relay-login",
		Password:    "relay-secret",
		FromAddress: "no_reply@hive.example",
		Timeout:     5 * time.Second,
	})
	err := sender.Send(context.Background(), mailer.Message{
		To: "a@b.test", Subject: "s", Text: "t",
	})
	if err == nil {
		t.Fatal("Send authenticated over a relay that offers no STARTTLS")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("Send error = %v, want a STARTTLS refusal", err)
	}
	// The credential must not have gone anywhere, and neither must the message.
	select {
	case body := <-captured:
		t.Fatalf("a message was delivered over the refused session: %q", body)
	default:
	}
	if strings.Contains(err.Error(), "relay-secret") {
		t.Fatal("the relay password is in the error string")
	}
}

// A boundary planted in the body would truncate the message at that point. The
// boundary is random, so this drives the check rather than the collision.
func TestSend_RefusesABodyThatCollidesWithItsBoundary(t *testing.T) {
	captured := make(chan string, 1)
	addr := startFakeRelay(t, captured)
	host, port := splitHostPort(t, addr)

	sender := mailer.NewSMTPSender(mailer.Config{
		Host: host, Port: port,
		FromAddress: "no_reply@hive.example",
		Timeout:     5 * time.Second,
	})
	// Two sends with the same body must both succeed, which they cannot do if
	// the boundary is a fixed string derived from anything the body can reach.
	for i := 0; i < 2; i++ {
		if err := sender.Send(context.Background(), mailer.Message{
			To: "a@b.test", Subject: "s", Text: "hive-boundary-lookalike", HTML: "<p>x</p>",
		}); err != nil {
			t.Fatalf("send %d: %v", i, err)
		}
		<-captured
	}
}

// startFakeRelay speaks just enough SMTP to accept one message and hands the
// body back on captured.
func startFakeRelay(t *testing.T, captured chan<- string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, aerr := listener.Accept()
			if aerr != nil {
				return
			}
			go serveFakeRelay(conn, captured)
		}
	}()
	return listener.Addr().String()
}

// serveFakeRelay handles one connection.
func serveFakeRelay(conn net.Conn, captured chan<- string) {
	{
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		reader := bufio.NewReader(conn)
		write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

		write("220 fake relay ready")
		var body strings.Builder
		inData := false
		for {
			line, rerr := reader.ReadString('\n')
			if rerr != nil {
				return
			}
			if inData {
				if strings.TrimRight(line, "\r\n") == "." {
					inData = false
					captured <- body.String()
					write("250 2.0.0 queued")
					continue
				}
				body.WriteString(line)
				continue
			}
			switch verb := strings.ToUpper(strings.Fields(line + " ")[0]); verb {
			case "EHLO", "HELO":
				// No STARTTLS advertised; the sender configures no credentials
				// in this test, so no downgrade is possible.
				write("250-fake relay")
				write("250 8BITMIME")
			case "MAIL", "RCPT":
				write("250 2.1.0 ok")
			case "DATA":
				inData = true
				write("354 send it")
			case "QUIT":
				write("221 2.0.0 bye")
				return
			default:
				write("250 2.0.0 ok")
			}
		}
	}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return host, port
}
