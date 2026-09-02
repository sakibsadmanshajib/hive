package sessionbilling

// Guards for the two properties of this lifecycle that are invisible from the
// calling handlers: a hold the client cannot cancel halfway through, and a
// refusal that is a value rather than something already written to a response.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/metering"
)

type stubAccounting struct {
	mu sync.Mutex

	reservationStatus int

	reservations []inference.CreateReservationInput
	released     []inference.ReleaseReservationInput
}

func (s *stubAccounting) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/usage/attempts", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(inference.AttemptResult{ID: "att_1", Status: "dispatching"})
	})
	mux.HandleFunc("/internal/accounting/reservations", func(w http.ResponseWriter, r *http.Request) {
		var in inference.CreateReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.mu.Lock()
		s.reservations = append(s.reservations, in)
		status, id := s.reservationStatus, fmt.Sprintf("res_%d", len(s.reservations))
		s.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"reservation exceeds available credits"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(inference.ReservationResult{
			ID: id, AccountID: in.AccountID, Status: "active", ReservedCredits: in.EstimatedCredits,
		})
	})
	mux.HandleFunc("/internal/accounting/reservations/release", func(w http.ResponseWriter, r *http.Request) {
		var in inference.ReleaseReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.mu.Lock()
		s.released = append(s.released, in)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type stubResolver struct {
	state metering.TenantBillingState
}

func (s stubResolver) ResolveState(context.Context, uuid.UUID) (metering.TenantBillingState, error) {
	return s.state, nil
}

func billableInput(t *testing.T, acct *stubAccounting) Input {
	t.Helper()
	return Input{
		Accounting: inference.NewAccountingClient(acct.server(t).URL),
		Billing: stubResolver{state: metering.TenantBillingState{
			AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
		}},
		TenantID:  uuid.New(),
		Route:     inference.SelectRouteResult{Pricing: inference.FixedPricing(300_000, 1_200_000), PriceUnit: inference.PriceUnitTokens},
		Alias:     "hive-fast",
		Endpoint:  inference.EndpointChatCompletions,
		RequestID: uuid.New(),
		Body:      []byte(`{"model":"hive-fast"}`),
		HoldFloor: inference.DefaultHoldText,
		Surface:   "test",
	}
}

// The hold must not ride the request context. Control-plane can commit the
// reservation row and then lose the answer to a cancellation, which refuses the
// request and returns before the caller has installed its deferred release. No
// reaper and no expires_at means the customer's credits are then locked until
// someone intervenes by hand (#600, the stranded-hold family behind #626).
func TestReserveTakesTheHoldOnAContextTheClientCannotCancel(t *testing.T) {
	acct := &stubAccounting{}
	in := billableInput(t, acct)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client is already gone by the time the hold is taken

	settle, refusal := Reserve(ctx, in)
	if refusal != nil {
		t.Fatalf("refused with %q; a cancelled client must not be able to abort the hold midway", refusal.Reason)
	}
	if settle == nil {
		t.Fatal("no settlement returned for a billable tenant")
	}
	if reservations := len(acct.reservations); reservations != 1 {
		t.Fatalf("want exactly one hold taken, got %d", reservations)
	}
	// And it is still releasable, on its own background context.
	settle.Release("test")
	if released := len(acct.released); released != 1 {
		t.Fatalf("want the hold released once, got %d", released)
	}
}

// The verdict is a value. Start renders it for an HTTP handler; a caller that
// is not one reads it instead, which is what lets the agent-task submit gate
// reuse this lifecycle rather than grow a second one.
func TestReserveReturnsTheQuotaVerdictWithoutWritingIt(t *testing.T) {
	acct := &stubAccounting{reservationStatus: http.StatusConflict}
	settle, refusal := Reserve(context.Background(), billableInput(t, acct))
	if settle != nil {
		t.Fatal("a refused reservation must not yield a settlement")
	}
	if refusal == nil || refusal.Reason != "insufficient_quota" {
		t.Fatalf("refusal = %+v, want insufficient_quota", refusal)
	}

	w := httptest.NewRecorder()
	refusal.Write(w)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("rendered status = %d, want 429", w.Code)
	}
}

// An endpoint or label left empty is a wiring mistake, and control-plane
// rejects an empty model_alias outright. Fail closed here instead of writing an
// empty string onto an attempt row and a reservation.
func TestReserveRefusesAnUnlabelledRequest(t *testing.T) {
	acct := &stubAccounting{}
	in := billableInput(t, acct)
	in.Endpoint = ""

	settle, refusal := Reserve(context.Background(), in)
	if settle != nil || refusal == nil {
		t.Fatalf("settle = %v, refusal = %v; want a refusal", settle, refusal)
	}
	if len(acct.reservations) != 0 {
		t.Error("an unlabelled request reached control-plane")
	}
}

// ReserveCharge drops the token price gate, which is correct for a caller
// deriving its own charge in a unit this package does not meter, and replaces
// it with the check that does apply to such a caller. A non-positive charge is
// refused rather than recorded: a zero credit reservation finalized at zero
// serves the request free while leaving a ledger row that reads as a charge,
// which is the shape that looked green while the gateway billed nothing for
// three days in July (D-034).
func TestReserveChargeRefusesANonPositiveCharge(t *testing.T) {
	for _, holdFloor := range []int64{0, -1} {
		acct := &stubAccounting{}
		in := billableInput(t, acct)
		// No route at all, which is the shape this entry point exists for.
		in.Route = inference.SelectRouteResult{}
		in.Body = nil
		in.HoldFloor = holdFloor

		settle, refusal := ReserveCharge(context.Background(), in)
		if settle != nil {
			t.Errorf("hold floor %d produced a settlement, want a refusal", holdFloor)
		}
		if refusal == nil {
			t.Fatalf("hold floor %d was accepted, want a refusal", holdFloor)
		}
		acct.mu.Lock()
		holds := len(acct.reservations)
		acct.mu.Unlock()
		if holds != 0 {
			t.Errorf("hold floor %d created %d reservations, want 0", holdFloor, holds)
		}
	}
}

// The positive case, so the guard above cannot pass by refusing everything: a
// caller with a derived charge and no route still gets a hold, sized at exactly
// the figure it derived. Nothing in the zero-value Route inflates or shrinks
// it, because ReservationCredits falls back to HoldFloor when the route
// carries no reservation estimate.
func TestReserveChargeHoldsExactlyTheDerivedCharge(t *testing.T) {
	acct := &stubAccounting{}
	in := billableInput(t, acct)
	in.Route = inference.SelectRouteResult{}
	in.Body = nil
	in.Alias = "hive-web-fetch"
	in.Endpoint = "web_fetch"
	in.HoldFloor = 200_000

	settle, refusal := ReserveCharge(context.Background(), in)
	if refusal != nil {
		t.Fatalf("refused: %s", refusal.Reason)
	}
	if settle == nil {
		t.Fatal("no settlement returned")
	}
	if settle.Held() != 200_000 {
		t.Errorf("held %d credits, want 200000", settle.Held())
	}
	acct.mu.Lock()
	defer acct.mu.Unlock()
	if len(acct.reservations) != 1 {
		t.Fatalf("reservations = %d, want 1", len(acct.reservations))
	}
	if got := acct.reservations[0].EstimatedCredits; got != 200_000 {
		t.Errorf("reservation estimated_credits = %d, want 200000", got)
	}
}
