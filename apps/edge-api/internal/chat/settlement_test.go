package chat_test

// Settlement tests for JWT-session chat (#746, #856).
//
// These assert the MAGNITUDE of the charge, not that a row appeared (#819). A
// test that only checked "a settlement happened" would have passed throughout
// the five days this path served production traffic for free, and through
// every one of the price corrections that preceded #697.
//
// No database: the handler tolerates a nil pool, so these run in the plain
// `go test ./... -short` step rather than only in the DB-backed one.

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
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
	"github.com/stretchr/testify/require"
)

// expectedCredits recomputes a charge from a catalog row the way the published
// price is defined: credits per MILLION metered units, both components summed
// before the single division, rounded half up (D-031).
//
// It is deliberately a second, independent implementation. Asserting against
// inference.CreditsForTokens would only prove the production helper equals
// itself, which is exactly the shape of green signal #819 was filed about.
func expectedCredits(inTokens, outTokens, inPricePerMillion, outPricePerMillion int64) int64 {
	numerator := inTokens*inPricePerMillion + outTokens*outPricePerMillion
	return (numerator + 500_000) / 1_000_000
}

// fakeAccounting is a fake control-plane accounting surface recording every
// call edge-api makes on the money path.
type fakeAccounting struct {
	mu sync.Mutex

	reservationStatus int // non-zero to refuse reservation creation with this status
	finalizeStatus    int // non-zero to fail settlement with this status

	attempts     []inference.StartAttemptInput
	reservations []inference.CreateReservationInput
	finalized    []inference.FinalizeReservationInput
	released     []inference.ReleaseReservationInput
}

func (f *fakeAccounting) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/usage/attempts", func(w http.ResponseWriter, r *http.Request) {
		var in inference.StartAttemptInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.attempts = append(f.attempts, in)
		f.mu.Unlock()
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
			ID: id, AccountID: in.AccountID, Status: "active", EstimatedCredits: in.EstimatedCredits,
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
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeAccounting) calls() (reservations []inference.CreateReservationInput,
	finalized []inference.FinalizeReservationInput, released []inference.ReleaseReservationInput) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reservations, f.finalized, f.released
}

// stubBilling is a tenant-to-account map with no database behind it.
type stubBilling struct {
	state metering.TenantBillingState
	err   error
}

func (s stubBilling) ResolveState(ctx context.Context, tenantID uuid.UUID) (metering.TenantBillingState, error) {
	return s.state, s.err
}

// billedDeps returns the accounting seam every session request now needs, for
// tests whose subject is something other than the charge itself.
func billedDeps(t *testing.T) (*inference.AccountingClient, chat.BillingResolver) {
	t.Helper()
	accounting := &fakeAccounting{}
	return inference.NewAccountingClient(accounting.server(t).URL), stubBilling{
		state: metering.TenantBillingState{
			AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
		},
	}
}

// pricedRouting resolves any alias to itself at the supplied catalog price.
func pricedRouting(t *testing.T, litellmModel string, inPrice, outPrice int64) *inference.RoutingClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in inference.SelectRouteInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          in.AliasID,
			LiteLLMModelName: litellmModel,
			Provider:         "test-provider",
			Pricing: inference.SelectRoutePricing{
				InputPriceCredits: inPrice, OutputPriceCredits: outPrice,
			},
			PriceUnit: inference.PriceUnitTokens,
		})
	}))
	t.Cleanup(srv.Close)
	return inference.NewRoutingClient(srv.URL)
}

// usageUpstream is a fake LiteLLM that answers with one content chunk and one
// terminal usage frame carrying the supplied token counts.
type usageUpstream struct {
	mu           sync.Mutex
	status       int
	promptTokens int
	outputTokens int
	servedModel  string

	calls  int
	bodies []string
}

func (u *usageUpstream) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.calls++
		u.bodies = append(u.bodies, string(raw))
		status, in, out, model := u.status, u.promptTokens, u.outputTokens, u.servedModel
		u.mu.Unlock()

		if status != 0 && (status < 200 || status > 299) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"message":"upstream refused"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "data: {\"model\":%q,\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n", model)
		if flusher != nil {
			flusher.Flush()
		}
		if in+out > 0 {
			_, _ = fmt.Fprintf(w,
				"data: {\"model\":%q,\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":%d,\"completion_tokens\":%d,\"total_tokens\":%d}}\n\n",
				model, in, out, in+out)
			if flusher != nil {
				flusher.Flush()
			}
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (u *usageUpstream) observed() (int, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls, u.bodies
}

func sessionChatRequest(t *testing.T, tenantID, userID uuid.UUID, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID: userID, TenantID: tenantID, Role: "member", Email: "member@example.test",
	}))
}

