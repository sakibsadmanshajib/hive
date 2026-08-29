package rag

// Money-path guards for POST /v1/rag/chat (#669).
//
// Two of these are the guards the issue asks for by name, and they are written
// to fail for the right reason rather than to pass:
//
//   - TestRAGChatRefusesAnAccountThatCannotPay asserts the DISPATCHER IS NEVER
//     CALLED, not merely that the status is 429. A refusal written after the
//     provider was already paid is the exact defect being fixed, and a test
//     that only read the status code would pass through it.
//   - TestRAGChatHoldReachesATerminalStateExactlyOnce enumerates every exit
//     this handler has and asserts the hold is finalized or released exactly
//     once on each. A stranded hold is worse than the free serving being fixed
//     (there is no expires_at and no reaper, #600), so "released" is asserted
//     positively per exit rather than inferred from the absence of a charge.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
)

// --- fakes ---

// ragAccounting is a fake control-plane accounting surface recording every
// call the RAG money path makes.
type ragAccounting struct {
	mu sync.Mutex

	reservationStatus int // non-zero to refuse reservation creation with this status
	finalizeStatus    int // non-zero to fail settlement with this status

	reservations []inference.CreateReservationInput
	finalized    []inference.FinalizeReservationInput
	released     []inference.ReleaseReservationInput
}

func (f *ragAccounting) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(f.mux())
	t.Cleanup(srv.Close)
	return srv
}

func (f *ragAccounting) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/usage/attempts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(inference.AttemptResult{ID: "att_1", Status: "dispatching"})
	})
	mux.HandleFunc("/internal/accounting/reservations", func(w http.ResponseWriter, r *http.Request) {
		var in inference.CreateReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.reservations = append(f.reservations, in)
		status, id := f.reservationStatus, fmt.Sprintf("res_%d", len(f.reservations))
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"reservation exceeds available credits"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(inference.ReservationResult{
			ID: id, AccountID: in.AccountID, Status: "active", ReservedCredits: in.EstimatedCredits,
		})
	})
	mux.HandleFunc("/internal/accounting/reservations/finalize", func(w http.ResponseWriter, r *http.Request) {
		var in inference.FinalizeReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.finalized = append(f.finalized, in)
		status := f.finalizeStatus
		f.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/internal/accounting/reservations/release", func(w http.ResponseWriter, r *http.Request) {
		var in inference.ReleaseReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.released = append(f.released, in)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (f *ragAccounting) counts() (reservations, finalized, released int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reservations), len(f.finalized), len(f.released)
}

// stubRAGBilling is a tenant-to-account map with no database behind it.
type stubRAGBilling struct {
	state metering.TenantBillingState
	err   error
}

func (s stubRAGBilling) ResolveState(context.Context, uuid.UUID) (metering.TenantBillingState, error) {
	return s.state, s.err
}

func billableTenant() stubRAGBilling {
	return stubRAGBilling{state: metering.TenantBillingState{
		AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
	}}
}

// newBilledChatHandler wires the full money path onto a chat-capable handler.
func newBilledChatHandler(t *testing.T, acct *ragAccounting, billing BillingResolver,
	route RouteSelectFunc, dispatch ChatDispatchFunc) *Handler {
	t.Helper()
	store := newFakeStore()
	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits)
	return h.WithChat(route, dispatch).
		WithBilling(inference.NewAccountingClient(acct.server(t).URL), billing)
}

// dispatchThatMustNotRun fails the test if the provider is ever reached.
func dispatchThatMustNotRun(t *testing.T) ChatDispatchFunc {
	t.Helper()
	return func(context.Context, string, []byte) (*http.Response, error) {
		t.Error("provider was dispatched to on a request that should have been refused before any spend")
		return nil, fmt.Errorf("must not dispatch")
	}
}

// --- tests ---

