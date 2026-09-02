// Package billingtest is a fake control-plane accounting surface for tests of
// handlers that gate on sessionbilling.
//
// It exists because every such handler needs the same three endpoints and the
// same tenant-state stub, and three copies of that fixture drift: the first
// copy covered a rich tenant, a 409-poor tenant and an unwired seam, and every
// copy inherited the same blind spot (a billing lookup that fails). One
// fixture with an explicit error field makes that branch reachable everywhere.
package billingtest

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

// Accounting records every accounting call a gated handler makes.
type Accounting struct {
	mu sync.Mutex

	// ReservationStatus, when non-zero, refuses the hold with that status.
	ReservationStatus int
	// FinalizeStatus, when non-zero, fails every finalize with that status.
	// The hold must then be handed back rather than left stranded (#616), so
	// this is the branch that proves a reservation still reaches a terminal
	// state exactly once when the charge itself cannot land.
	FinalizeStatus int

	reservations []inference.CreateReservationInput
	finalized    []inference.FinalizeReservationInput
	released     []inference.ReleaseReservationInput
}

// Client starts a server for this fake and returns an accounting client
// pointed at it. The server is closed by t.Cleanup.
func (a *Accounting) Client(t *testing.T) *inference.AccountingClient {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/usage/attempts", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(inference.AttemptResult{ID: "att_1", Status: "dispatching"})
	})
	mux.HandleFunc("/internal/accounting/reservations", func(w http.ResponseWriter, r *http.Request) {
		var in inference.CreateReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		a.mu.Lock()
		a.reservations = append(a.reservations, in)
		status, id := a.ReservationStatus, fmt.Sprintf("res_%d", len(a.reservations))
		a.mu.Unlock()
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
		a.mu.Lock()
		a.finalized = append(a.finalized, in)
		status := a.FinalizeStatus
		a.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"finalize refused"}`)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/internal/accounting/reservations/release", func(w http.ResponseWriter, r *http.Request) {
		var in inference.ReleaseReservationInput
		_ = json.NewDecoder(r.Body).Decode(&in)
		a.mu.Lock()
		a.released = append(a.released, in)
		a.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return inference.NewAccountingClient(srv.URL)
}

// Counts reports how many holds were taken and how many were handed back.
func (a *Accounting) Counts() (reservations, released int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.reservations), len(a.released)
}

// Finalized returns a copy of the charges settled, credits included.
func (a *Accounting) Finalized() []inference.FinalizeReservationInput {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]inference.FinalizeReservationInput(nil), a.finalized...)
}

// Reservations returns a copy of the holds taken.
func (a *Accounting) Reservations() []inference.CreateReservationInput {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]inference.CreateReservationInput(nil), a.reservations...)
}

// Released returns a copy of the releases, reasons included.
func (a *Accounting) Released() []inference.ReleaseReservationInput {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]inference.ReleaseReservationInput(nil), a.released...)
}

// Resolver is a tenant-to-account map with no database behind it. Err is the
// point of it: a billing position that cannot be read is not the same as one
// that is known-absent, and the handler must refuse rather than serve.
type Resolver struct {
	State metering.TenantBillingState
	Err   error
}

// ResolveState satisfies sessionbilling.Resolver.
func (r Resolver) ResolveState(context.Context, uuid.UUID) (metering.TenantBillingState, error) {
	return r.State, r.Err
}

// Billable is a tenant with a billing account on the hosted deployment.
func Billable() Resolver {
	return Resolver{State: metering.TenantBillingState{
		AccountID: uuid.New(), Found: true, Deployment: metering.DeploymentHiveCloud,
	}}
}

// Enterprise is a tenant with no prepaid relationship with Hive (D-027).
func Enterprise() Resolver {
	return Resolver{State: metering.TenantBillingState{Deployment: metering.DeploymentEnterpriseEdge}}
}

// Unreadable is a tenant whose billing position cannot be read at all.
func Unreadable() Resolver {
	return Resolver{Err: fmt.Errorf("billingtest: billing lookup failed")}
}
