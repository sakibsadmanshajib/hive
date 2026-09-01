package webtools

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// URL admission for an agent-supplied URL, stage 0 of the web_fetch pipeline.
//
// The threat this exists for is sharper than for the search path: with
// web_fetch the URL comes from the model, and the model reads attacker
// controlled pages. A page that says "for the full text see
// http://169.254.169.254/latest/meta-data/" is the whole attack.
//
// Nothing here resolves DNS. Resolution-time checking is not what closes the
// rebinding window, and doing it here as well as at connect time would only
// produce a second answer that the connect can disagree with. The address
// class check that matters runs in safedial.go's dialer, on the address
// actually being connected to, and it shares addrAllowed below with the
// literal-address arm of this function so the two can never diverge.

var (
	// ErrURLRejected is the class every admission denial belongs to. The
	// wrapped detail names the reason class and never the address, so the
	// reason can be logged next to the URL without repeating an internal
	// hostname back at whoever supplied it (the #1562 precedent).
	ErrURLRejected = errors.New("webtools: url refused")
)

// deniedHostnames carries upstream's DEFAULT_WEB_FETCH_FILTER_LIST
// (vendor/open-webui/backend/open_webui/config.py) across deliberately. These
// are denied by name, independent of what DNS answers for them, so a future
// DNS answer cannot re-open a metadata endpoint that the address-class check
// would otherwise have to catch. Entries here are the ones that contain a
// dot; the dotless ones (a bare compose service name) are covered by the
// stronger general rule in Admit below.
var deniedHostnames = map[string]bool{
	"metadata.google.internal": true,
	"metadata.azure.com":       true,
}

// parserConfusingChars are rejected before parsing decides anything, mirroring
// vendor/open-webui/backend/open_webui/retrieval/web/utils.py: url.Parse and
// an HTTP client can split on these differently, so
// "http://127.0.0.1\@1.1.1.1" reads as the public host to one and the
// loopback host to the other.
const parserConfusingChars = "\\\t\n\r"

// nonGlobalPrefixes are the special-purpose ranges Go's own net/netip
// predicates do not cover but Python's ipaddress.is_global does exclude. The
// address-class rule this package enforces is "must be globally routable",
// and IsLoopback/IsPrivate/IsLinkLocal*/IsMulticast/IsUnspecified alone leave
// CGNAT (which contains Alibaba's 100.100.100.200 metadata endpoint), the
// IETF protocol assignments, the benchmarking range, the documentation
// ranges, and the reserved 240/4 block wide open.
var nonGlobalPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT, contains 100.100.100.200
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("192.88.99.0/24"),  // 6to4 relay anycast
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes 255.255.255.255
	netip.MustParsePrefix("::/128"),          // unspecified
	netip.MustParsePrefix("64:ff9b:1::/48"),  // local-use NAT64
	netip.MustParsePrefix("100::/64"),        // discard-only
	netip.MustParsePrefix("2001::/32"),       // Teredo
	netip.MustParsePrefix("2001:2::/48"),     // benchmarking
	netip.MustParsePrefix("2001:db8::/32"),   // documentation
	netip.MustParsePrefix("2002::/16"),       // 6to4, embeds an arbitrary v4 address
}

// addrAllowed reports whether a is an address this gateway will connect to on
// an agent's behalf: globally routable, and nothing else. It is the Go
// equivalent of Python's ipaddress.is_global, and it is the single place both
// the literal-address arm of Admit and the connect-time dialer check ask.
func addrAllowed(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	// An IPv4-mapped IPv6 address is an IPv4 address wearing a hat:
	// ::ffff:127.0.0.1 is loopback, and the v6 predicates do not say so.
	if a.Is4In6() {
		a = a.Unmap()
	}
	if a.IsLoopback() || a.IsUnspecified() || a.IsPrivate() ||
		a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() {
		return false
	}
	for _, p := range nonGlobalPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// Admit parses and screens an agent-supplied URL, returning the parsed URL
// when it may be fetched. Every denial is an ErrURLRejected.
//
// It is also what re-admits every redirect hop (safedial.go's CheckRedirect),
// which is the case upstream never had to solve: AIOHTTP_CLIENT_ALLOW_REDIRECTS
// defaults to false there, so a redirect to a private address was never
// followed and never had to be screened.
func Admit(rawURL string) (*url.URL, error) {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty", ErrURLRejected)
	}
	if strings.ContainsAny(raw, parserConfusingChars) {
		return nil, fmt.Errorf("%w: parser-confusing character", ErrURLRejected)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: unparseable", ErrURLRejected)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("%w: scheme", ErrURLRejected)
	}
	// Credentials in a URL are a parser-confusion vector in their own right
	// and there is no legitimate reason for a model to send one.
	if parsed.User != nil {
		return nil, fmt.Errorf("%w: embedded credentials", ErrURLRejected)
	}

	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return nil, fmt.Errorf("%w: no host", ErrURLRejected)
	}
	if deniedHostnames[host] {
		return nil, fmt.Errorf("%w: denied host", ErrURLRejected)
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if !addrAllowed(addr) {
			return nil, fmt.Errorf("%w: non-global address", ErrURLRejected)
		}
		return parsed, nil
	}

	// A dotless hostname is never a public site; it is a container DNS name,
	// a search-domain completion or an intranet short name. Refusing the
	// whole shape covers every compose service in this stack (edge-api,
	// control-plane, litellm, searxng, markitdown, open-webui, redis, and
	// every self-hosted Supabase service) plus every service a future
	// deployment adds, which an enumerated list cannot do.
	if !strings.Contains(host, ".") {
		return nil, fmt.Errorf("%w: non-public host", ErrURLRejected)
	}

	return parsed, nil
}