// TestSessionChatSettlesAtTheCatalogMagnitude is the assertion #819 asks for:
// the settled charge equals a figure recomputed from the catalog row in the
// test, for a known token count, on the account the tenant maps to.
//
// Both cases are the worked examples from the #746 diagnosis, computed by hand:
//
//	hive-auto    56000 / 224000 per million, 111 in / 70 out
//	             (111*56000 + 70*224000) / 1e6 = 21.896 -> 22
//	hive-default 21000 / 84000 per million, 111 in / 32 out
//	             (111*21000 + 32*84000) / 1e6 = 5.019  -> 5
func TestSessionChatSettlesAtTheCatalogMagnitude(t *testing.T) {
	cases := []struct {
		name        string
		alias       string
		route       string
		inPrice     int64
		outPrice    int64
		inTokens    int
		outTokens   int
		wantCredits int64
	}{
		{"hive-auto", "hive-auto", "route-test-auto", 56_000, 224_000, 111, 70, 22},
		{"hive-default", "hive-default", "route-test-default", 21_000, 84_000, 111, 32, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			accounting := &fakeAccounting{}
			cp := accounting.server(t)
			upstream := &usageUpstream{promptTokens: tc.inTokens, outputTokens: tc.outTokens, servedModel: tc.route}
			ll := upstream.server(t)

			tenantID, accountID := uuid.New(), uuid.New()
			handler := chat.NewDispatch(chat.Deps{
				Routing:    pricedRouting(t, tc.route, tc.inPrice, tc.outPrice),
				Accounting: inference.NewAccountingClient(cp.URL),
				Billing: stubBilling{state: metering.TenantBillingState{
					AccountID: accountID, Found: true, Deployment: metering.DeploymentHiveCloud,
				}},
				LiteLLMURL: ll.URL,
				Env:        "test",
			})

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, sessionChatRequest(t, tenantID, uuid.New(),
				fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, tc.alias)))
			require.Equal(t, http.StatusOK, rec.Code)

			reservations, finalized, released := accounting.calls()
			// Exactly one hold, exactly one terminal call, never both a charge
			// and a release for the same request.
			require.Len(t, reservations, 1, "a served completion must take exactly one hold")
			require.Len(t, finalized, 1, "a served completion must settle exactly once")
			require.Empty(t, released, "a settled request must not also release its hold")

			// Magnitude, recomputed here rather than read from the price table.
			want := expectedCredits(int64(tc.inTokens), int64(tc.outTokens), tc.inPrice, tc.outPrice)
			require.Equal(t, tc.wantCredits, want, "hand-computed expectation disagrees with the arithmetic")
			require.Equal(t, want, finalized[0].ActualCredits,
				"settled credits must equal the catalog price of the metered tokens")
			// The token total, the flat hold, and one credit per token are the
			// three wrong answers this path has produced before.
			require.NotEqual(t, int64(tc.inTokens+tc.outTokens), finalized[0].ActualCredits)
			require.NotEqual(t, int64(10_000), finalized[0].ActualCredits)

			// Attribution: the account the tenant maps to, on both calls.
			require.Equal(t, accountID.String(), reservations[0].AccountID)
			require.Equal(t, accountID.String(), finalized[0].AccountID)
			require.Equal(t, tc.alias, reservations[0].ModelAlias)

			// Real quantities, carried onto the settlement row so the console
			// reads tokens rather than zero (#856).
			require.Equal(t, int64(tc.inTokens), finalized[0].InputTokens)
			require.Equal(t, int64(tc.outTokens), finalized[0].OutputTokens)
			require.True(t, finalized[0].TerminalUsageConfirmed,
				"a provider-reported token count settles as measured truth")
			require.Equal(t, "completed", finalized[0].Status)
		})
	}
}

