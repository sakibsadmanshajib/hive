package webtools

import (
	"net"
	"net/netip"
	"strings"
	"testing"
)

// B2. Every admission denial the spec names, table driven, one row per class.
// A row that starts passing means an agent supplied URL can reach a place the
// gateway must never reach on its behalf.
func TestAdmitRejects(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
	}{
		{"loopback v4", "http://127.0.0.1/latest"},
		{"loopback name", "http://localhost:8080/"},
		{"loopback v6", "http://[::1]:9000/"},
		{"rfc1918", "http://10.0.0.5/admin"},
		{"rfc1918 172", "http://172.16.3.9/"},
		{"rfc1918 192", "http://192.168.1.1/"},
		{"link local aws metadata", "http://169.254.169.254/latest/meta-data/"},
		{"ipv6 unique local", "http://[fd00::1]/"},
		{"ipv6 ec2 metadata", "http://[fd00:ec2::254]/latest/meta-data/"},
		{"ipv4-compatible ipv6 loopback", "http://[::7f00:1]/"},
		{"ipv4-translated rfc6052", "http://[::ffff:0:7f00:1]/"},
		{"well-known nat64 metadata", "http://[64:ff9b::a9fe:a9fe]/latest/meta-data/"},
		{"deprecated site-local", "http://[fec0::1]/"},
		{"gcp metadata by name", "http://metadata.google.internal/computeMetadata/v1/"},
		{"azure metadata by name", "http://metadata.azure.com/metadata/instance"},
		{"alibaba metadata", "http://100.100.100.200/latest/meta-data/"},
		{"cgnat", "http://100.72.4.4/"},
		{"compose service name", "http://control-plane:8081/internal/accounting"},
		{"compose service name edge-api", "http://edge-api:8080/v1/models"},
		{"compose service name searxng", "http://searxng:8080/search"},
		{"file scheme", "file:///etc/passwd"},
		{"gopher scheme", "gopher://example.com/1"},
		{"data scheme", "data:text/html,<h1>hi</h1>"},
		{"ftp scheme", "ftp://example.com/x"},
		{"backslash parser confusion", "http://127.0.0.1\\@example.com/"},
		{"tab parser confusion", "http://exa\tmple.com/"},
		{"cr parser confusion", "http://example.com/\r/x"},
		{"lf parser confusion", "http://example.com/\n/x"},
		{"userinfo credentials", "http://user:pass@example.com/"},
		{"unspecified", "http://0.0.0.0/"},
		{"broadcast", "http://255.255.255.255/"},
		{"empty", ""},
		{"no host", "http:///path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Admit(tc.url); err == nil {
				t.Fatalf("Admit(%q) was allowed; it must be refused", tc.url)
			}
		})
	}
}

func TestAdmitAllowsOrdinaryPublicURLs(t *testing.T) {
	for _, raw := range []string{
		"https://example.com/",
		"http://example.com/a/b?c=d#e",
		"https://sub.domain.example.co.uk:8443/path",
		"https://93.184.216.34/",
	} {
		got, err := Admit(raw)
		if err != nil {
			t.Fatalf("Admit(%q) refused a public URL: %v", raw, err)
		}
		if got == nil || got.Host == "" {
			t.Fatalf("Admit(%q) returned %v", raw, got)
		}
	}
}

// Dotted-decimal shorthand ("127.1") is admitted here and refused later at
// connect time, and this test pins the reason so the split is not mistaken for
// a gap. Neither net.ParseIP nor netip.ParseAddr accepts the shorthand, so it
// is not treated as a literal by either parser; it contains a dot, so the
// dotless rule does not catch it; and the OS resolver is what expands it at
// dial time, which is exactly where the dialer screens the result.
//
// The practical consequence is bounded: nothing is reachable, but such a hit
// could be carried into a search envelope as a citation. Dropping it by
// comparing the two parsers, which is the obvious one-line fix, would catch
// nothing, and this test is what says so.
func TestDottedShorthandIsNotTreatedAsALiteral(t *testing.T) {
	const shorthand = "127.1"
	if ip := net.ParseIP(shorthand); ip != nil {
		t.Fatalf("net.ParseIP(%q) = %v; the comparison fix would be viable after all", shorthand, ip)
	}
	if addr, err := netip.ParseAddr(shorthand); err == nil {
		t.Fatalf("netip.ParseAddr(%q) = %v, want a parse error", shorthand, addr)
	}
	if _, err := Admit("http://" + shorthand + "/"); err != nil {
		t.Fatalf("Admit refused %q at admission; this test's premise no longer holds: %v", shorthand, err)
	}
}

// Admission refusals name a class, never the resolved address or the internal
// host, so the reason can be shown to a user without leaking topology (#1562
// precedent, criterion B11).
func TestAdmitErrorsCarryNoAddress(t *testing.T) {
	for _, raw := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://control-plane:8081/x",
		"http://10.0.0.5/",
	} {
		_, err := Admit(raw)
		if err == nil {
			t.Fatalf("Admit(%q) was allowed", raw)
		}
		for _, leak := range []string{"169.254.169.254", "control-plane", "10.0.0.5"} {
			if strings.Contains(err.Error(), leak) {
				t.Fatalf("Admit(%q) error %q leaks %q", raw, err, leak)
			}
		}
	}
}
