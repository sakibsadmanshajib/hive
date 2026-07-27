package auth

// The tenant a caller "selected" is self-asserted metadata, not an
// authorization claim. user_metadata is writable by the user through GoTrue's
// PUT /auth/v1/user, so these tests pin the rule that control-plane will only
// carry a tenant into a Viewer once it has confirmed a live membership on it.
//
// Before the membership check existed, an ordinary signed-in user could write
// any tenant id into their own metadata and control-plane would treat it as
// authoritative, because every consumer of Viewer.TenantID checked only that it
// was not uuid.Nil. /api/v1/catalog/models is OptionalRequire rather than admin
// gated, which made another tenant's model visibility list readable that way.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

const (
	victimTenant = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	attackerUser = "33333333-3333-4333-8333-333333333333"
)

// selectedTenantServer stands in for GoTrue returning a user whose metadata
// names victimTenant, which is exactly what an attacker writes to their own
// profile.
func selectedTenantServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"` + attackerUser + `",
			"email":"attacker@example.com",
			"email_confirmed_at":"2026-07-27T00:00:00Z",
			"user_metadata":{"selected_tenant_id":"` + victimTenant + `"}
		}`))
	}))
}

// TestSelectedTenantDeniedWithoutActiveMembership is the core assertion: a
// tenant the caller is not a member of must not survive into the Viewer.
func TestSelectedTenantDeniedWithoutActiveMembership(t *testing.T) {
	server := selectedTenantServer(t)
	defer server.Close()

	var checkedUser, checkedTenant uuid.UUID
	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			checkedUser, checkedTenant = userID, tenantID
			return false, nil
		})

	viewer, err := client.LookupUser(context.Background(), "bearer-token")
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}

	if viewer.TenantID != uuid.Nil {
		t.Fatalf("a self-asserted tenant with no active membership must resolve to uuid.Nil, got %s", viewer.TenantID)
	}
	// The caller is still authenticated. Account-scoped routes must keep
	// working; only tenant-scoped ones deny.
	if viewer.UserID.String() != attackerUser {
		t.Fatalf("expected the authenticated user to survive, got %s", viewer.UserID)
	}
	if checkedUser.String() != attackerUser || checkedTenant.String() != victimTenant {
		t.Fatalf("membership check must be asked about (%s, %s), got (%s, %s)",
			attackerUser, victimTenant, checkedUser, checkedTenant)
	}
}

// TestSelectedTenantAllowedWithActiveMembership is the counterpart: a genuine
// member keeps their tenant, so the fix does not break the normal path.
func TestSelectedTenantAllowedWithActiveMembership(t *testing.T) {
	server := selectedTenantServer(t)
	defer server.Close()

	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			return true, nil
		})

	viewer, err := client.LookupUser(context.Background(), "bearer-token")
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}
	if viewer.TenantID.String() != victimTenant {
		t.Fatalf("a validated membership must keep the tenant, got %s", viewer.TenantID)
	}
}

// TestSelectedTenantFailsClosedWhenCheckErrors pins the direction of failure. A
// membership lookup that cannot be completed must never be read as a grant.
func TestSelectedTenantFailsClosedWhenCheckErrors(t *testing.T) {
	server := selectedTenantServer(t)
	defer server.Close()

	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			return false, errors.New("connection reset")
		})

	viewer, err := client.LookupUser(context.Background(), "bearer-token")
	if err != nil {
		t.Fatalf("LookupUser must still return an authenticated viewer: %v", err)
	}
	if viewer.TenantID != uuid.Nil {
		t.Fatalf("an errored membership check must deny the tenant, got %s", viewer.TenantID)
	}
}

// TestNoSelectedTenantSkipsMembershipCheck keeps the common case free of an
// extra query: a user with no selected tenant has nothing to validate.
func TestNoSelectedTenantSkipsMembershipCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"` + attackerUser + `",
			"email":"plain@example.com",
			"email_confirmed_at":"2026-07-27T00:00:00Z",
			"user_metadata":{}
		}`))
	}))
	defer server.Close()

	called := false
	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			called = true
			return true, nil
		})

	viewer, err := client.LookupUser(context.Background(), "bearer-token")
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}
	if viewer.TenantID != uuid.Nil {
		t.Fatalf("expected uuid.Nil tenant, got %s", viewer.TenantID)
	}
	if called {
		t.Fatal("membership check must not run when no tenant was selected")
	}
}

// TestMalformedSelectedTenantResolvesToNil guards the parse path. A tenant id
// that is not a uuid must deny rather than reach the membership check as
// something surprising.
func TestMalformedSelectedTenantResolvesToNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"` + attackerUser + `",
			"email":"bent@example.com",
			"email_confirmed_at":"2026-07-27T00:00:00Z",
			"user_metadata":{"selected_tenant_id":"not-a-uuid"}
		}`))
	}))
	defer server.Close()

	called := false
	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			called = true
			return true, nil
		})

	viewer, err := client.LookupUser(context.Background(), "bearer-token")
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}
	if viewer.TenantID != uuid.Nil {
		t.Fatalf("expected uuid.Nil tenant, got %s", viewer.TenantID)
	}
	if called {
		t.Fatal("membership check must not run for an unparseable tenant id")
	}
}
