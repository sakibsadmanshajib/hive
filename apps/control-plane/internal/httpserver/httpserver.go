// Package httpserver holds the public API server's construction, including
// the timeouts a streaming handler has to know about.
//
// It is a package rather than a function in cmd/server because the timeouts
// have to be reachable from outside package main. They were not, and that is
// how a fifteen second guillotine sat on the agent-task event stream through
// sixteen review findings: every test of that endpoint used
// httptest.NewServer, which sets no timeouts at all, so all of them passed
// against a handler production was cutting mid-response. Any replacement
// server built from its own literals would have the same hole one copy-paste
// later, so there is one definition and everything that needs it imports it.
package httpserver

import (
	"net/http"
	"time"
)

// WriteTimeout is the API server's write timeout.
//
// Go applies this as ONE absolute deadline for the whole response rather than
// per write, so any handler that holds a response open past it is cut
// mid-body with "i/o timeout" on this side and an unexpected EOF at the
// client. A handler that streams must push the deadline out per frame with an
// http.ResponseController; see the agent-task event stream
// (apps/control-plane/internal/agenttask/stream.go). Anything else added here
// that streams has to do the same, or it silently dies at this number.
const WriteTimeout = 15 * time.Second

// ReadTimeout and IdleTimeout are the other two, named for symmetry so a
// reader does not have to guess which of the three is load bearing.
const (
	ReadTimeout = 15 * time.Second
	IdleTimeout = 60 * time.Second
)

// New builds the public API server with its real timeouts.
func New(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  ReadTimeout,
		WriteTimeout: WriteTimeout,
		IdleTimeout:  IdleTimeout,
	}
}