// An account that cannot pay is refused BEFORE the provider is reached. This
// is the guard for the defect in #669: before this, the endpoint took no hold
// at all, so this condition could not fire and the request was served.
func TestRAGChatRefusesAnAccountThatCannotPay(t *testing.T) {
	acct := &ragAccounting{reservationStatus: http.StatusConflict}
	h := newBilledChatHandler(t, acct, billableTenant(),
		fakeSelectRoute("route-test", nil), dispatchThatMustNotRun(t))

	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
	}, uuid.New()))

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body = %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); !strings.Contains(got, "insufficient_quota") {
		t.Errorf("body = %s, want insufficient_quota", got)
	}
	// Provider-blind: a refusal must not name a provider or a route.
	for _, leak := range []string{"route-test", "test-provider"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("refusal leaked %q: %s", leak, w.Body.String())
		}
	}
	_, finalized, released := acct.counts()
	if finalized != 0 || released != 0 {
		t.Errorf("a refused reservation was never created, so nothing may settle: finalized=%d released=%d",
			finalized, released)
	}
}

// A tenant with no billing account is refused, never served free (#721).
func TestRAGChatRefusesTenantWithNoBillingAccount(t *testing.T) {
	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct,
		stubRAGBilling{state: metering.TenantBillingState{Found: false, Deployment: metering.DeploymentHiveCloud}},
		fakeSelectRoute("route-test", nil), dispatchThatMustNotRun(t))

	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New()))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", w.Code, w.Body.String())
	}
}

// A handler built without its accounting seam refuses rather than serving
// inference it cannot charge for. Without this, forgetting one wiring line in
// main.go silently restores the #669 behaviour with no signal at all.
func TestRAGChatRefusesWhenBillingIsNotWired(t *testing.T) {
	store := newFakeStore()
	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits).
		WithChat(fakeSelectRoute("route-test", nil), dispatchThatMustNotRun(t))

	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New()))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
}

// An ENTERPRISE_EDGE tenant has no prepaid relationship with Hive (D-027), so
// it is served with no hold and no charge. That is a recorded verdict, not the
// silence #669 is about.
func TestRAGChatEnterpriseTenantIsNotBilled(t *testing.T) {
	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct,
		stubRAGBilling{state: metering.TenantBillingState{Deployment: metering.DeploymentEnterpriseEdge}},
		fakeSelectRoute("route-test", nil), fakeDispatch(http.StatusOK, canned200Response, nil))

	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	}, uuid.New()))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	reservations, finalized, released := acct.counts()
	if reservations != 0 || finalized != 0 || released != 0 {
		t.Errorf("enterprise tenant must take no hold: reservations=%d finalized=%d released=%d",
			reservations, finalized, released)
	}
}

// sseBody builds an upstream SSE stream from the given frames.
func sseBody(frames ...string) string {
	var b strings.Builder
	for _, f := range frames {
		b.WriteString("data: " + f + "\n\n")
	}
	return b.String()
}

const streamUsageFrame = `{"id":"up-1","choices":[{"index":0,"delta":{"content":"42"},"finish_reason":null}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`

// upstreamCostFrame is the same stream frame carrying the provider-reported
// cost an upstream_actual alias prices from. 0.0004 USD at the standard 7/5
// margin and 1e9 credits per USD (D-046) is 560,000 credits, against a
// 100,000,000 credit hold, so a settlement that fell back to the hold is off
// by roughly 178x and cannot be mistaken for a rounding difference.
const upstreamCostFrame = `{"id":"gen-abc","choices":[{"index":0,"delta":{"content":"42"},"finish_reason":null}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.0004}}`

// upstreamCostBody is the non-streaming equivalent of upstreamCostFrame.
const upstreamCostBody = `{"id":"gen-abc","choices":[{"message":{"role":"assistant","content":"The answer is 42 [1]."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"cost":0.0004}}`

// wantUpstreamActualCredits is 0.0004 x 7/5 x 1e9, computed here rather than
// by calling the production helper so this cannot pass by agreeing with
// itself.
const wantUpstreamActualCredits int64 = 560_000

