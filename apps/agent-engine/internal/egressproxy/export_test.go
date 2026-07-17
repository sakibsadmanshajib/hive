package egressproxy

// NewAllowingLocalDest builds a Proxy that permits egress to
// private/loopback/link-local destinations. Test-only: this file compiles
// only under `go test`, so production has no such escape hatch. The
// destination guard in resolvePinnedAddr (issue #342 item 2) is correct in
// production, but the httptest backends the proxy tests dial bind to
// 127.0.0.1, so those specific tests must opt out of it.
func NewAllowingLocalDest(allowedHosts []string) *Proxy {
	return newProxy(allowedHosts, true)
}
