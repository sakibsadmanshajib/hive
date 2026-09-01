package webtools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubResolver hands out a scripted answer per lookup so a DNS rebinding
// sequence can be expressed without standing up a DNS server.
type stubResolver struct {
	answers [][]netip.Addr
	calls   atomic.Int32
}

func (s *stubResolver) LookupNetIP(_ context.Context, _ string, _ string) ([]netip.Addr, error) {
	i := int(s.calls.Add(1)) - 1
	if i >= len(s.answers) {
		i = len(s.answers) - 1
	}
	if len(s.answers) == 0 {
		return nil, errors.New("stubResolver: no answers scripted")
	}
	return s.answers[i], nil
}

// allowAnyAddrForTest weakens only the address class check, and only for the
// two tests below that need a live HTTP server on loopback. There is
// deliberately no exported setter and no environment variable that reaches
// ClientConfig.allowAddr: ENABLE_LOCAL_WEB_FETCH=true is exactly the knob
// spec section 7 item 11 forbids, and TestSafeClientDefaultRefusesLoopback
// holds the default shut.
func allowAnyAddrForTest(netip.Addr) bool { return true }

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return a
}

// B4. A name that answers public on the first lookup and private on the
// second must be refused at connect time, on the address actually being
// connected to. The test server sits on the exact address the second answer
// points at, so a guard that checks at resolve time only would let the
// request through and the hit counter would move.
func TestSafeClientRefusesRebindAtConnectTime(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()

	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}
	host, port, err := net.SplitHostPort(srvURL.Host)
	if err != nil {
		t.Fatalf("splitting %q: %v", srvURL.Host, err)
	}

	res := &stubResolver{answers: [][]netip.Addr{
		{mustAddr(t, "93.184.216.34")}, // first lookup: public
		{mustAddr(t, host)},            // second lookup: the loopback server
	}}

	// The first lookup, standing in for any caller that validates a name
	// before handing it to the client. It answers public, so a resolve-time
	// check would admit the URL here.
	if _, err := res.LookupNetIP(context.Background(), "ip", "rebind.example"); err != nil {
		t.Fatalf("priming lookup: %v", err)
	}

	client := SafeClient(ClientConfig{Resolver: res})
	resp, err := client.Get(fmt.Sprintf("http://rebind.example:%s/", port))
	if err == nil {
		resp.Body.Close()
		t.Fatal("the rebound request succeeded; connect-time validation did not run")
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("error = %v, want ErrBlockedAddress", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("the server was reached %d times; it must be reached zero times", got)
	}
}

// The default configuration must refuse a private address with no resolver,
// no flag and no environment variable involved. This is the assertion that
// keeps the test-only allowAddr hook below from ever becoming the deployment
// default (spec section 7 item 11).
func TestSafeClientDefaultRefusesLoopback(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	client := SafeClient(ClientConfig{})
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("the default client reached a loopback server")
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("error = %v, want ErrBlockedAddress", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("the server was reached %d times; want 0", got)
	}
}

// B3. A public host that 302s to 127.0.0.1 is refused at the redirect, and
// the redirected body never comes back.
func TestSafeClientRefusesRedirectToPrivate(t *testing.T) {
	var secretHits atomic.Int32
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secretHits.Add(1)
		_, _ = w.Write([]byte("INTERNAL"))
	}))
	defer secret.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secret.URL, http.StatusFound)
	}))
	defer origin.Close()

	client := SafeClient(ClientConfig{allowAddr: allowAnyAddrForTest})
	resp, err := client.Get(origin.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("the redirect to a private address was followed")
	}
	if !errors.Is(err, ErrBlockedRedirect) {
		t.Fatalf("error = %v, want ErrBlockedRedirect", err)
	}
	if got := secretHits.Load(); got != 0 {
		t.Fatalf("the redirect target was reached %d times; want 0", got)
	}
}

// Hop limit. Without one, an attacker page can walk the fetcher through an
// unbounded redirect chain. Asserted against the client's own CheckRedirect
// so the policy is exercised without a live loop.
func TestSafeClientBoundsRedirectHops(t *testing.T) {
	client := SafeClient(ClientConfig{})
	req, err := http.NewRequest(http.MethodGet, "https://example.com/final", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if err := client.CheckRedirect(req, make([]*http.Request, MaxRedirectHops-1)); err != nil {
		t.Fatalf("hop %d was refused: %v", MaxRedirectHops-1, err)
	}
	if err := client.CheckRedirect(req, make([]*http.Request, MaxRedirectHops)); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("hop %d: error = %v, want ErrTooManyRedirects", MaxRedirectHops, err)
	}
}