// cancelOnDrainBody hands out an SSE body once and cancels the request context
// when the reader is drained, which is a client that read every content frame
// and then hung up before [DONE] arrived. Nothing else in this package can
// reach the client-disconnect exit: chatReq roots its context at
// context.Background(), so r.Context().Err() is otherwise always nil.
type cancelOnDrainBody struct {
	r         io.Reader
	cancel    context.CancelFunc
	cancelled bool
}

func (b *cancelOnDrainBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err == io.EOF && !b.cancelled {
		b.cancelled = true
		b.cancel()
	}
	return n, err
}

func (b *cancelOnDrainBody) Close() error { return nil }

// Every exit this handler has, and what happens to the hold on each. The hold
// must reach a terminal state EXACTLY once: finalized or released, never both
// and never neither, and a release is asserted by its RECORDED REASON as well
// as by its count. Asserting the count alone let every releaseReason in the
// handler be deleted with this suite still green, which made the exit-to-reason
// table in the PR description an assertion nothing checked.
func TestRAGChatHoldReachesATerminalStateExactlyOnce(t *testing.T) {
	tests := []struct {
		name          string
		stream        bool
		dispatch      func(t *testing.T, cancel context.CancelFunc) ChatDispatchFunc
		finalizeFails bool
		wantFinalized int
		wantReleased  int
		wantReason    string
	}{
		{
			name: "served, non streaming",
			dispatch: func(*testing.T, context.CancelFunc) ChatDispatchFunc {
				return fakeDispatch(http.StatusOK, canned200Response, nil)
			},
			wantFinalized: 1,
			wantReleased:  0,
		},
		{
			name: "upstream answers non 2xx",
			dispatch: func(*testing.T, context.CancelFunc) ChatDispatchFunc {
				return fakeDispatch(http.StatusBadGateway, `{"error":"upstream sad"}`, nil)
			},
			wantFinalized: 0,
			wantReleased:  1,
			wantReason:    "upstream_error",
		},
		{
			name: "dispatch fails in transport",
			dispatch: func(*testing.T, context.CancelFunc) ChatDispatchFunc {
				return fakeDispatch(0, "", fmt.Errorf("connection refused"))
			},
			wantFinalized: 0,
			wantReleased:  1,
			wantReason:    "upstream_error",
		},
		{
			name: "upstream body is not valid json",
			dispatch: func(*testing.T, context.CancelFunc) ChatDispatchFunc {
				return fakeDispatch(http.StatusOK, `not json at all`, nil)
			},
			wantFinalized: 0,
			wantReleased:  1,
			wantReason:    "normalize_error",
		},
		{
			name: "finalize fails, so the hold is handed back instead",
			dispatch: func(*testing.T, context.CancelFunc) ChatDispatchFunc {
				return fakeDispatch(http.StatusOK, canned200Response, nil)
			},
			finalizeFails: true,
			// Two attempts, not zero: Finalize retries once by design before
			// giving the hold back. The old expectation of 0 was never
			// compared to anything, so it read as an assertion and was not one.
			wantFinalized: 2,
			wantReleased:  1,
			wantReason:    "finalize_failed",
		},
		{
			name:   "stream completes",
			stream: true,
			dispatch: func(*testing.T, context.CancelFunc) ChatDispatchFunc {
				return fakeDispatch(http.StatusOK, sseBody(streamUsageFrame, "[DONE]"), nil)
			},
			wantFinalized: 1,
			wantReleased:  0,
		},
		{
			name:   "stream ends before delivering anything",
			stream: true,
			dispatch: func(*testing.T, context.CancelFunc) ChatDispatchFunc {
				return fakeDispatch(http.StatusOK, "", nil)
			},
			wantFinalized: 0,
			wantReleased:  1,
			wantReason:    "upstream_error",
		},
		{
			// The generation reached the customer and Hive has already paid
			// the provider for it, so a stream that stopped short of [DONE]
			// settles at what it delivered. Releasing here is a free serve
			// (D-055: no served request bills zero).
			name:   "stream is truncated after delivering content",
			stream: true,
			dispatch: func(*testing.T, context.CancelFunc) ChatDispatchFunc {
				return fakeDispatch(http.StatusOK, sseBody(streamUsageFrame), nil)
			},
			wantFinalized: 1,
			wantReleased:  0,
		},
		{
			// The caller-controlled version of the same shape, and the
			// exploitable one: read every content frame, hang up before
			// [DONE], pay nothing, repeat.
			name:   "client hangs up after the content, before DONE",
			stream: true,
			dispatch: func(_ *testing.T, cancel context.CancelFunc) ChatDispatchFunc {
				return func(context.Context, string, []byte) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body: &cancelOnDrainBody{
							r: strings.NewReader(sseBody(streamUsageFrame)), cancel: cancel,
						},
					}, nil
				}
			},
			wantFinalized: 1,
			wantReleased:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			acct := &ragAccounting{}
			if tc.finalizeFails {
				acct.finalizeStatus = http.StatusInternalServerError
			}
			ctx, cancel := context.WithCancel(userCtx(uuid.New()))
			defer cancel()
			h := newBilledChatHandler(t, acct, billableTenant(),
				fakeSelectRoute("route-test", nil), tc.dispatch(t, cancel))

			w := httptest.NewRecorder()
			h.handleChat(w, chatReqCtx(t, ChatRequest{
				Model:    "hive-fast",
				Stream:   tc.stream,
				Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
			}, ctx))

			reservations, finalized, released := acct.counts()
			if reservations != 1 {
				t.Fatalf("want exactly one hold taken, got %d", reservations)
			}
			if finalized != tc.wantFinalized {
				t.Errorf("finalize attempts = %d, want %d", finalized, tc.wantFinalized)
			}
			if released != tc.wantReleased {
				t.Errorf("released = %d, want %d (a hold released zero times is stranded forever: no expires_at, no reaper, #600)",
					released, tc.wantReleased)
			}
			if tc.wantReleased > 0 {
				acct.mu.Lock()
				gotReason := acct.released[0].Reason
				acct.mu.Unlock()
				if gotReason != tc.wantReason {
					t.Errorf("release reason = %q, want %q", gotReason, tc.wantReason)
				}
			}
			if tc.wantReleased == 0 && tc.wantFinalized > 0 && released != 0 {
				t.Errorf("a charged request also released its hold: that is both terminal states, not one")
			}
		})
	}
}

