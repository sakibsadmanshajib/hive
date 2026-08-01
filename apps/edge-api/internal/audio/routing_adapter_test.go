package audio_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/audio"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
)

// fakeRoutingServer stands in for control-plane's /internal/routing/select.
// It records whether it was ever called, and the tenant_id it received, so
// tests can tell "the adapter refused locally" apart from "the adapter asked
// control-plane and got refused" without depending on any real
// routing.Service code.
type fakeRoutingServer struct {
	called    bool
	tenantID  string
	status    int
	aliasID   string
	litellmID string
}

func newFakeRoutingServer(status int) (*fakeRoutingServer, *httptest.Server) {
	f := &fakeRoutingServer{status: status, aliasID: "hive-restricted", litellmID: "route-groq-restricted"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.called = true
		var decoded map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &decoded)
		if tid, ok := decoded["tenant_id"]; ok {
			f.tenantID, _ = tid.(string)
		}
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(`{"error":"routing: model not entitled for tenant"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(inference.SelectRouteResult{
			AliasID:          f.aliasID,
			LiteLLMModelName: f.litellmID,
		})
	}))
	return f, srv
}

// TestRoutingAdapterFailsClosedWithNoTenant is the #623 regression guard: an
// API key with no resolvable tenant must never reach control-plane's routing
// endpoint at all: reaching it with an empty tenant_id is exactly the shape
// that let control-plane parse it as uuid.Nil and skip the entitlement gate.
func TestRoutingAdapterFailsClosedWithNoTenant(t *testing.T) {
	fake, srv := newFakeRoutingServer(http.StatusOK)
	defer srv.Close()

	adapter := audio.NewRoutingAdapter(inference.NewRoutingClient(srv.URL))

	_, err := adapter.SelectRoute(context.Background(), audio.RouteInput{
		AliasID:  "hive-restricted",
		TenantID: "", // no resolvable tenant
		NeedTTS:  true,
	})
	if err == nil {
		t.Fatal("expected SelectRoute to fail closed for a missing tenant")
	}
	if !errors.Is(err, audio.ErrAccountNotProvisioned) {
		t.Fatalf("expected ErrAccountNotProvisioned, got %v", err)
	}
	if fake.called {
		t.Fatal("expected the routing server to never be contacted: fail-closed must happen locally, before any request goes out")
	}
}

// TestRoutingAdapterFailsClosedWithUnparseableTenant covers a malformed
// tenant string the same way as an empty one: both mean "no resolvable
// tenant" and must fail closed identically.
func TestRoutingAdapterFailsClosedWithUnparseableTenant(t *testing.T) {
	fake, srv := newFakeRoutingServer(http.StatusOK)
	defer srv.Close()

	adapter := audio.NewRoutingAdapter(inference.NewRoutingClient(srv.URL))

	_, err := adapter.SelectRoute(context.Background(), audio.RouteInput{
		AliasID:  "hive-restricted",
		TenantID: "not-a-uuid",
		NeedTTS:  true,
	})
	if err == nil {
		t.Fatal("expected SelectRoute to fail closed for an unparseable tenant")
	}
	if fake.called {
		t.Fatal("expected the routing server to never be contacted for an unparseable tenant")
	}
}

// TestRoutingAdapterBindsTenantOntoWire proves the core #623 fix: a resolved
// tenant now actually reaches control-plane on the wire. Before the fix, the
// audio RoutingAdapter never put a tenant on ctx at all, so
// inference.RoutingClient.SelectRoute always sent an empty tenant_id.
func TestRoutingAdapterBindsTenantOntoWire(t *testing.T) {
	fake, srv := newFakeRoutingServer(http.StatusOK)
	defer srv.Close()

	adapter := audio.NewRoutingAdapter(inference.NewRoutingClient(srv.URL))

	tenantID := uuid.New()
	result, err := adapter.SelectRoute(context.Background(), audio.RouteInput{
		AliasID:  "hive-restricted",
		TenantID: tenantID.String(),
		NeedTTS:  true,
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}
	if !fake.called {
		t.Fatal("expected the routing server to be contacted for a resolvable tenant")
	}
	if fake.tenantID != tenantID.String() {
		t.Fatalf("expected tenant_id %q on the wire, got %q", tenantID.String(), fake.tenantID)
	}
	if result.AliasID != "hive-restricted" {
		t.Fatalf("expected a permitted alias to still route successfully, got alias=%q", result.AliasID)
	}
}

// TestRoutingAdapterRefusesEntitlementDenial proves the #623 regression end
// to end: a tenant that control-plane's routing.Service refuses (the
// restricted-alias gate at routing/service.go) must surface as a SelectRoute
// error the handler treats as "model not available", not a silent success.
func TestRoutingAdapterRefusesEntitlementDenial(t *testing.T) {
	_, srv := newFakeRoutingServer(http.StatusForbidden)
	defer srv.Close()

	adapter := audio.NewRoutingAdapter(inference.NewRoutingClient(srv.URL))

	_, err := adapter.SelectRoute(context.Background(), audio.RouteInput{
		AliasID:  "hive-restricted",
		TenantID: uuid.New().String(),
		NeedTTS:  true,
	})
	if err == nil {
		t.Fatal("expected SelectRoute to refuse a tenant-restricted alias")
	}
}
