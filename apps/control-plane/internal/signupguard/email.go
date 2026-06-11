// Package signupguard provides abuse-prevention checks for the public signup
// entry path (issue #116): disposable-email-domain blocking, per-IP signup
// rate limiting, and Cloudflare Turnstile CAPTCHA verification.
//
// All customer-visible rejections use a single generic, provider-blind message
// so an attacker cannot tell which control tripped (disposable list vs rate
// limit vs CAPTCHA) and no upstream/internal detail leaks. Operators see the
// real classification in the process log and the audit trail.
package signupguard

import (
	"bufio"
	"bytes"
	_ "embed"
	"errors"
	"strings"
)

//go:embed data/disposable_domains.txt
var disposableDomainsFile []byte

// ErrInvalidEmail is returned when an address cannot be parsed into a
// local-part and a single domain.
var ErrInvalidEmail = errors.New("signupguard: invalid email address")

// NormalizeEmail lowercases, trims, and canonicalizes an email so that a single
// underlying mailbox cannot be used to create unlimited accounts.
//
//   - The whole address is trimmed and lowercased.
//   - Any "+tag" suffix on the local part is stripped (sub-addressing).
//   - For Gmail/Googlemail, dots in the local part are removed and the domain
//     is folded to gmail.com (Google treats a.b@gmail == ab@gmail == ab@googlemail).
//
// It returns ErrInvalidEmail for anything that is not exactly local@domain.
func NormalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	at := strings.IndexByte(e, '@')
	if at <= 0 || at != strings.LastIndexByte(e, '@') {
		return "", ErrInvalidEmail
	}
	local := e[:at]
	domain := e[at+1:]
	if local == "" || domain == "" {
		return "", ErrInvalidEmail
	}

	if plus := strings.IndexByte(local, '+'); plus >= 0 {
		local = local[:plus]
	}

	if domain == "gmail.com" || domain == "googlemail.com" {
		local = strings.ReplaceAll(local, ".", "")
		domain = "gmail.com"
	}

	if local == "" {
		return "", ErrInvalidEmail
	}
	return local + "@" + domain, nil
}

// EmailDomain returns the lowercased, trimmed domain portion of an email after
// normalization (so a "+tag" address and a Gmail-dotted address resolve to the
// canonical domain).
func EmailDomain(raw string) (string, error) {
	norm, err := NormalizeEmail(raw)
	if err != nil {
		return "", err
	}
	at := strings.LastIndexByte(norm, '@')
	return norm[at+1:], nil
}

// Blocklist is an immutable set of disposable email domains. Construct it once
// at startup via LoadDisposableBlocklist and share the pointer; reads are
// lock-free and concurrency-safe.
type Blocklist struct {
	domains map[string]struct{}
}

// LoadDisposableBlocklist parses the embedded disposable-domain list. It never
// performs network I/O; the list is compiled into the binary.
func LoadDisposableBlocklist() (*Blocklist, error) {
	domains := make(map[string]struct{}, 256)
	scanner := bufio.NewScanner(bytes.NewReader(disposableDomainsFile))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domains[strings.ToLower(line)] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return &Blocklist{domains: domains}, nil
}

// Len returns the number of domains in the blocklist.
func (b *Blocklist) Len() int {
	if b == nil {
		return 0
	}
	return len(b.domains)
}

// IsDisposableDomain reports whether the given (already lowercased) domain is in
// the blocklist. An empty domain never matches.
func (b *Blocklist) IsDisposableDomain(domain string) bool {
	if b == nil || domain == "" {
		return false
	}
	_, ok := b.domains[domain]
	return ok
}

// IsDisposableEmail normalizes the email and reports whether its domain is a
// known disposable provider. It returns ErrInvalidEmail for unparseable input.
func (b *Blocklist) IsDisposableEmail(raw string) (bool, error) {
	domain, err := EmailDomain(raw)
	if err != nil {
		return false, err
	}
	return b.IsDisposableDomain(domain), nil
}
