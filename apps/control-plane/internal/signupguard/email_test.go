package signupguard

import "testing"

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "lowercases and trims", in: "  Alice@Example.COM ", want: "alice@example.com"},
		{name: "strips gmail plus tag", in: "alice+spam@gmail.com", want: "alice@gmail.com"},
		{name: "strips googlemail dots and plus", in: "a.l.i.c.e+x@googlemail.com", want: "alice@gmail.com"},
		{name: "keeps dots for non-gmail", in: "a.l.i.c.e@example.com", want: "a.l.i.c.e@example.com"},
		{name: "strips plus tag for non-gmail too", in: "user+tag@example.com", want: "user@example.com"},
		{name: "empty is error", in: "   ", wantErr: true},
		{name: "no at sign is error", in: "notanemail", wantErr: true},
		{name: "empty local part is error", in: "@example.com", wantErr: true},
		{name: "empty domain is error", in: "user@", wantErr: true},
		{name: "double at is error", in: "a@b@example.com", wantErr: true},
		{name: "unicode local preserved", in: "JÜrgen@Example.com", want: "jürgen@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeEmail(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("NormalizeEmail(%q) expected error, got %q", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeEmail(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeEmail(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestEmailDomain(t *testing.T) {
	got, err := EmailDomain("Alice+x@Sub.Example.COM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sub.example.com" {
		t.Fatalf("EmailDomain = %q, want sub.example.com", got)
	}
	if _, err := EmailDomain("garbage"); err == nil {
		t.Fatal("expected error for invalid email")
	}
}

func TestDisposableBlocklist(t *testing.T) {
	bl, err := LoadDisposableBlocklist()
	if err != nil {
		t.Fatalf("LoadDisposableBlocklist: %v", err)
	}

	if n := bl.Len(); n < 50 {
		t.Fatalf("blocklist too small (%d), embed likely broken", n)
	}

	cases := []struct {
		email   string
		blocked bool
	}{
		{"abuser@mailinator.com", true},
		{"x@MAILINATOR.COM", true},        // case-insensitive
		{"x+tag@guerrillamail.com", true}, // plus tag does not evade
		{"x@10minutemail.com", true},
		{"real.user@gmail.com", false},
		{"founder@fundmore.ai", false},
		{"dev@somelegitcompany.io", false},
	}
	for _, c := range cases {
		got, err := bl.IsDisposableEmail(c.email)
		if err != nil {
			t.Fatalf("IsDisposableEmail(%q): %v", c.email, err)
		}
		if got != c.blocked {
			t.Fatalf("IsDisposableEmail(%q) = %v, want %v", c.email, got, c.blocked)
		}
	}
}

func TestDisposableBlocklistDomainComments(t *testing.T) {
	bl, err := LoadDisposableBlocklist()
	if err != nil {
		t.Fatalf("LoadDisposableBlocklist: %v", err)
	}
	// Comment lines and the literal '#' token must never become a domain.
	if bl.IsDisposableDomain("#") {
		t.Fatal("comment marker leaked into blocklist")
	}
	if bl.IsDisposableDomain("") {
		t.Fatal("empty domain must never match")
	}
}
