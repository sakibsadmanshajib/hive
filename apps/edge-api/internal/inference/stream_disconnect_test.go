package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
)

// --- disconnect-settlement test harness ---
//
// These tests exercise the real executeStreaming/executeResponsesStreaming
// lifecycle end-to-end against in-process httptest stand-ins for the
// control-plane (routing + accounting) and LiteLLM, so the regression guard
// is the real defer/context wiring, not a hand-simulated approximation of it.

// recordedCall is one POST the edge-api sent to a control-plane internal
// endpoint, captured by path and decoded JSON body.
type recordedCall struct {
	path string
	body map[string]any
}

// accountingRecorder captures every call edge-api's AccountingClient makes,
// so a test can assert exactly what settlement decided (finalize vs release,
// actual_credits, terminal_usage_confirmed) without a real database.
type accountingRecorder struct {
	mu    sync.Mutex
	calls []recordedCall
}

func (r *accountingRecorder) record(path string, body map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, recordedCall{path: path, body: body})
}

func (r *accountingRecorder) find(path string) (map[string]any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c.path == path {
			return c.body, true
		}
	}
	return nil, false
}

func (r *accountingRecorder) has(path string) bool {
	_, ok := r.find(path)
	return ok
}

// newAccountingMock stands in for the control-plane's internal accounting
// and usage endpoints. It always answers 200 OK: this test isolates what
// edge-api *decides to send*, not control-plane's own ledger business logic
// (covered separately by the control-plane accounting service tests).
func newAccountingMock(rec *accountingRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/internal/accounting/reservations":
			_ = json.NewEncoder(w).Encode(ReservationResult{
				ID: "res-test-1", AccountID: "acct-test-1", Status: "active", EstimatedCredits: 10000,
			})
		case "/internal/usage/attempts":
			_ = json.NewEncoder(w).Encode(AttemptResult{
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "streaming",
			})
		}
	}))
}

// newAccountingMockFinalizeFails behaves like newAccountingMock but answers
// every finalize call with a 500, so a test can exercise the finalize-error
// fallback path (settleStream must release the reservation instead of
// stranding the hold when the finalize call itself fails).
func newAccountingMockFinalizeFails(rec *accountingRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		if r.URL.Path == "/internal/accounting/reservations/finalize" {
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
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "streaming",
			})
		}
	}))
}

// newAccountingMockReservationFails behaves like newAccountingMock but
// answers every CreateReservation call with a 500, the transient
// control-plane failure that stream.go's step 5 explicitly tolerates
// (reservation.ID stays "" and the request proceeds). A test can use this to
// exercise the reservation.ID == "" branches of settleStream.
func newAccountingMockReservationFails(rec *accountingRecorder) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.record(r.URL.Path, body)
		if r.URL.Path == "/internal/accounting/reservations" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Path == "/internal/usage/attempts" {
			_ = json.NewEncoder(w).Encode(AttemptResult{
				ID: "attempt-test-1", RequestID: "req-test-1", Status: "streaming",
			})
		}
	}))
}

// newRoutingMock stands in for the control-plane's route-selection endpoint,
// always resolving to litellmURL regardless of the request body.
//
// It carries hive-fast's catalog price and an explicit token unit because the
// real endpoint always does: routing.Service refuses an alias with no usable
// price (#617) and every selection has carried price_unit since #627. A
// settlement is derived from those fields (#688), so a mock without them would
// exercise a payload production cannot produce.
func newRoutingMock(litellmURL string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(SelectRouteResult{
			AliasID:          "gpt-4o",
			RouteID:          "route-test-1",
			LiteLLMModelName: "openrouter/openai/gpt-4o",
			Provider:         "openrouter",
			Pricing:          catalogHiveFast,
			PriceUnit:        PriceUnitTokens,
		})
	}))
}

