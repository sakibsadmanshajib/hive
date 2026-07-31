package inference

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
)

// TestOrchestratorSelectRouteFailsClosedWithoutTenant is the D-030 fail-closed
// guard at the invocation path: an API key whose account has no resolvable
// tenant (authz.AuthSnapshot.TenantID empty) must be refused with
// ErrAccountNotProvisioned before the routing client is ever dialed. The
// routing client here points at an address nothing listens on, so any call
// through to it would surface as a dial error rather than
// ErrAccountNotProvisioned -- proving the early return, not just the error
// value.
func TestOrchestratorSelectRouteFailsClosedWithoutTenant(t *testing.T) {
	o := &Orchestrator{routing: NewRoutingClient("http://127.0.0.1:1")}

	_, err := o.selectRoute(context.Background(), authz.AuthSnapshot{
		KeyID:     "key-1",
		AccountID: "acct-1",
		// TenantID deliberately left empty: account has no
		// public.tenant_billing_accounts row.
	}, SelectRouteInput{AliasID: "hive-restricted"})

	if !errors.Is(err, ErrAccountNotProvisioned) {
		t.Fatalf("expected ErrAccountNotProvisioned, got %v", err)
	}
}

// TestOrchestratorSelectRouteBindsResolvedTenant proves the success path: a
// snapshot carrying a resolved tenant reaches the control-plane routing call
// bearing that tenant, so the entitlement check inside
// routing.Service.SelectRoute can finally run for API-key traffic. Combined
// with control-plane's own entitlement tests, this closes the loop the
// unreachable-gate bug left open: a tenant lacking a restricted alias is now
// refused via ErrModelNotEntitled instead of silently admitted.
func TestOrchestratorSelectRouteBindsResolvedTenant(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SelectRouteResult{
			AliasID:          "hive-restricted",
			RouteID:          "route-1",
			LiteLLMModelName: "route-1",
			Provider:         "groq",
		})
	}))
	defer srv.Close()

	o := &Orchestrator{routing: NewRoutingClient(srv.URL)}
	tenantID := uuid.New()

	if _, err := o.selectRoute(context.Background(), authz.AuthSnapshot{
		KeyID:     "key-1",
		AccountID: "acct-1",
		TenantID:  tenantID.String(),
	}, SelectRouteInput{AliasID: "hive-restricted"}); err != nil {
		t.Fatalf("selectRoute returned error: %v", err)
	}

	if got := captured["tenant_id"]; got != tenantID.String() {
		t.Fatalf("expected tenant_id %q on the wire, got %v", tenantID.String(), got)
	}
}

// TestOrchestratorSelectRoutePropagatesEntitlementRefusal proves an
// entitled-tenant call that control-plane refuses (403, ErrModelNotEntitled)
// propagates through selectRoute unchanged, so the three inference entry
// points can map it to a 403 rather than a generic routing_error.
func TestOrchestratorSelectRoutePropagatesEntitlementRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"routing: model not entitled for tenant: alias hive-restricted"}`))
	}))
	defer srv.Close()

	o := &Orchestrator{routing: NewRoutingClient(srv.URL)}

	_, err := o.selectRoute(context.Background(), authz.AuthSnapshot{
		KeyID:     "key-1",
		AccountID: "acct-1",
		TenantID:  uuid.New().String(),
	}, SelectRouteInput{AliasID: "hive-restricted"})

	if !errors.Is(err, ErrModelNotEntitled) {
		t.Fatalf("expected ErrModelNotEntitled, got %v", err)
	}
}
