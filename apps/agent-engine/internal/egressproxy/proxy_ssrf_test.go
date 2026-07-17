package egressproxy

import (
	"context"
	"net"
	"strings"
	"testing"
)

// Issue #342 item 2: the resolved-address guard that keeps an allowed
// hostname rebound to an internal IP from reaching host-local or LAN
// services from inside the sandbox. Mirrors the desktop Rust proxy's
// egress_proxy.rs::is_forbidden_ip / resolve_pinned so both #308 surfaces
// reject the same set.

func TestIsForbiddenIP(t *testing.T) {
	forbidden := []string{
		"127.0.0.1", "10.1.2.3", "192.168.0.1", "172.16.0.1",
		"169.254.0.1", "0.0.0.0", "255.255.255.255", "224.0.0.1",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1",
	}
	for _, s := range forbidden {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		if !isForbiddenIP(ip) {
			t.Errorf("%s should be forbidden", s)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("bad test IP %q", s)
		}
		if isForbiddenIP(ip) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestResolvePinnedAddrRejectsInternalLiterals(t *testing.T) {
	ctx := context.Background()
	// IP literals: LookupHost returns them as-is, so these do no DNS and
	// are deterministic.
	for _, hp := range []string{"127.0.0.1:443", "10.0.0.1:443", "169.254.1.1:443", "192.168.1.1:443"} {
		if _, err := resolvePinnedAddr(ctx, hp, "443", false); err == nil {
			t.Errorf("%s should be rejected as an internal address", hp)
		}
	}
}

func TestResolvePinnedAddrAllowsPublicLiteral(t *testing.T) {
	ctx := context.Background()
	got, err := resolvePinnedAddr(ctx, "8.8.8.8:443", "443", false)
	if err != nil {
		t.Fatalf("public IP literal should resolve: %v", err)
	}
	if !strings.HasPrefix(got, "8.8.8.8:") {
		t.Errorf("unexpected pinned addr %q", got)
	}
}
