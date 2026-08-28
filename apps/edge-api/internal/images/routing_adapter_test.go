package images_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/images"
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
	f := &fakeRoutingServer{status: status, aliasID: "hive-restricted-image", litellmID: "route-groq-restricted-image"}
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

	adapter := images.NewRoutingAdapter(inference.NewRoutingClient(srv.URL))

	_, err := adapter.SelectRoute(context.Background(), images.RouteInput{
		AliasID:             "hive-restricted-image",
		TenantID:            "", // no resolvable tenant
		NeedImageGeneration: true,
	})
	if err == nil {
		t.Fatal("expected SelectRoute to fail closed for a missing tenant")
	}
	if !errors.Is(err, images.ErrAccountNotProvisioned) {
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

	adapter := images.NewRoutingAdapter(inference.NewRoutingClient(srv.URL))

	_, err := adapter.SelectRoute(context.Background(), images.RouteInput{
		AliasID:             "hive-restricted-image",
		TenantID:            "not-a-uuid",
		NeedImageGeneration: true,
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
// images RoutingAdapter never put a tenant on ctx at all, so
// inference.RoutingClient.SelectRoute always sent an empty tenant_id.
func TestRoutingAdapterBindsTenantOntoWire(t *testing.T) {
	fake, srv := newFakeRoutingServer(http.StatusOK)
	defer srv.Close()

	adapter := images.NewRoutingAdapter(inference.NewRoutingClient(srv.URL))

	tenantID := uuid.New()
	result, err := adapter.SelectRoute(context.Background(), images.RouteInput{
		AliasID:             "hive-restricted-image",
		TenantID:            tenantID.String(),
		NeedImageGeneration: true,
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
	if result.AliasID != "hive-restricted-image" {
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

	adapter := images.NewRoutingAdapter(inference.NewRoutingClient(srv.URL))

	_, err := adapter.SelectRoute(context.Background(), images.RouteInput{
		AliasID:             "hive-restricted-image",
		TenantID:            uuid.New().String(),
		NeedImageGeneration: true,
	})
	if err == nil {
		t.Fatal("expected SelectRoute to refuse a tenant-restricted alias")
	}
}

// TestRoutingAdapterLogsAccountNotProvisioned is the #1240 review-comment
// regression guard: this adapter used to fail closed with a local uuid.Parse
// that never told an operator anything happened, alongside audio's identical
// gap, while /v1/models and /v1/chat/completions were already loud. Proven
// the same way the reviewer proved authz.TenantUUID's log: comment out the
// account_id/key_id fields on RouteInput below (or the ParseTenantID call in
// routing_adapter.go) and this test fails; restore either and it passes.
func TestRoutingAdapterLogsAccountNotProvisioned(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	adapter := images.NewRoutingAdapter(inference.NewRoutingClient("http://unused.invalid"))

	_, err := adapter.SelectRoute(context.Background(), images.RouteInput{
		AliasID:             "hive-restricted-image",
		TenantID:            "",
		AccountID:           "acct-repro",
		APIKeyID:            "key-repro",
		NeedImageGeneration: true,
	})
	if !errors.Is(err, images.ErrAccountNotProvisioned) {
		t.Fatalf("expected ErrAccountNotProvisioned, got %v", err)
	}

	got := buf.String()
	for _, want := range []string{"account_not_provisioned", "acct-repro", "key-repro"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected log output to contain %q, got %q", want, got)
		}
	}
}