// An upstream_actual alias (hive-auto, D-059) carries no catalog token price,
// so its charge is the cost the upstream reported for this generation times
// the standard margin. Settling at the hold instead overcharges a sub-cent
// request by two orders of magnitude and flags it unconfirmed, which is the
// same defect issue #1198 records on another path.
//
// Both paths are covered because they read the usage block from different
// bytes: the non-streaming one from the raw upstream body, the streaming one
// from a frame the sanitizer has already stripped the cost out of.
func TestRAGChatSettlesAnUpstreamActualAliasAtTheReportedCost(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stream bool
		body   string
	}{
		{name: "non streaming", body: upstreamCostBody},
		{name: "streaming", stream: true, body: sseBody(upstreamCostFrame, "[DONE]")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acct := &ragAccounting{}
			h := newBilledChatHandler(t, acct, billableTenant(),
				upstreamActualSelectRoute("route-test"),
				fakeDispatch(http.StatusOK, tc.body, nil))

			w := httptest.NewRecorder()
			h.handleChat(w, chatReq(t, ChatRequest{
				Model:    "hive-auto",
				Stream:   tc.stream,
				Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
			}, uuid.New()))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
			}

			acct.mu.Lock()
			defer acct.mu.Unlock()
			if len(acct.finalized) != 1 {
				t.Fatalf("want exactly one settlement, got %d", len(acct.finalized))
			}
			got := acct.finalized[0]
			if got.ActualCredits != wantUpstreamActualCredits {
				t.Errorf("charged %d credits, want %d (settling at the hold, %d, is the defect)",
					got.ActualCredits, wantUpstreamActualCredits, inference.DefaultHoldText)
			}
			if !got.TerminalUsageConfirmed {
				t.Error("a settlement priced from a reported upstream cost is confirmed, not an estimate")
			}
			// The upstream cost and the upstream generation id are ours, never
			// the customer's.
			for _, leak := range []string{"cost", "gen-abc", "test-provider"} {
				if strings.Contains(w.Body.String(), leak) {
					t.Errorf("response leaked %q: %s", leak, w.Body.String())
				}
			}
		})
	}
}