// newAuthorizedOrchestrator wires an Orchestrator whose authorizer always
// admits the request via authz.Client.ResolveOverride (the existing test
// seam for bypassing Redis/control-plane I/O) and whose routing/accounting/
// litellm clients point at the given mock servers.
func newAuthorizedOrchestrator(acctURL, routingURL, litellmURL string) *Orchestrator {
	client := &authz.Client{
		ResolveOverride: func(_ context.Context, _ string) (authz.AuthSnapshot, error) {
			return authz.AuthSnapshot{
				KeyID:          "key-test-1",
				AccountID:      "acct-test-1",
				TenantID:       "11111111-1111-1111-1111-111111111111",
				Status:         "active",
				AllowAllModels: true,
				BudgetKind:     "none",
			}, nil
		},
	}
	return &Orchestrator{
		authorizer: authz.NewAuthorizer(client, nil),
		routing:    NewRoutingClient(routingURL),
		accounting: NewAccountingClient(acctURL),
		litellm:    NewLiteLLMClient(litellmURL, "test-key"),
	}
}

// gatedSSEServer streams firstChunk (if non-empty), signals ready, then
// blocks until the request context the client used to fetch it is
// cancelled -- the same shape as an upstream that keeps a generation open
// after Hive's own client has abandoned the request. ready lets the test
// cancel deterministically instead of racing a sleep against a goroutine.
func gatedSSEServer(firstChunk string, ready chan<- struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		// Always flush right after the status line, even with no content:
		// otherwise headers can still be sitting in a buffer, unsent, when the
		// test cancels below -- racing the dispatch call itself (which would
		// then fail before the SSE loop even starts) instead of the mid-loop
		// disconnect this harness means to simulate.
		flusher.Flush()
		if firstChunk != "" {
			fmt.Fprintln(w, buildChunkLine("chunk-1", "route", firstChunk, nil))
			flusher.Flush()
			// Give the client's own scan loop -- a separate goroutine -- a
			// deterministic window to actually consume these already-flushed
			// bytes before ready fires. Flushing here only proves the server
			// sent them; without this, the test can cancel before the
			// client-side goroutine gets scheduled to read them, cutting the
			// stream off before anything reaches the accumulator even though
			// the bytes were on the wire.
			time.Sleep(50 * time.Millisecond)
		}
		close(ready)
		// Prefer reacting to the request context closing (the realistic
		// signal: the client gave up and the connection tore down), but
		// never block the handler -- and therefore httptest.Server.Close --
		// indefinitely if this sandbox's loopback networking doesn't
		// propagate that promptly.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
}

// neverRespondingServer signals ready as soon as it receives the request (so
// a test knows dispatch is in flight against it), then blocks without ever
// writing a response -- the shape of an upstream stuck in prefill. It reacts
// to the request context closing (the client gave up mid-dispatch, before
// any bytes came back) or a hard timeout so it can never hang the test.
func neverRespondingServer(ready chan<- struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(ready)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
}

// abruptUpstreamCloseServer commits SSE headers then closes the connection
// immediately, sending no content, no usage block, and no [DONE] -- an
// upstream provider dying mid-stream while the client is still connected,
// as opposed to the client disconnecting itself.
func abruptUpstreamCloseServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
	}))
}

// completingSSEServer streams a full, ordinary completion (content, a
// finish_reason, a terminal usage chunk, [DONE]) with no disconnect at all.
// Used as the no-regression control case for normal completions.
func completingSSEServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		stop := "stop"
		fmt.Fprintln(w, buildChunkLine("chunk-1", "route", "hello there", nil))
		flusher.Flush()
		fmt.Fprintln(w, buildChunkLine("chunk-2", "route", "", &stop))
		flusher.Flush()
		usageChunk := ChatCompletionChunk{
			ID:      "chunk-3",
			Object:  "chat.completion.chunk",
			Created: 1700000000,
			Model:   "route",
			Choices: []ChunkChoice{},
			Usage: &UsageResponse{
				PromptTokens:     20,
				CompletionTokens: 5,
				TotalTokens:      25,
			},
		}
		b, _ := json.Marshal(usageChunk)
		fmt.Fprintln(w, "data: "+string(b))
		flusher.Flush()
		fmt.Fprintln(w, "data: [DONE]")
		flusher.Flush()
	}))
}

