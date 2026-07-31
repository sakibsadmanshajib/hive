package inference

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// captureRoutingServer records the raw request body sent to
// /internal/routing/select and answers with a fixed route.
func captureRoutingServer(t *testing.T, captured *map[string]any, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(raw, &decoded)
		*captured = decoded
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"routing: model not entitled for tenant: alias hive-fast"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(SelectRouteResult{
			AliasID:          "hive-fast",
			RouteID:          "route-groq-fast",
			LiteLLMModelName: "route-groq-fast",
			Provider:         "groq",
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestSelectRouteBindsTenantFromRequestContext is the guard that makes the fix
// impossible to forget in a new transport: every JWT-session inference path
// funnels through this client, so the tenant is bound here, once.
func TestSelectRouteBindsTenantFromRequestContext(t *testing.T) {
	var captured map[string]any
	srv := captureRoutingServer(t, &captured, http.StatusOK)
	client := NewRoutingClient(srv.URL)

	tenantID := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{ID: uuid.New(), TenantID: tenantID})

	if _, err := client.SelectRoute(ctx, SelectRouteInput{AliasID: "hive-fast", NeedChatCompletions: true}); err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}

	if got := captured["tenant_id"]; got != tenantID.String() {
		t.Fatalf("expected tenant_id %q on the wire, got %v", tenantID.String(), got)
	}
}

// TestSelectRouteSendsNoTenantForAPIKeyContext covers the API-key path: no JWT
// principal on the context means no tenant claim on the wire.
func TestSelectRouteSendsNoTenantForAPIKeyContext(t *testing.T) {
	var captured map[string]any
	srv := captureRoutingServer(t, &captured, http.StatusOK)
	client := NewRoutingClient(srv.URL)

	if _, err := client.SelectRoute(context.Background(), SelectRouteInput{AliasID: "hive-fast"}); err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}

	if _, present := captured["tenant_id"]; present {
		t.Fatalf("expected no tenant_id for an API-key request, got %v", captured["tenant_id"])
	}
}

// TestSelectRouteIgnoresCallerSuppliedTenant asserts the tenant is always
// derived from the authenticated context, never from the caller's struct, so no
// handler can widen its own scope by filling the field in.
func TestSelectRouteIgnoresCallerSuppliedTenant(t *testing.T) {
	var captured map[string]any
	srv := captureRoutingServer(t, &captured, http.StatusOK)
	client := NewRoutingClient(srv.URL)

	ctxTenant := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{ID: uuid.New(), TenantID: ctxTenant})

	_, err := client.SelectRoute(ctx, SelectRouteInput{
		AliasID:  "hive-fast",
		TenantID: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}

	if got := captured["tenant_id"]; got != ctxTenant.String() {
		t.Fatalf("expected the context tenant %q to win, got %v", ctxTenant.String(), got)
	}
}

// TestSelectRouteBindsAPIKeyTenantWhenNoJWTPrincipal covers D-030: an
// API-key request carries no JWT auth.User, so auth.TenantID(ctx) is always
// uuid.Nil for it. Orchestrator.selectRoute binds the key's resolved tenant
// via withAPIKeyTenant before ever reaching here; that must land on the wire
// the same way a JWT-session tenant does, so the control-plane entitlement
// check (routing.Service.SelectRoute) -- previously unreachable for API-key
// traffic -- actually runs.
func TestSelectRouteBindsAPIKeyTenantWhenNoJWTPrincipal(t *testing.T) {
	var captured map[string]any
	srv := captureRoutingServer(t, &captured, http.StatusOK)
	client := NewRoutingClient(srv.URL)

	tenantID := uuid.New()
	ctx := withAPIKeyTenant(context.Background(), tenantID)

	if _, err := client.SelectRoute(ctx, SelectRouteInput{AliasID: "hive-fast"}); err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}

	if got := captured["tenant_id"]; got != tenantID.String() {
		t.Fatalf("expected tenant_id %q on the wire, got %v", tenantID.String(), got)
	}
}

// TestSelectRouteJWTTenantWinsOverAPIKeyTenant pins the precedence order if
// both sources are ever present on ctx (the two auth paths are mutually
// exclusive in production, so this should never happen, but the precedence
// itself must stay deterministic rather than accidental).
func TestSelectRouteJWTTenantWinsOverAPIKeyTenant(t *testing.T) {
	var captured map[string]any
	srv := captureRoutingServer(t, &captured, http.StatusOK)
	client := NewRoutingClient(srv.URL)

	jwtTenant := uuid.New()
	ctx := auth.WithUser(context.Background(), &auth.User{ID: uuid.New(), TenantID: jwtTenant})
	ctx = withAPIKeyTenant(ctx, uuid.New())

	if _, err := client.SelectRoute(ctx, SelectRouteInput{AliasID: "hive-fast"}); err != nil {
		t.Fatalf("SelectRoute returned error: %v", err)
	}

	if got := captured["tenant_id"]; got != jwtTenant.String() {
		t.Fatalf("expected the JWT-session tenant %q to win, got %v", jwtTenant.String(), got)
	}
}

// TestSelectRouteMapsForbiddenToErrModelNotEntitled lets callers distinguish an
// entitlement refusal from a transport failure, which would otherwise surface as
// a transient 503.
func TestSelectRouteMapsForbiddenToErrModelNotEntitled(t *testing.T) {
	var captured map[string]any
	srv := captureRoutingServer(t, &captured, http.StatusForbidden)
	client := NewRoutingClient(srv.URL)

	ctx := auth.WithUser(context.Background(), &auth.User{ID: uuid.New(), TenantID: uuid.New()})

	_, err := client.SelectRoute(ctx, SelectRouteInput{AliasID: "hive-fast"})
	if err == nil {
		t.Fatal("expected an error for a 403 routing response")
	}
	if !errors.Is(err, ErrModelNotEntitled) {
		t.Fatalf("expected ErrModelNotEntitled, got %v", err)
	}
	if errors.Is(err, ErrRouteNotFound) {
		t.Fatal("an entitlement refusal must not look like an unknown model")
	}
}
