package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- synchronous settlement harness ---
//
// A reservation must reach a terminal state exactly once, either charged or
// released, and a failure to charge must not leave it held. The streaming path
// got that guarantee in PR #602; executeSync never did, which is how issue
// #616 accumulated stranded active reservations.

// newAccountingMockSyncFinalizeFails answers finalize with a 500 and every
// other accounting or usage path with 200, and calls onFinalize (if set) before
// answering, so a test can cancel the request context at exactly the moment
// settlement begins.
func newAccountingMockSyncFinalizeFails(rec *accountingRecorder, onFinalize func()) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		if r.URL.Path == "/internal/accounting/reservations/finalize" {
			if onFinalize != nil {
				onFinalize()
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/internal/accounting/reservations":
			_ = json.NewEncoder(w).Encode(ReservationResult{
				ID: "res-test-1", AccountID: "acct-test-1", Status: "active", EstimatedCredits: 10000,
			})
		case "/internal/usage/attempts":
			_ = json.NewEncoder(w).Encode(AttemptResult{
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "accepted",
			})
		}
	}))
}

// newAccountingMockSyncFinalizeSucceedsGated behaves like newAccountingMock
// (every path, including finalize, answers 200) but for the finalize path
// specifically, signals ready then blocks on proceed before answering. That
// gives a test a deterministic happens-before order: cancel the caller's
// context, THEN close proceed, so the mock only writes its response once
// the cancellation is already in effect. Review on #650 found the earlier
// version of this test (cancel() called from inside the handler,
// concurrently with the handler returning) detected the #637 regression
// only 29 times out of 30 -- a genuine race, not a guarantee -- because
// whether the client observed the cancellation before or after it finished
// reading the response depended on goroutine scheduling. The ready/proceed
// gate removes that race entirely.
func newAccountingMockSyncFinalizeSucceedsGated(rec *accountingRecorder, ready chan<- struct{}, proceed <-chan struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		if r.URL.Path == "/internal/accounting/reservations/finalize" {
			close(ready)
			<-proceed
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/internal/accounting/reservations":
			_ = json.NewEncoder(w).Encode(ReservationResult{
				ID: "res-test-1", AccountID: "acct-test-1", Status: "active", EstimatedCredits: 10000,
			})
		case "/internal/usage/attempts":
			_ = json.NewEncoder(w).Encode(AttemptResult{
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "accepted",
			})
		}
	}))
}

func callSyncCtx(orch *Orchestrator, ctx context.Context) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()
	orch.executeSync(ctx, w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o",
		NeedFlags{NeedChatCompletions: true}, 10000, orch.litellm.ChatCompletion, normalizeChatCompletion)
	return w
}

// captureLogs collects everything written to the standard logger while fn runs.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

// TestExecuteSync_FinalizeError_ReleasesReservationAndLogs is the issue #616
// regression guard for the synchronous path: a finalize failure must not both
// lose the charge and strand the hold, and it must not be swallowed silently.
func TestExecuteSync_FinalizeError_ReleasesReservationAndLogs(t *testing.T) {
	var hits int64
	litellmSrv := countingJSONServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMockSyncFinalizeFails(rec, nil)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	logs := captureLogs(t, func() { callSyncCtx(orch, context.Background()) })

	if !rec.has("/internal/accounting/reservations/finalize") {
		t.Fatalf("expected FinalizeReservation to be attempted; calls seen: %+v", rec.calls)
	}
	body, ok := rec.find("/internal/accounting/reservations/release")
	if !ok {
		t.Fatalf("expected a fallback ReleaseReservation when finalize fails, or the hold is stranded; calls seen: %+v", rec.calls)
	}
	if body["reservation_id"] != "res-test-1" {
		t.Errorf("reservation_id = %v, want res-test-1", body["reservation_id"])
	}
	if body["reason"] != "finalize_failed" {
		t.Errorf("reason = %v, want finalize_failed", body["reason"])
	}
	if !strings.Contains(logs, "finalize") {
		t.Errorf("finalize failure was swallowed: no finalize error in logs. logs: %q", logs)
	}
}