// TestSessionChatRequestsTheUsageEnvelope is the #746 root cause in one
// assertion: without include_usage no terminal usage frame ever arrives, the
// token counts stay zero, and every settlement above would charge for a
// request it never measured.
func TestSessionChatRequestsTheUsageEnvelope(t *testing.T) {
	accounting := &fakeAccounting{}
	cp := accounting.server(t)
	upstream := &usageUpstream{promptTokens: 11, outputTokens: 7, servedModel: "route-test-auto"}
	ll := upstream.server(t)

	handler := chat.NewDispatch(chat.Deps{
		Routing:    pricedRouting(t, "route-test-auto", 56_000, 224_000),
		Accounting: inference.NewAccountingClient(cp.URL),
		Billing: stubBilling{state: metering.TenantBillingState{
			AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
		}},
		LiteLLMURL: ll.URL,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionChatRequest(t, uuid.New(), uuid.New(),
		`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}],"max_tokens":32}`))
	require.Equal(t, http.StatusOK, rec.Code)

	calls, bodies := upstream.observed()
	require.Equal(t, 1, calls)

	var sent struct {
		Model         string `json:"model"`
		Stream        bool   `json:"stream"`
		MaxTokens     int    `json:"max_tokens"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	require.NoError(t, json.Unmarshal([]byte(bodies[0]), &sent))
	require.True(t, sent.StreamOptions.IncludeUsage, "the terminal usage frame must be requested")
	require.True(t, sent.Stream)
	require.Equal(t, "route-test-auto", sent.Model)
	require.Equal(t, 32, sent.MaxTokens, "caller parameters must survive the rewrite")
}

// TestSessionChatRefusesWhenTheAccountCannotPay covers the zero-balance case.
// The budget middleware never saw this traffic (it resolves an hk_ bearer
// only), which is why a workspace showing no credits still served completions.
// The hold now refuses it, before a provider is reached.
func TestSessionChatRefusesWhenTheAccountCannotPay(t *testing.T) {
	accounting := &fakeAccounting{reservationStatus: http.StatusConflict}
	cp := accounting.server(t)
	upstream := &usageUpstream{promptTokens: 11, outputTokens: 7}
	ll := upstream.server(t)

	handler := chat.NewDispatch(chat.Deps{
		Routing:    pricedRouting(t, "route-test-auto", 56_000, 224_000),
		Accounting: inference.NewAccountingClient(cp.URL),
		Billing: stubBilling{state: metering.TenantBillingState{
			AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
		}},
		LiteLLMURL: ll.URL,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionChatRequest(t, uuid.New(), uuid.New(),
		`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}]}`))

	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Contains(t, rec.Body.String(), "insufficient_quota")
	calls, _ := upstream.observed()
	require.Zero(t, calls, "a request that cannot be paid for must never reach a provider")

	_, finalized, released := accounting.calls()
	require.Empty(t, finalized, "a refused request charges nothing")
	require.Empty(t, released, "a reservation that was never created cannot be stranded")
	// No currency, no balance, no exchange language in a customer-facing refusal.
	body := strings.ToLower(rec.Body.String())
	for _, leak := range []string{"credit", "balance", "usd", "bdt", "route-", "provider"} {
		require.NotContains(t, body, leak)
	}
}

// TestSessionChatRefusesTenantWithNoBillingAccount is the fail-closed half of
// the fix. 26 of 461 tenants carry a tenant_billing_accounts row today (#721),
// and the alternative to refusing them is serving inference nobody can be
// charged for, which is the defect itself.
func TestSessionChatRefusesTenantWithNoBillingAccount(t *testing.T) {
	accounting := &fakeAccounting{}
	cp := accounting.server(t)
	upstream := &usageUpstream{promptTokens: 11, outputTokens: 7}
	ll := upstream.server(t)

	handler := chat.NewDispatch(chat.Deps{
		Routing:    pricedRouting(t, "route-test-auto", 56_000, 224_000),
		Accounting: inference.NewAccountingClient(cp.URL),
		Billing: stubBilling{state: metering.TenantBillingState{
			Found: false, Deployment: metering.DeploymentHiveCloud,
		}},
		LiteLLMURL: ll.URL,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionChatRequest(t, uuid.New(), uuid.New(),
		`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}]}`))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), "billing_not_configured")
	calls, _ := upstream.observed()
	require.Zero(t, calls)
	reservations, finalized, _ := accounting.calls()
	require.Empty(t, reservations)
	require.Empty(t, finalized)
}

// TestSessionChatReleasesTheHoldWhenTheUpstreamFails: a refused or failed
// request charges nothing and leaves no hold behind. Stranded holds locked
// 154000 credits across 32 reservations the last time this went wrong (#616).
func TestSessionChatReleasesTheHoldWhenTheUpstreamFails(t *testing.T) {
	accounting := &fakeAccounting{}
	cp := accounting.server(t)
	upstream := &usageUpstream{status: http.StatusInternalServerError}
	ll := upstream.server(t)

	handler := chat.NewDispatch(chat.Deps{
		Routing:    pricedRouting(t, "route-test-auto", 56_000, 224_000),
		Accounting: inference.NewAccountingClient(cp.URL),
		Billing: stubBilling{state: metering.TenantBillingState{
			AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
		}},
		LiteLLMURL: ll.URL,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionChatRequest(t, uuid.New(), uuid.New(),
		`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}]}`))

	require.GreaterOrEqual(t, rec.Code, http.StatusBadRequest)
	reservations, finalized, released := accounting.calls()
	require.Len(t, reservations, 1)
	require.Empty(t, finalized, "a failed request must not settle a charge")
	require.Len(t, released, 1, "the hold must be handed back exactly once")
	require.Equal(t, "upstream_error", released[0].Reason)
}

// TestSessionChatReleasesTheHoldWhenSettlementFails covers the other half of
// the exactly-once rule: a finalize that does not land must fall back to a
// release, so the reservation still reaches a terminal state once rather than
// losing the charge and stranding the hold in the same step (#616).
func TestSessionChatReleasesTheHoldWhenSettlementFails(t *testing.T) {
	accounting := &fakeAccounting{finalizeStatus: http.StatusInternalServerError}
	cp := accounting.server(t)
	upstream := &usageUpstream{promptTokens: 111, outputTokens: 70, servedModel: "route-test-auto"}
	ll := upstream.server(t)

	handler := chat.NewDispatch(chat.Deps{
		Routing:    pricedRouting(t, "route-test-auto", 56_000, 224_000),
		Accounting: inference.NewAccountingClient(cp.URL),
		Billing: stubBilling{state: metering.TenantBillingState{
			AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
		}},
		LiteLLMURL: ll.URL,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionChatRequest(t, uuid.New(), uuid.New(),
		`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}]}`))
	require.Equal(t, http.StatusOK, rec.Code)

	_, finalized, released := accounting.calls()
	require.Len(t, finalized, 1, "settlement is attempted once")
	require.Len(t, released, 1, "a failed settlement releases the hold instead")
	require.Equal(t, "finalize_failed", released[0].Reason)
}

// TestSessionChatEnterpriseTenantIsNotBilled records the one deliberate
// exception (D-027): an ENTERPRISE_EDGE tenant has no prepaid relationship
// with Hive, so it takes no hold and issues no charge. It is still not silent,
// because the trace and audit rows carry the real token counts the usage frame
// now delivers.
func TestSessionChatEnterpriseTenantIsNotBilled(t *testing.T) {
	accounting := &fakeAccounting{}
	cp := accounting.server(t)
	upstream := &usageUpstream{promptTokens: 111, outputTokens: 70, servedModel: "route-test-auto"}
	ll := upstream.server(t)

	handler := chat.NewDispatch(chat.Deps{
		Routing:    pricedRouting(t, "route-test-auto", 56_000, 224_000),
		Accounting: inference.NewAccountingClient(cp.URL),
		Billing: stubBilling{state: metering.TenantBillingState{
			Found: false, Deployment: metering.DeploymentEnterpriseEdge,
		}},
		LiteLLMURL: ll.URL,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionChatRequest(t, uuid.New(), uuid.New(),
		`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}]}`))

	require.Equal(t, http.StatusOK, rec.Code)
	reservations, finalized, released := accounting.calls()
	require.Empty(t, reservations)
	require.Empty(t, finalized)
	require.Empty(t, released)
}

// TestSessionChatRefusesWhenAccountingIsNotWired: a gateway that cannot charge
// must not serve. This is the shape the defect took in production, so it is
// asserted rather than left to wiring discipline.
func TestSessionChatRefusesWhenAccountingIsNotWired(t *testing.T) {
	upstream := &usageUpstream{promptTokens: 11, outputTokens: 7}
	ll := upstream.server(t)

	handler := chat.NewDispatch(chat.Deps{
		Routing:    pricedRouting(t, "route-test-auto", 56_000, 224_000),
		LiteLLMURL: ll.URL,
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sessionChatRequest(t, uuid.New(), uuid.New(),
		`{"model":"hive-auto","messages":[{"role":"user","content":"hi"}]}`))

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	calls, _ := upstream.observed()
	require.Zero(t, calls)
}
