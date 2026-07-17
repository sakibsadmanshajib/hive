// Package egressproxy implements the allowlist-enforcing forward proxy that
// bounds agent-sandbox network egress (issue #308). Each sandbox launch gets
// its own Proxy instance scoped to that tenant/user's effective
// allowed_hosts (apps/control-plane/internal/egress); the sandbox's
// HTTP_PROXY and HTTPS_PROXY point at it (apps/agent-engine/internal/sandbox
// wires this), so every outbound connection is checked against the
// allowlist and refused by default.
package egressproxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// Proxy is an allowlist-enforcing HTTP/HTTPS forward proxy. It implements
// http.Handler so callers run it with an ordinary http.Server.
type Proxy struct {
	allowed map[string]struct{}
	// allowLocalDest opts out of the private/loopback/link-local destination
	// guard (issue #342 item 2). Always false in production; only the
	// test-only constructor in export_test.go sets it true, since the
	// httptest backends the proxy tests dial bind to 127.0.0.1.
	allowLocalDest bool
	transport      *http.Transport
}

// New builds a Proxy that permits egress only to the given hosts (hostname
// or IP, no port, matching the egress-policy wire shape). A nil or empty
// list denies all egress, which is the correct fail-closed behaviour when no
// policy could be resolved. Resolved addresses that are private, loopback,
// or link-local are always refused (issue #342 item 2).
func New(allowedHosts []string) *Proxy {
	return newProxy(allowedHosts, false)
}

func newProxy(allowedHosts []string, allowLocalDest bool) *Proxy {
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, h := range allowedHosts {
		allowed[strings.ToLower(h)] = struct{}{}
	}
	p := &Proxy{
		allowed:        allowed,
		allowLocalDest: allowLocalDest,
	}
	// Plain-HTTP path (handleForward) must pin the resolved IP exactly
	// like the CONNECT path: DialContext resolves once here instead of
	// letting the transport's default dialer resolve addr itself.
	p.transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			pinned, err := resolvePinnedAddr(ctx, addr, "80", allowLocalDest)
			if err != nil {
				return nil, err
			}
			return (&net.Dialer{}).DialContext(ctx, network, pinned)
		},
	}
	return p
}

func (p *Proxy) isAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	_, ok := p.allowed[strings.ToLower(host)]
	return ok
}

// ServeHTTP dispatches CONNECT (HTTPS tunnelling) separately from plain
// proxied HTTP requests. Both deny by default.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleForward(w, r)
}

func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	if !p.isAllowed(r.Host) {
		http.Error(w, "egress denied: host not in allowlist", http.StatusForbidden)
		return
	}

	// Resolve once and dial the literal pinned address rather than r.Host
	// again: dialing by hostname would trigger a second, independent DNS
	// resolution, leaving a window for a short-TTL DNS-rebind response
	// between the allowlist check above and the actual connection.
	pinned, err := resolvePinnedAddr(r.Context(), r.Host, "443", p.allowLocalDest)
	if err != nil {
		http.Error(w, "egress: dns lookup failed", http.StatusBadGateway)
		return
	}

	dest, err := net.Dial("tcp", pinned)
	if err != nil {
		http.Error(w, "egress: dial failed", http.StatusBadGateway)
		return
	}
	defer dest.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "egress: hijack unsupported", http.StatusInternalServerError)
		return
	}
	src, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer src.Close()

	if _, err := src.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	relay(src, dest)
}

func (p *Proxy) handleForward(w http.ResponseWriter, r *http.Request) {
	if !p.isAllowed(r.Host) {
		http.Error(w, "egress denied: host not in allowlist", http.StatusForbidden)
		return
	}

	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	resp, err := p.transport.RoundTrip(outReq)
	if err != nil {
		http.Error(w, "egress: upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// resolvePinnedAddr resolves hostport's host component exactly once and
// returns "resolvedIP:port", so the caller can dial that literal address
// instead of triggering a second resolution. defaultPort is used when
// hostport carries no port of its own.
func resolvePinnedAddr(ctx context.Context, hostport, defaultPort string, allowLocal bool) (string, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = hostport, defaultPort
	}
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return "", fmt.Errorf("egressproxy: could not resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return "", fmt.Errorf("egressproxy: no addresses found for %q", host)
	}
	// Issue #342 item 2: pin the first resolved address that is not private,
	// loopback, or link-local. An allowed hostname rebound to an internal IP
	// (127.0.0.1, 169.254.x.x, RFC1918/ULA) must not become a path to
	// host-local or LAN services from inside the sandbox. Mirrors the same
	// guard in the desktop Rust proxy (egress_proxy.rs::resolve_pinned) so
	// both #308 surfaces reject the same addresses.
	for _, ipStr := range ips {
		if ip := net.ParseIP(ipStr); ip != nil && (allowLocal || !isForbiddenIP(ip)) {
			return net.JoinHostPort(ipStr, port), nil
		}
	}
	return "", fmt.Errorf(
		"egressproxy: %q resolved only to disallowed (private/loopback/link-local) addresses",
		host,
	)
}

// isForbiddenIP reports whether ip is an address a sandboxed task must never
// be allowed to reach: loopback, unspecified, multicast, or any private or
// link-local range on IPv4 or IPv6. net.IP.IsPrivate covers RFC1918 and the
// IPv6 unique-local fc00::/7 range.
func isForbiddenIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsUnspecified() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.Equal(net.IPv4bcast)
}

func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
}
