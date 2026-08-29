package mailer

import (
	"context"
	"errors"
	"testing"
)

// A recipient address is user input, and mail.ParseAddress does not hand back
// what it was given: it unquotes a quoted local part and discards a display
// name. Accepting the parser's rewrite is how `"a,b"@example.test` becomes a To
// header carrying two mailboxes, which is content spoofing of the one header a
// recipient reads to decide whether a message was meant for them.
//
// Proved load bearing: replacing the round-trip check in validRecipient with the
// older `headerSafe(parsed.Address)` makes every case here fail.
func TestSend_RefusesAnAddressThePayloadWouldNotRoundTrip(t *testing.T) {
	rewritten := map[string]string{
		"quoted local part smuggles a comma": `"a,b"@example.test`,
		"quoted local part smuggles a space": `"a b"@example.test`,
		"quoted local part smuggles angles":  `"a<x>b"@example.test`,
		"display name is silently dropped":   `Bob <bob@example.test>`,
		"display name reads as a claim":      `"Hive Support" <attacker@evil.test>`,
	}
	sender := NewSMTPSender(Config{
		Host:        "relay.example.test",
		FromAddress: "no_reply@example.test",
	})
	for name, addr := range rewritten {
		t.Run(name, func(t *testing.T) {
			if _, err := validRecipient(addr); !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("validRecipient(%q) error = %v, want ErrInvalidMessage", addr, err)
			}
			err := sender.Send(context.Background(), Message{To: addr, Subject: "s", Text: "t"})
			if !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("Send to %q error = %v, want ErrInvalidMessage", addr, err)
			}
		})
	}

	// The addresses an invitation actually carries still pass, including the
	// surrounding whitespace a paste leaves behind and mixed case, which must
	// survive untouched so the accept-time comparison sees the same string.
	for _, addr := range []string{"a@b.test", "  Foo.Bar+tag@Example.test  ", "x@sub.domain.test"} {
		got, err := validRecipient(addr)
		if err != nil {
			t.Fatalf("validRecipient(%q) unexpected error %v", addr, err)
		}
		if got == "" {
			t.Fatalf("validRecipient(%q) returned an empty address", addr)
		}
	}
}