// headerCommitRecorder wraps httptest.ResponseRecorder and closes committed
// exactly once, the first time WriteHeader is called. executeStreaming only
// calls WriteHeader on the outer response after dispatch has already
// succeeded and the upstream status + Flusher checks have passed, so
// waiting on committed gives a race-free "the SSE loop is about to start"
// signal -- unlike waiting on the mock server alone, which only proves the
// server *sent* bytes, not that this process's dispatch call already
// returned.
type headerCommitRecorder struct {
	*httptest.ResponseRecorder
	once      sync.Once
	committed chan struct{}
}

func newHeaderCommitRecorder() *headerCommitRecorder {
	return &headerCommitRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		committed:        make(chan struct{}),
	}
}

func (r *headerCommitRecorder) WriteHeader(code int) {
	r.ResponseRecorder.WriteHeader(code)
	r.once.Do(func() { close(r.committed) })
}

func runExecuteStreaming(orch *Orchestrator, ctx context.Context) (done <-chan struct{}, committed <-chan struct{}) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test-token")
	w := newHeaderCommitRecorder()
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		_ = orch.executeStreaming(ctx, w, req, EndpointChatCompletions, []byte(`{}`), "gpt-4o", "gpt-4o",
			NeedFlags{NeedChatCompletions: true, NeedStreaming: true}, 10000, false, nil, orch.litellm.ChatCompletion)
	}()
	return doneCh, w.committed
}

func waitReady(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream mock never became ready")
	}
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("executeStreaming did not return in time")
	}
}

// TestExecuteStreaming_ClientDisconnect_SettlesDeliveredTokensDespiteCancelledContext
// is the primary regression guard: it reproduces the exact bug shape from the
// investigation (dispatch and settlement sharing a request context that the
// client disconnect cancels) and asserts that settlement still reaches the
// control-plane, charging for what was actually delivered -- not the flat
// 10000 estimate, and not zero.
func TestExecuteStreaming_ClientDisconnect_SettlesDeliveredTokensDespiteCancelledContext(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	ready := make(chan struct{})
	litellmSrv := gatedSSEServer("here is a partial reply the client never received in full", ready)
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done, _ := runExecuteStreaming(orch, ctx)
	waitReady(t, ready)

	// Simulate the client disconnect: cancel the exact context executeStreaming
	// was called with, the same context dispatch used to reach litellmSrv.
	cancel()
	waitDone(t, done)

	if ctx.Err() == nil {
		t.Fatal("sanity check failed: context was not actually cancelled")
	}

	if rec.has("/internal/accounting/reservations/release") {
		t.Error("nothing should be released in full: content was delivered")
	}

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation to reach control-plane despite the cancelled context; calls seen: %+v", rec.calls)
	}

	actual, _ := body["actual_credits"].(float64)
	if actual <= 0 {
		t.Errorf("actual_credits = %v, want > 0 (content was delivered)", body["actual_credits"])
	}
	if int64(actual) == 10000 {
		t.Error("actual_credits must not fall back to the flat 10000 estimate")
	}
	if confirmed, _ := body["terminal_usage_confirmed"].(bool); confirmed {
		t.Error("terminal_usage_confirmed must be false: upstream never sent a real usage block")
	}
	if body["reservation_id"] != "res-test-1" {
		t.Errorf("reservation_id = %v, want res-test-1", body["reservation_id"])
	}
}