// The charge is the catalog price of the tokens actually metered, at the
// alias's own rate. Recomputed here independently rather than by calling the
// production helper, so this cannot pass by agreeing with itself.
func TestRAGChatSettlesAtTheCatalogMagnitude(t *testing.T) {
	const inPrice, outPrice int64 = 300_000, 1_200_000
	acct := &ragAccounting{}
	h := newBilledChatHandler(t, acct, billableTenant(),
		pricedSelectRoute("route-test", inPrice, outPrice),
		fakeDispatch(http.StatusOK, canned200Response, nil))

	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-fast",
		Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
	}, uuid.New()))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}

	acct.mu.Lock()
	defer acct.mu.Unlock()
	if len(acct.finalized) != 1 {
		t.Fatalf("want exactly one settlement, got %d", len(acct.finalized))
	}
	got := acct.finalized[0]
	// canned200Response reports 10 prompt and 5 completion tokens.
	want := (10*inPrice + 5*outPrice + 500_000) / 1_000_000
	if got.ActualCredits != want {
		t.Errorf("charged %d credits, want %d", got.ActualCredits, want)
	}
	if !got.TerminalUsageConfirmed {
		t.Error("a provider usage block with real token counts must settle as confirmed")
	}
	if got.InputTokens != 10 || got.OutputTokens != 5 {
		t.Errorf("settled tokens = (%d, %d), want (10, 5)", got.InputTokens, got.OutputTokens)
	}
}

// The variable-price size ceiling is enforced on the body this handler
// DISPATCHES, which on this endpoint is the customer's messages plus the
// grounding block built from retrieved chunks. That is deliberate: the hold is
// sized from the same bytes, and a hold sized from a body smaller than the one
// sent upstream is a number with nothing behind it (issue #1372). The
// consequence is that retrieval can push a short question over the ceiling,
// which is a refusal before any spend rather than an unprovable hold.
func TestRAGChatRefusesWhenRetrievedContextCrossesTheVariablePriceCeiling(t *testing.T) {
	store := newFakeStore()
	store.chunks = []ChunkRow{{
		ID:         uuid.New(),
		DocumentID: uuid.New(),
		Content:    strings.Repeat("x", inference.VariablePriceMaxRequestBytes+1),
		Score:      0.1,
	}}
	acct := &ragAccounting{}
	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits).
		WithChat(upstreamActualSelectRoute("route-test"), dispatchThatMustNotRun(t)).
		WithBilling(inference.NewAccountingClient(acct.server(t).URL), billableTenant())

	w := httptest.NewRecorder()
	h.handleChat(w, chatReq(t, ChatRequest{
		Model:    "hive-auto",
		TopK:     1,
		Messages: []ChatMessage{{Role: "user", Content: "short question"}},
	}, uuid.New()))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); !strings.Contains(got, "context_length_exceeded") {
		t.Errorf("body = %s, want context_length_exceeded", got)
	}
	if reservations, _, _ := acct.counts(); reservations != 0 {
		t.Errorf("a refused request took %d holds; the bound runs before the hold", reservations)
	}
}
