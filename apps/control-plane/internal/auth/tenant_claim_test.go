package auth

// The tenant claim is the one source of tenant scope that anything actually
// writes. public.custom_access_token_hook resolves it inside the database from
// a single snapshot of ACTIVE tenant_users joined to non-archived tenants, and
// stamps it into every issued token. raw_user_meta_data.selected_tenant_id, the
// field LookupUser used to read exclusively, is written only by
// POST /v1/tenants/switch and by the seeding scripts. The web console never
// calls that endpoint (its workspace switcher writes a cookie control-plane
// does not read), so every account created through normal signup carried
// uuid.Nil forever and every route behind platform.WorkspaceAdminGate answered
// it 400 "no tenant selected". That is what made /console/feature-gates and
// /console/marketplace unreachable for the workspace owners they were built
// for.
//
// Reading the claim is strictly safer than reading the metadata, not merely
// more convenient. The claim is emitted by a SECURITY DEFINER function that has
// already confirmed a live ACTIVE membership, and the token carrying it is
// signed by GoTrue. The metadata is writable by the caller through
// PUT /auth/v1/user. Both still pass through the same membership check below,
// so the claim can never widen access beyond a live membership either.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

const (
	claimUser         = "44444444-4444-4444-8444-444444444444"
	claimTenant       = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	otherUser         = "55555555-5555-4555-8555-555555555555"
	metadataOnlyState = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
)