// TestExecuteSync_FinalizeError_CancelledContext_StillReleases is the specific
// trap PR #602 hit on the streaming side: the request context is already
// cancelled by settlement time, so a release issued on that context is
// silently swallowed and the hold survives.
func TestExecuteSync_FinalizeError_CancelledContext_StillReleases(t *testing.T) {
	var hits int64
	litellmSrv := countingJSONServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &accountingRecorder{}
	// The client hangs up exactly as settlement starts.
	acctSrv := newAccountingMockSyncFinalizeFails(rec, cancel)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	callSyncCtx(orch, ctx)

	if ctx.Err() == nil {
		t.Fatal("sanity check failed: context was not actually cancelled")
	}
	if _, ok := rec.find("/internal/accounting/reservations/release"); !ok {
		t.Fatalf("expected ReleaseReservation to reach the control plane despite the cancelled request context; calls seen: %+v", rec.calls)
	}
}

// TestExecuteSync_ClientDisconnect_ChargesDeliveredWork is issue #637's
// primary regression guard: unlike the previous test, the control plane's
// finalize handler here SUCCEEDS (200) if it actually receives the call on a
// live context. The client disconnects (its context is cancelled) exactly
// as the finalize request reaches the mock, deterministically: the mock
// signals ready and blocks, the test cancels ctx (synchronous, guaranteed
// complete once it returns), THEN closes proceed so the mock's response is
// only ever written after the cancellation is already in effect. Before
// #637's fix, finalize ran on that same cancelled request context, so
// cancelling it here would make the finalize call fail client-side
// regardless of the mock's 200, falling through to a release and losing
// the charge for genuinely delivered work. After the fix, finalize runs on
// its own independent background context, so cancelling the caller's
// context has no effect on it: the charge must still land.
func TestExecuteSync_ClientDisconnect_ChargesDeliveredWork(t *testing.T) {
	var hits int64
	litellmSrv := countingJSONServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &accountingRecorder{}
	ready := make(chan struct{})
	proceed := make(chan struct{})
	acctSrv := newAccountingMockSyncFinalizeSucceedsGated(rec, ready, proceed)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	done := make(chan struct{})
	go func() {
		defer close(done)
		callSyncCtx(orch, ctx)
	}()

	<-ready
	cancel()
	close(proceed)
	<-done

	if ctx.Err() == nil {
		t.Fatal("sanity check failed: context was not actually cancelled")
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("nothing should be released: content was already delivered and finalize succeeded")
	}
	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation to reach control-plane despite the cancelled request context; calls seen: %+v", rec.calls)
	}
	// 20 input + 5 output tokens at the fixture's pinned hive-fast price, floored at 1
	// credit (see settle_from_catalog_test.go for the same bound at thousands of
	// tokens, where the floor cannot mask a wrong conversion).
	if actual, _ := body["actual_credits"].(float64); int64(actual) != 1 {
		t.Errorf("actual_credits = %v, want 1 (catalog price for the confirmed usage)", body["actual_credits"])
	}
	if body["reservation_id"] != "res-test-1" {
		t.Errorf("reservation_id = %v, want res-test-1", body["reservation_id"])
	}
}

// TestExecuteSync_NormalCompletion_TerminatesExactlyOnce guards the other half
// of the invariant: a successful charge must never also be released, which
// would refund a legitimate charge.
func TestExecuteSync_NormalCompletion_TerminatesExactlyOnce(t *testing.T) {
	var hits int64
	litellmSrv := countingJSONServer(&hits)
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	w := callSyncCtx(orch, context.Background())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation on a normal completion; calls seen: %+v", rec.calls)
	}
	if actual, _ := body["actual_credits"].(float64); int64(actual) != 1 {
		t.Errorf("actual_credits = %v, want 1 (catalog price for the confirmed usage)", body["actual_credits"])
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("a charged reservation must never also be released: that refunds a legitimate charge")
	}
}

// TestExecuteSync_UpstreamError_CancelledContext_StillReleases covers the
// sibling release sites in executeSync (upstream error, read error, normalize
// error), which had the same shape as the finalize path: released on the
// request context, error discarded, finalized set unconditionally.
func TestExecuteSync_UpstreamError_CancelledContext_StillReleases(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Upstream answers 500 and cancels the request context at the same moment.
	litellmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"upstream exploded"}`))
	}))
	defer litellmSrv.Close()
	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)
	callSyncCtx(orch, ctx)

	if rec.has("/internal/accounting/reservations/finalize") {
		t.Error("upstream failed: must not finalize a charge")
	}
	if _, ok := rec.find("/internal/accounting/reservations/release"); !ok {
		t.Fatalf("expected ReleaseReservation despite the cancelled request context; calls seen: %+v", rec.calls)
	}
}