// Total time cap. A slow page must not hold a tool call open indefinitely.
func TestSafeClientAppliesTotalTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	// Order matters: httptest.Server.Close waits for outstanding handlers, so
	// the handler has to be released before Close runs. Deferred calls run
	// last-in-first-out, hence Close first, release second.
	defer srv.Close()
	defer close(release)

	client := SafeClient(ClientConfig{Timeout: 50 * time.Millisecond, allowAddr: allowAnyAddrForTest})
	start := time.Now()
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("a request against a server that never answers returned successfully")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the timeout took %s to fire", elapsed)
	}
}

// Every value leaving this file names a class and carries no address. The raw
// *net.OpError from a failed dial stringifies as "dial tcp 93.184.216.34:80:
// connect: connection refused", so the one path that returns a live dial
// failure is the one that would otherwise break the rule the sentinels follow.
// The address belongs in the log, not in the value a caller may surface.
func TestDialFailureCarriesNoAddress(t *testing.T) {
	// A server that is closed immediately, so the port is real, admissible
	// under the test hook, and refuses the connection.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	target := srv.URL
	srv.Close()

	client := SafeClient(ClientConfig{allowAddr: allowAnyAddrForTest, Timeout: 2 * time.Second})
	resp, err := client.Get(target)
	if err == nil {
		resp.Body.Close()
		t.Fatal("a connection to a closed port succeeded")
	}
	if !errors.Is(err, ErrDialFailed) {
		t.Fatalf("error = %v, want ErrDialFailed", err)
	}
	// url.Error wraps our sentinel and adds the request URL, which is the
	// caller's own input. What must not appear is the resolved address.
	if strings.Contains(ErrDialFailed.Error(), "127.0.0.1") {
		t.Fatalf("the sentinel itself carries an address: %q", ErrDialFailed)
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		t.Fatalf("the raw dial error is still reachable through the chain: %v", opErr)
	}
}

// The client must not honour HTTP_PROXY and friends: a proxy would carry the
// request past the dialer that does the address checking.
func TestSafeClientIgnoresProxyEnvironment(t *testing.T) {
	tr, ok := SafeClient(ClientConfig{}).Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", SafeClient(ClientConfig{}).Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("transport has a Proxy function; an agent supplied URL must never be proxied")
	}
}

func TestAddrAllowed(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"93.184.216.34", true},
		{"2606:2800:220:1:248:1893:25c8:1946", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.1.2.3", false},
		{"172.20.0.7", false},
		{"192.168.0.1", false},
		{"169.254.169.254", false},
		{"fd00:ec2::254", false},
		{"fe80::1", false},
		{"100.100.100.200", false},
		{"100.64.0.1", false},
		{"0.0.0.0", false},
		{"255.255.255.255", false},
		{"224.0.0.1", false},
		{"240.0.0.1", false},
		{"192.0.0.1", false},
		{"198.18.0.1", false},
		{"::ffff:127.0.0.1", false},
		{"::ffff:10.0.0.1", false},
		{"2001:db8::1", false},
		// IPv4-compatible IPv6. ::127.0.0.1 is ::7f00:1, and Is4In6 matches
		// only the ::ffff: mapped form, so Unmap never runs and IsLoopback is
		// false for this shape.
		{"::7f00:1", false},
		{"::a00:1", false},
		// IPv4-translated, RFC 6052 SIIT.
		{"::ffff:0:7f00:1", false},
		// Well-known NAT64, which embeds an arbitrary v4 address. The last
		// row is the AWS metadata endpoint behind a translator.
		{"64:ff9b::7f00:1", false},
		{"64:ff9b::a9fe:a9fe", false},
		// Local-use NAT64.
		{"64:ff9b:1::1", false},
		// Deprecated site-local, outside Go's IsPrivate.
		{"fec0::1", false},
		{"feff::1", false},
		// A global address adjacent to the ranges above must still pass, so
		// these prefixes cannot quietly swallow the public internet.
		{"64:ff9c::1", true},
		{"2001:4860:4860::8888", true},
	} {
		got := addrAllowed(mustAddr(t, tc.addr))
		if got != tc.want {
			t.Errorf("addrAllowed(%s) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}