// TestExecuteStreaming_ClientDisconnect_NothingDelivered_ReleasesInFull covers
// the opposite outcome required by the ruling: when a disconnect happens
// before any content reaches the accumulator, the hold must be released in
// full and nothing charged.
func TestExecuteStreaming_ClientDisconnect_NothingDelivered_ReleasesInFull(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	ready := make(chan struct{})
	litellmSrv := gatedSSEServer("", ready) // no content at all before disconnect
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done, committed := runExecuteStreaming(orch, ctx)
	waitReady(t, ready)
	// Wait for executeStreaming's own dispatch to have already succeeded
	// (see headerCommitRecorder) before cancelling: with no content chunk to
	// anchor timing on, waiting on the mock server alone would race the
	// dispatch call itself instead of the SSE-loop-active disconnect this
	// test means to simulate.
	select {
	case <-committed:
	case <-time.After(5 * time.Second):
		t.Fatal("executeStreaming never committed SSE response headers")
	}
	cancel()
	waitDone(t, done)

	if rec.has("/internal/accounting/reservations/finalize") {
		t.Error("nothing was delivered: must not finalize a charge")
	}

	body, ok := rec.find("/internal/accounting/reservations/release")
	if !ok {
		t.Fatalf("expected ReleaseReservation (full release) when nothing was delivered; calls seen: %+v", rec.calls)
	}
	if body["reservation_id"] != "res-test-1" {
		t.Errorf("reservation_id = %v, want res-test-1", body["reservation_id"])
	}
	if body["reason"] != "client_disconnect" {
		t.Errorf("reason = %v, want client_disconnect", body["reason"])
	}
}

// TestExecuteStreaming_NormalCompletion_ChargesConfirmedUsage_NoRegression is
// the no-regression control: an ordinary completion (no disconnect) must
// still finalize using the upstream-confirmed usage tokens, exactly as
// before the fix.
func TestExecuteStreaming_NormalCompletion_ChargesConfirmedUsage_NoRegression(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := completingSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)

	body, ok := rec.find("/internal/accounting/reservations/finalize")
	if !ok {
		t.Fatalf("expected FinalizeReservation on normal completion; calls seen: %+v", rec.calls)
	}
	actual, _ := body["actual_credits"].(float64)
	// 20 input + 5 output tokens at hive-fast's catalog price is 0.42 credits,
	// which the never-free floor lifts to 1. The catalog-price bound itself is
	// asserted at thousands of tokens in settle_from_catalog_test.go, where the
	// floor cannot mask a wrong conversion.
	if int64(actual) != 1 {
		t.Errorf("actual_credits = %v, want 1 (catalog price for 25 confirmed tokens, floored)", body["actual_credits"])
	}
	if int64(actual) == 25 {
		t.Error("actual_credits = 25: the raw token count, not a catalog-derived charge (#688)")
	}
	if confirmed, _ := body["terminal_usage_confirmed"].(bool); !confirmed {
		t.Error("terminal_usage_confirmed must be true: upstream sent a real usage block")
	}
	if rec.has("/internal/accounting/reservations/release") {
		t.Error("normal completion must not release; it must finalize")
	}
}

// TestExecuteStreaming_FinalizeError_ReleasesReservationInstead is the
// regression guard for PR #602 finding 2: a failed FinalizeReservation call
// must not leave the hold stranded. settleStream must fall back to a full
// release so the customer's credits are freed rather than locked forever
// behind a charge that never landed.
func TestExecuteStreaming_FinalizeError_ReleasesReservationInstead(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMockFinalizeFails(rec)
	defer acctSrv.Close()

	litellmSrv := completingSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)

	if !rec.has("/internal/accounting/reservations/finalize") {
		t.Fatalf("expected FinalizeReservation to be attempted; calls seen: %+v", rec.calls)
	}

	body, ok := rec.find("/internal/accounting/reservations/release")
	if !ok {
		t.Fatalf("expected fallback ReleaseReservation when finalize fails; calls seen: %+v", rec.calls)
	}
	if body["reason"] != "finalize_failed" {
		t.Errorf("reason = %v, want finalize_failed", body["reason"])
	}
	if body["reservation_id"] != "res-test-1" {
		t.Errorf("reservation_id = %v, want res-test-1", body["reservation_id"])
	}
}

