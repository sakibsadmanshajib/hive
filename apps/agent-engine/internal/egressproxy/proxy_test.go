package egressproxy_test

import (
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/egressproxy"
)

func TestProxy_AllowsAllowlistedHTTPSHost(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	// NewAllowingLocalDest: the backend is a 127.0.0.1 httptest server, which
	// the production loopback guard (issue #342 item 2) would correctly
	// refuse. This test opts out to exercise the allow path itself.
	proxy := httptest.NewServer(egressproxy.NewAllowingLocalDest([]string{hostOf(t, backend.URL)}))
	defer proxy.Close()

	client := clientThroughProxy(t, proxy.URL)
	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("expected allowlisted request to succeed, got %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestProxy_BlocksNonAllowlistedHTTPSHost(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should never be reached"))
	}))
	defer backend.Close()

	// Allowlist a host other than the backend, so the CONNECT is denied.
	proxy := httptest.NewServer(egressproxy.New([]string{"example-not-the-backend.invalid"}))
	defer proxy.Close()

	client := clientThroughProxy(t, proxy.URL)
	_, err := client.Get(backend.URL)
	if err == nil {
		t.Fatal("expected blocked request to fail, got nil error")
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Fatalf("expected Forbidden (403) in CONNECT error, got %v", err)
	}
}

func TestProxy_EmptyAllowlistDeniesEverything(t *testing.T) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer backend.Close()

	proxy := httptest.NewServer(egressproxy.New(nil))
	defer proxy.Close()

	client := clientThroughProxy(t, proxy.URL)
	if _, err := client.Get(backend.URL); err == nil {
		t.Fatal("expected fail-closed empty allowlist to deny request")
	}
}

func TestProxy_AllowsAllowlistedPlainHTTPHost(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain-ok"))
	}))
	defer backend.Close()

	// NewAllowingLocalDest: 127.0.0.1 httptest backend, see the HTTPS variant.
	proxy := httptest.NewServer(egressproxy.NewAllowingLocalDest([]string{hostOf(t, backend.URL)}))
	defer proxy.Close()

	proxyURLParsed, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURLParsed)}}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("expected allowlisted plain HTTP request to succeed, got %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "plain-ok" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestProxy_BlocksNonAllowlistedPlainHTTPHost(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("should never be reached"))
	}))
	defer backend.Close()

	proxy := httptest.NewServer(egressproxy.New([]string{"example-not-the-backend.invalid"}))
	defer proxy.Close()

	proxyURLParsed, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURLParsed)}}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("request failed transport-level: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestProxy_UnresolvablePlainHTTPHostFailsCleanly(t *testing.T) {
	// Same pinning fix as the CONNECT path, applied to handleForward's
	// custom transport DialContext.
	const unresolvable = "this-host-does-not-exist.invalid"
	proxy := httptest.NewServer(egressproxy.New([]string{unresolvable}))
	defer proxy.Close()

	proxyURLParsed, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURLParsed)}}

	resp, err := client.Get("http://" + unresolvable)
	if err != nil {
		t.Fatalf("request failed transport-level: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 for unresolvable host, got %d", resp.StatusCode)
	}
}

func TestProxy_UnresolvableAllowlistedHostFailsCleanly(t *testing.T) {
	// Exercises the resolve-once-and-pin path's error branch (DNS-rebind
	// mitigation): an allowlisted host that simply cannot be resolved must
	// fail the CONNECT cleanly rather than falling through to any default
	// route.
	const unresolvable = "this-host-does-not-exist.invalid"
	proxy := httptest.NewServer(egressproxy.New([]string{unresolvable}))
	defer proxy.Close()

	client := clientThroughProxy(t, proxy.URL)
	_, err := client.Get("https://" + unresolvable)
	if err == nil {
		t.Fatal("expected request to an unresolvable allowlisted host to fail")
	}
}

func hostOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url %q: %v", rawURL, err)
	}
	if host, _, err := net.SplitHostPort(u.Host); err == nil {
		return host
	}
	return u.Hostname()
}

func clientThroughProxy(t *testing.T, proxyURL string) *http.Client {
	t.Helper()
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(parsed),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // test backend uses a self-signed cert
		},
	}
}
