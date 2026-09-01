package webtools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// The HTTP client web_fetch uses for agent-supplied URLs.
//
// Three things make it different from a plain http.Client, and all three are
// load bearing:
//
//  1. It resolves the hostname itself and dials the resolved literal, after
//     checking every candidate address with addrAllowed. That is the Go form
//     of upstream's _ssrf_safe_new_conn: the lookup that feeds the actual TCP
//     connect is the one that gets validated, so there is no second
//     resolution and no DNS rebinding window.
//  2. It re-admits every redirect hop through Admit, with a hop limit.
//     Upstream never solved this case because it does not follow redirects at
//     all (AIOHTTP_CLIENT_ALLOW_REDIRECTS defaults to false). We follow them,
//     so we screen them.
//  3. It ignores HTTP_PROXY and friends. A proxy would carry the request past
//     the dialer that does the checking, which would quietly undo item 1.

const (
	// DefaultTotalTimeout caps the wall time of one fetch, headers and body
	// together. Near the fork's own measured 12 second loader timeout.
	DefaultTotalTimeout = 15 * time.Second
	// MaxRedirectHops bounds a redirect chain.
	MaxRedirectHops = 5
)

var (
	// ErrBlockedAddress is returned by the dialer when a host resolves to no
	// globally routable address. It carries no address of its own.
	ErrBlockedAddress = errors.New("webtools: refusing to connect to a non-global address")
	// ErrBlockedRedirect is returned when a redirect target fails admission.
	ErrBlockedRedirect = errors.New("webtools: redirect target refused")
	// ErrTooManyRedirects is returned past MaxRedirectHops.
	ErrTooManyRedirects = errors.New("webtools: too many redirects")
	// ErrResolveFailed is returned when a hostname does not resolve. It is
	// deliberately generic: the resolver's own message can carry search
	// domains and internal suffixes.
	ErrResolveFailed = errors.New("webtools: could not resolve host")
)

// Resolver is the lookup surface the safe dialer needs. *net.Resolver
// satisfies it as-is, so production wiring passes net.DefaultResolver and
// tests pass a scripted stub.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// ClientConfig configures SafeClient. Every field has a safe zero value.
type ClientConfig struct {
	// Timeout is the total time cap for one request. Zero means
	// DefaultTotalTimeout.
	Timeout time.Duration
	// Resolver is the DNS surface. Nil means net.DefaultResolver.
	Resolver Resolver

	// allowAddr overrides the address class check. It is unexported, has no
	// setter and no environment variable behind it, and is set only by this
	// package's own tests, which need a live HTTP server on loopback to
	// exercise redirect handling. Spec section 7 item 11 is explicit that
	// ENABLE_LOCAL_WEB_FETCH=true, the fork's equivalent knob, must never be
	// true on any deployment; keeping the Go equivalent unreachable from
	// outside this package is how that is held.
	allowAddr func(netip.Addr) bool
}

// SafeClient builds the HTTP client for agent-supplied URLs.
func SafeClient(cfg ClientConfig) *http.Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTotalTimeout
	}
	res := cfg.Resolver
	if res == nil {
		res = net.DefaultResolver
	}
	allow := cfg.allowAddr
	if allow == nil {
		allow = addrAllowed
	}

	d := &safeDialer{resolver: res, allow: allow, dialer: &net.Dialer{Timeout: timeout, KeepAlive: -1}}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// Never nil-check this away: a proxy is a way around the dialer.
			Proxy:       nil,
			DialContext: d.DialContext,
			// One connection per request. A pooled connection skips
			// DialContext and therefore skips the address check; the cost of
			// an extra handshake on a rare tool call is not worth reasoning
			// about when that reuse is safe.
			DisableKeepAlives:     true,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirectHops {
				return ErrTooManyRedirects
			}
			if _, err := Admit(req.URL.String()); err != nil {
				return fmt.Errorf("%w: %w", ErrBlockedRedirect, err)
			}
			return nil
		},
	}
}

type safeDialer struct {
	resolver Resolver
	allow    func(netip.Addr) bool
	dialer   *net.Dialer
}

// DialContext resolves once, screens every candidate, and connects to a
// screened literal address. A host whose every address is non-global is
// ErrBlockedAddress, which is what makes the refusal happen at connect time
// rather than at some earlier lookup a later one can contradict.
func (d *safeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: unparseable address", ErrBlockedAddress)
	}

	// Defence in depth. Every path into this client today goes through Admit
	// first, which already refuses these names, but the dialer is the last
	// thing between a hostname and a socket and it should not depend on a
	// caller having remembered.
	if deniedHostnames[strings.ToLower(strings.TrimSuffix(host, "."))] {
		return nil, ErrBlockedAddress
	}

	var candidates []netip.Addr
	if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
		candidates = []netip.Addr{literal}
	} else {
		candidates, err = d.resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, ErrResolveFailed
		}
	}

	var dialErr error
	for _, candidate := range candidates {
		if !d.allow(candidate) {
			continue
		}
		conn, err := d.dialer.DialContext(ctx, network, net.JoinHostPort(candidate.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = err
	}
	if dialErr != nil {
		return nil, dialErr
	}
	return nil, ErrBlockedAddress
}