// testToken builds a JWT-shaped string carrying the given payload. The
// signature segment is deliberately junk: nothing in control-plane verifies it
// locally, and these tests exist partly to pin that the claim is only ever read
// after Supabase has accepted the same token.
func testToken(t *testing.T, payload map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal token segment: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return enc(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." +
		enc(payload) + ".not-a-real-signature"
}

// userServer stands in for GoTrue answering GET /auth/v1/user for the given
// user id and user_metadata block.
func userServer(t *testing.T, userID, metadataJSON string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/v1/user" {
			t.Fatalf("expected /auth/v1/user, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"` + userID + `",
			"email":"claims@example.com",
			"email_confirmed_at":"2026-08-29T00:00:00Z",
			"user_metadata":` + metadataJSON + `
		}`))
	}))
}

// TestTenantClaimResolvesWhenMetadataIsEmpty is the defect this change fixes:
// the console never writes selected_tenant_id, so the metadata path yields
// nothing while the token has carried the right answer all along.
func TestTenantClaimResolvesWhenMetadataIsEmpty(t *testing.T) {
	server := userServer(t, claimUser, `{}`)
	defer server.Close()

	var checkedUser, checkedTenant uuid.UUID
	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			checkedUser, checkedTenant = userID, tenantID
			return true, nil
		})

	token := testToken(t, map[string]any{"sub": claimUser, "tenant_id": claimTenant})
	viewer, err := client.LookupUser(context.Background(), token)
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}

	if viewer.TenantID.String() != claimTenant {
		t.Fatalf("expected the validated tenant_id claim to resolve, got %s", viewer.TenantID)
	}
	if checkedUser.String() != claimUser || checkedTenant.String() != claimTenant {
		t.Fatalf("membership check must be asked about (%s, %s), got (%s, %s)",
			claimUser, claimTenant, checkedUser, checkedTenant)
	}
}

// TestTenantClaimIgnoredWhenSubDoesNotMatch pins the binding between the token
// and the user Supabase resolved from it. The two always agree in production;
// asserting it means a future refactor that starts reading a token from
// somewhere other than the validated Authorization header fails here rather
// than silently accepting another user's tenant.
func TestTenantClaimIgnoredWhenSubDoesNotMatch(t *testing.T) {
	server := userServer(t, claimUser, `{}`)
	defer server.Close()

	called := false
	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			called = true
			return true, nil
		})

	token := testToken(t, map[string]any{"sub": otherUser, "tenant_id": claimTenant})
	viewer, err := client.LookupUser(context.Background(), token)
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}

	if viewer.TenantID != uuid.Nil {
		t.Fatalf("a claim whose sub names another user must be ignored, got %s", viewer.TenantID)
	}
	if called {
		t.Fatal("membership check must not run for a claim that was not bound to this user")
	}
}

// TestMalformedTenantClaimFallsBackToMetadata keeps a bent token from
// destroying a resolution the old path would have made. A claim that is not a
// uuid is not a reason to deny a tenant the metadata path can still validate.
func TestMalformedTenantClaimFallsBackToMetadata(t *testing.T) {
	server := userServer(t, claimUser, `{"selected_tenant_id":"`+metadataOnlyState+`"}`)
	defer server.Close()

	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			return true, nil
		})

	token := testToken(t, map[string]any{"sub": claimUser, "tenant_id": "not-a-uuid"})
	viewer, err := client.LookupUser(context.Background(), token)
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}

	if viewer.TenantID.String() != metadataOnlyState {
		t.Fatalf("expected the metadata fallback to resolve, got %s", viewer.TenantID)
	}
}

// TestBearerThatIsNotAJWTFallsBackToMetadata covers the shapes a bearer can
// take that carry no claims at all, including an opaque token and an empty
// string. Neither may raise, and neither may lose the metadata path.
func TestBearerThatIsNotAJWTFallsBackToMetadata(t *testing.T) {
	for name, bearer := range map[string]string{
		"opaque":     "an-opaque-token",
		"empty":      "",
		"two-part":   "aGVhZGVy.cGF5bG9hZA",
		"bad-base64": "aGVhZGVy.!!!not-base64!!!.sig",
	} {
		t.Run(name, func(t *testing.T) {
			server := userServer(t, claimUser, `{"selected_tenant_id":"`+metadataOnlyState+`"}`)
			defer server.Close()

			client := NewClient(server.URL, "anon-key").WithMembershipCheck(
				func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
					return true, nil
				})

			viewer, err := client.LookupUser(context.Background(), bearer)
			if err != nil {
				t.Fatalf("LookupUser error: %v", err)
			}
			if viewer.TenantID.String() != metadataOnlyState {
				t.Fatalf("expected the metadata fallback to resolve, got %s", viewer.TenantID)
			}
		})
	}
}

// TestTenantClaimStillMembershipChecked is the security invariant. The claim is
// database-validated at issue time, but a membership revoked since then must
// still deny, so the claim goes through the same check the metadata does.
func TestTenantClaimStillMembershipChecked(t *testing.T) {
	server := userServer(t, claimUser, `{}`)
	defer server.Close()

	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			return false, nil
		})

	token := testToken(t, map[string]any{"sub": claimUser, "tenant_id": claimTenant})
	viewer, err := client.LookupUser(context.Background(), token)
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}
	if viewer.TenantID != uuid.Nil {
		t.Fatalf("a revoked membership must deny the claimed tenant, got %s", viewer.TenantID)
	}
	if viewer.UserID.String() != claimUser {
		t.Fatalf("the caller stays authenticated, got %s", viewer.UserID)
	}
}

// TestTenantClaimFailsClosedWhenCheckErrors keeps the direction of failure the
// same for the claim path as the metadata path already has.
func TestTenantClaimFailsClosedWhenCheckErrors(t *testing.T) {
	server := userServer(t, claimUser, `{}`)
	defer server.Close()

	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			return false, errors.New("connection reset")
		})

	token := testToken(t, map[string]any{"sub": claimUser, "tenant_id": claimTenant})
	viewer, err := client.LookupUser(context.Background(), token)
	if err != nil {
		t.Fatalf("LookupUser must still return an authenticated viewer: %v", err)
	}
	if viewer.TenantID != uuid.Nil {
		t.Fatalf("an errored membership check must deny the tenant, got %s", viewer.TenantID)
	}
}

// TestTenantClaimWinsOverStaleMetadata pins precedence. Both fields can be
// present and disagree: the metadata is a stale self-assertion left by an older
// switch, the claim is what the database resolved when this token was issued.
// The claim is the newer and the validated one.
func TestTenantClaimWinsOverStaleMetadata(t *testing.T) {
	server := userServer(t, claimUser, `{"selected_tenant_id":"`+metadataOnlyState+`"}`)
	defer server.Close()

	var checkedTenant uuid.UUID
	client := NewClient(server.URL, "anon-key").WithMembershipCheck(
		func(ctx context.Context, userID, tenantID uuid.UUID) (bool, error) {
			checkedTenant = tenantID
			return true, nil
		})

	token := testToken(t, map[string]any{"sub": claimUser, "tenant_id": claimTenant})
	viewer, err := client.LookupUser(context.Background(), token)
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}
	if viewer.TenantID.String() != claimTenant {
		t.Fatalf("the claim must win over stale metadata, got %s", viewer.TenantID)
	}
	if checkedTenant.String() != claimTenant {
		t.Fatalf("membership check must be asked about the claim, got %s", checkedTenant)
	}
}
