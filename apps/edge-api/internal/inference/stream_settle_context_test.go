package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
)

// newAccountingMockSlowFinalize answers finalize with a 500 only after
// sleeping, so finalize is SLOW rather than instantly failing, and answers
// every other path 200 immediately.
//
// The slowness is the whole point. Every existing settlement mock fails
// finalize instantly, which always leaves the fallback release plenty of
// budget on a shared deadline, so none of them could ever catch a release
// starved by the finalize that preceded it.
func newAccountingMockSlowFinalize(rec *accountingRecorder, finalizeSleep time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		if r.URL.Path == "/internal/accounting/reservations/finalize" {
			time.Sleep(finalizeSleep)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
}

// TestSettleStream_ReleaseGetsFreshDeadlineAfterSlowFinalize is issue #657.
//
// settleStream built one background context and used it for both the finalize
// call and the fallback release that follows a failed finalize. They shared a
// single deadline, so a finalize that was merely SLOW consumed the whole
// budget and the release then ran on an already-expired context, never left
// the gateway, and stranded the hold. That is #616's failure mode on the
// streaming path, which carries the most traffic.
//
// The assertion is on what the release context did to the release, not on a
// call count: a shared context makes the release fail with context deadline
// exceeded before it is ever sent, which surfaces both as the missing call at
// the control plane and as the fallback-release failure log.
//
// accountingTimeout is shrunk so this proves the shared-versus-fresh property
// in milliseconds rather than waiting out two real 30s budgets. It is read
// when the accounting client is constructed as well as when each settlement
// context is built, so it must be set before NewAccountingClient.
func TestSettleStream_ReleaseGetsFreshDeadlineAfterSlowFinalize(t *testing.T) {
	original := accountingTimeout
	accountingTimeout = 200 * time.Millisecond
	defer func() { accountingTimeout = original }()

	rec := &accountingRecorder{}
	// Finalize takes longer than its own full budget, so it exhausts whatever
	// deadline it is given before failing.
	acctSrv := newAccountingMockSlowFinalize(rec, 400*time.Millisecond)
	defer acctSrv.Close()

	orch := &Orchestrator{accounting: NewAccountingClient(acctSrv.URL)}
	acc := &UsageAccumulator{InputTokens: 20, OutputTokens: 5, TotalTokens: 25, HasUsage: true}

	var settled bool
	logs := captureLogs(t, func() {
		settled = orch.settleStream(
			context.Background(),
			authz.AuthSnapshot{AccountID: "acct-test-1", KeyID: "key-test-1"},
			AttemptResult{ID: "attempt-test-1"},
			ReservationResult{ID: "res-test-1"},
			hiveFastRoute,
			"req-test-1", EndpointChatCompletions, "gpt-4o", 0, acc, `{}`, "hello",
		)
	})

	if !rec.has("/internal/accounting/reservations/finalize") {
		t.Fatalf("expected FinalizeReservation to be attempted; calls seen: %+v", rec.calls)
	}
	if strings.Contains(logs, "finalize-fallback release failed") {
		t.Fatalf("the fallback release ran on finalize's already-expired context instead of a fresh full budget (#657). logs: %q", logs)
	}
	if !rec.has("/internal/accounting/reservations/release") {
		t.Fatalf("the fallback release never reached the control plane, so the hold is stranded (#657, the #616 failure mode). calls seen: %+v, logs: %q", rec.calls, logs)
	}
	if !settled {
		t.Fatal("settleStream reported an unsettled reservation even though the release should have succeeded on its own budget")
	}
}

// TestSettleStream_SlowButSuccessfulFinalize_NeverReleases guards the other
// half of the invariant against this fix. Cancelling finalizeCtx explicitly,
// which the fix does the moment finalize returns, must not create a path where
// a finalize that SUCCEEDED is followed by a release that refunds a real
// charge. The finalize here is slow enough to be interesting but answers 200,
// so nothing may be released.
func TestSettleStream_SlowButSuccessfulFinalize_NeverReleases(t *testing.T) {
	original := accountingTimeout
	accountingTimeout = 2 * time.Second
	defer func() { accountingTimeout = original }()

	rec := &accountingRecorder{}
	acctSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		if r.URL.Path == "/internal/accounting/reservations/finalize" {
			time.Sleep(100 * time.Millisecond)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
	}))
	defer acctSrv.Close()

	orch := &Orchestrator{accounting: NewAccountingClient(acctSrv.URL)}
	acc := &UsageAccumulator{InputTokens: 20, OutputTokens: 5, TotalTokens: 25, HasUsage: true}

	settled := orch.settleStream(
		context.Background(),
		authz.AuthSnapshot{AccountID: "acct-test-1", KeyID: "key-test-1"},
		AttemptResult{ID: "attempt-test-1"},
		ReservationResult{ID: "res-test-1"},
		hiveFastRoute,
		"req-test-1", EndpointChatCompletions, "gpt-4o", 0, acc, `{}`, "hello",
	)

	if !settled {
		t.Fatal("expected a successful finalize to settle the reservation")
	}
	if !rec.has("/internal/accounting/reservations/finalize") {
		t.Fatalf("expected FinalizeReservation to be attempted; calls seen: %+v", rec.calls)
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Fatalf("a charged reservation must never also be released: that refunds a legitimate charge. calls seen: %+v", rec.calls)
	}
}