// TestExecuteStreaming_UpstreamClosesWithoutDelivery_ReleasesAsUpstreamError
// is the regression guard for PR #602 finding 3: when nothing is delivered
// because the upstream provider ended the stream early -- not because the
// client disconnected -- the release reason must say so instead of always
// claiming client_disconnect.
func TestExecuteStreaming_UpstreamClosesWithoutDelivery_ReleasesAsUpstreamError(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	litellmSrv := abruptUpstreamCloseServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	// Never cancelled: the client stays connected for the whole request.
	// Only the upstream provider ends the stream early.
	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)

	body, ok := rec.find("/internal/accounting/reservations/release")
	if !ok {
		t.Fatalf("expected ReleaseReservation when nothing was delivered; calls seen: %+v", rec.calls)
	}
	if body["reason"] != "upstream_error" {
		t.Errorf("reason = %v, want upstream_error (client never disconnected)", body["reason"])
	}
}

// TestExecuteStreaming_ClientDisconnect_DuringDispatch_ReleasesReservation is
// the regression guard for the second-round PR #602 finding: dispatchWithRetry
// itself failing because the client cancelled the request context mid-prefill
// (before any bytes ever came back from the upstream). The old code released
// on that same cancelled context, discarded the error, and still set
// finalized = true, stranding the hold -- exactly the bug this whole PR
// exists to fix, just one step earlier in the lifecycle. releaseReservationBackground
// must release on its own background context so the hold actually clears.
func TestExecuteStreaming_ClientDisconnect_DuringDispatch_ReleasesReservation(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMock(rec)
	defer acctSrv.Close()

	ready := make(chan struct{})
	litellmSrv := neverRespondingServer(ready)
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done, _ := runExecuteStreaming(orch, ctx)
	waitReady(t, ready) // dispatch is in flight against the upstream mock
	cancel()            // client disconnects before dispatch ever gets a response
	waitDone(t, done)

	if rec.has("/internal/accounting/reservations/finalize") {
		t.Error("dispatch never succeeded: must not finalize a charge")
	}

	body, ok := rec.find("/internal/accounting/reservations/release")
	if !ok {
		t.Fatalf("expected ReleaseReservation despite the cancelled dispatch context; calls seen: %+v", rec.calls)
	}
	if body["reservation_id"] != "res-test-1" {
		t.Errorf("reservation_id = %v, want res-test-1", body["reservation_id"])
	}
	if body["reason"] != "upstream_error" {
		t.Errorf("reason = %v, want upstream_error", body["reason"])
	}
}

// TestExecuteStreaming_NoReservation_StillRecordsCompletedUsageEvent is the
// regression guard for the second-round PR #602 finding that recordCompletedEvent
// was gated on reservation.ID != "": when CreateReservation fails non-fatally
// (a transient control-plane error stream.go explicitly tolerates so the
// request can still complete), a normal completion must still record its
// usage event instead of silently dropping the telemetry.
func TestExecuteStreaming_NoReservation_StillRecordsCompletedUsageEvent(t *testing.T) {
	rec := &accountingRecorder{}
	acctSrv := newAccountingMockReservationFails(rec)
	defer acctSrv.Close()

	litellmSrv := completingSSEServer()
	defer litellmSrv.Close()

	routingSrv := newRoutingMock(litellmSrv.URL)
	defer routingSrv.Close()

	orch := newAuthorizedOrchestrator(acctSrv.URL, routingSrv.URL, litellmSrv.URL)

	done, _ := runExecuteStreaming(orch, context.Background())
	waitDone(t, done)

	if rec.has("/internal/accounting/reservations/finalize") {
		t.Error("no reservation was ever created: nothing to finalize")
	}

	body, ok := rec.find("/internal/usage/events")
	if !ok {
		t.Fatalf("expected the completed usage event to still be recorded even with no reservation; calls seen: %+v", rec.calls)
	}
	if body["event_type"] != "completed" {
		t.Errorf("event_type = %v, want completed", body["event_type"])
	}
}
