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

// Every exit this handler has, and what happens to the hold on each. The hold
// must reach a terminal state EXACTLY once: finalized or released, never both
// and never neither.
func TestRAGChatHoldReachesATerminalStateExactlyOnce(t *testing.T) {
	tests := []struct {
		name          string
		stream        bool
		dispatch      func(t *testing.T) ChatDispatchFunc
		finalizeFails bool
		wantFinalized int
		wantReleased  int
	}{
		{
			name:          "served, non streaming",
			dispatch:      func(*testing.T) ChatDispatchFunc { return fakeDispatch(http.StatusOK, canned200Response, nil) },
			wantFinalized: 1,
			wantReleased:  0,
		},
		{
			name: "upstream answers non 2xx",
			dispatch: func(*testing.T) ChatDispatchFunc {
				return fakeDispatch(http.StatusBadGateway, `{"error":"upstream sad"}`, nil)
			},
			wantFinalized: 0,
			wantReleased:  1,
		},
		{
			name: "dispatch fails in transport",
			dispatch: func(*testing.T) ChatDispatchFunc {
				return fakeDispatch(0, "", fmt.Errorf("connection refused"))
			},
			wantFinalized: 0,
			wantReleased:  1,
		},
		{
			name: "upstream body is not valid json",
			dispatch: func(*testing.T) ChatDispatchFunc {
				return fakeDispatch(http.StatusOK, `not json at all`, nil)
			},
			wantFinalized: 0,
			wantReleased:  1,
		},
		{
			name:          "finalize fails, so the hold is handed back instead",
			dispatch:      func(*testing.T) ChatDispatchFunc { return fakeDispatch(http.StatusOK, canned200Response, nil) },
			finalizeFails: true,
			wantFinalized: 0,
			wantReleased:  1,
		},
		{
			name:   "stream completes",
			stream: true,
			dispatch: func(*testing.T) ChatDispatchFunc {
				return fakeDispatch(http.StatusOK, sseBody(streamUsageFrame, "[DONE]"), nil)
			},
			wantFinalized: 1,
			wantReleased:  0,
		},
		{
			name:   "stream is truncated before DONE",
			stream: true,
			dispatch: func(*testing.T) ChatDispatchFunc {
				return fakeDispatch(http.StatusOK, sseBody(streamUsageFrame), nil)
			},
			wantFinalized: 0,
			wantReleased:  1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			acct := &ragAccounting{}
			if tc.finalizeFails {
				acct.finalizeStatus = http.StatusInternalServerError
			}
			h := newBilledChatHandler(t, acct, billableTenant(),
				fakeSelectRoute("route-test", nil), tc.dispatch(t))

			w := httptest.NewRecorder()
			h.handleChat(w, chatReq(t, ChatRequest{
				Model:    "hive-fast",
				Stream:   tc.stream,
				Messages: []ChatMessage{{Role: "user", Content: "what is the answer"}},
			}, uuid.New()))

			reservations, finalized, released := acct.counts()
			if reservations != 1 {
				t.Fatalf("want exactly one hold taken, got %d", reservations)
			}
			// The finalize counter records ATTEMPTS, and a failing finalize is
			// retried once by design before the hold is handed back, so a
			// failed settlement is asserted as "not charged" rather than by a
			// call count.
			if !tc.finalizeFails && finalized != tc.wantFinalized {
				t.Errorf("finalized = %d, want %d", finalized, tc.wantFinalized)
			}
			if released != tc.wantReleased {
				t.Errorf("released = %d, want %d (a hold released zero times is stranded forever: no expires_at, no reaper, #600)",
					released, tc.wantReleased)
			}
			if tc.wantFinalized == 1 && released != 0 {
				t.Errorf("a charged request also released its hold: that is both terminal states, not one")
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
