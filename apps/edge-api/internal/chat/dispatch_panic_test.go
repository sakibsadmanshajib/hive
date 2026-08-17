package chat_test

// Panic-survives-defer regression guard for the session chat dispatch path.
//
// dispatch.go registers its reservation release via `defer func() { ... }()`
// immediately after the hold is taken, specifically so a client disconnect,
// an upstream drop, or any other abnormal exit still frees the customer's
// credits (#616, #746). Go's defer semantics already guarantee this holds
// even across a panic unwind, but nothing in the existing suite exercises
// that: every other test in this package returns normally or via a plain
// error path. A refactor that swapped the deferred release for an
// if-err-then-release check at the tail of ServeHTTP would drop this
// protection silently -- every other test would stay green, because none of
// them panics mid-dispatch -- and a request that happened to panic in
// production (a nil-pointer bug in response parsing, say) would strand the
// hold forever with no test catching the regression. This proves the
// structural guarantee directly rather than trusting it never gets refactored
// away.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// panicOnBodyReadTransport performs the real round trip to the fake upstream,
// then swaps the response body for one that panics on its first Read call.
// The panic therefore surfaces exactly where dispatch.go's SSE scanner reads
// the upstream body: deep inside ServeHTTP, well after the reservation hold
// and its deferred release are already registered, and before settled is ever
// set true.
type panicOnBodyReadTransport struct {
	inner http.RoundTripper
}

func (t panicOnBodyReadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	_ = resp.Body.Close()
	resp.Body = panicReadCloser{}
	return resp, nil
}

type panicReadCloser struct{}

func (panicReadCloser) Read(_ []byte) (int, error) {
	panic("simulated: upstream response body panicked mid-read")
}

func (panicReadCloser) Close() error { return nil }

func TestDispatchReleasesReservationEvenWhenTheResponseBodyPanics(t *testing.T) {
	// The real body upstream sends is irrelevant: panicOnBodyReadTransport
	// discards it and substitutes a reader that panics on first Read. Only
	// the status code and headers dispatch.go inspects before reading the
	// body (200, text/event-stream) need to be real.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	}))
	t.Cleanup(upstream.Close)

	tenantID := uuid.New()
	userID := uuid.New()

	fake := &fakeAccounting{}
	accounting := inference.NewAccountingClient(fake.server(t).URL)
	billing := stubBilling{state: metering.TenantBillingState{
		AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
	}}

	handler := chat.NewDispatch(chat.Deps{
		Routing:    pricedRouting(t, "route-hive-fast", 10_500, 42_000),
		Accounting: accounting,
		Billing:    billing,
		LiteLLMURL: upstream.URL,
		DeploySHA:  "test",
		Env:        "test",
		HTTP:       &http.Client{Transport: panicOnBodyReadTransport{inner: http.DefaultTransport}},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"hive-fast","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: userID, TenantID: tenantID, Role: "member", Email: "member@example.test",
	}))
	rec := httptest.NewRecorder()

	func() {
		defer func() {
			r := recover()
			require.NotNil(t, r, "expected the simulated body-read panic to propagate out of ServeHTTP; if it did not, this test no longer exercises the panic path it exists to guard")
		}()
		handler.ServeHTTP(rec, req)
	}()

	_, finalized, released := fake.calls()
	require.Empty(t, finalized, "a request that panicked mid-stream produced nothing billable; it must not be finalized as a charge")
	require.Len(t, released, 1, "reservation must be released even though the handler panicked before reaching its normal settlement path")
}
