package accounts_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/authz"
)

// validPermStrings is the authoritative set of known permission wire strings
// derived from authz.AllPermissions(). Any string in Permissions must be one
// of these.
var validPermStrings = func() map[string]struct{} {
	m := make(map[string]struct{})
	for _, p := range authz.AllPermissions() {
		m[string(p)] = struct{}{}
	}
	return m
}()

// TestViewerResponseWireShape_NoGates_NoLegacy encodes the viewer response
// for a known actor and walks the resulting JSON to assert that no legacy
// gate-related keys appear at any level of the response tree.
//
// This test is a regression guard: if anyone adds back "gates",
// "can_invite_members", "can_manage_api_keys", or "allowedUnverifiedRoutes"
// anywhere in the viewer response, this test will fail red.
func TestViewerResponseWireShape_NoGates_NoLegacy(t *testing.T) {
	repo := newStubRepo()

	userID := uuid.New()
	acctID := uuid.New()
	repo.accountsMap[acctID] = &accounts.Account{
		ID:          acctID,
		Slug:        "wire-shape-workspace",
		DisplayName: "Wire Shape Workspace",
		AccountType: "personal",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: acctID, UserID: userID, Role: "owner", Status: "active"},
	}

	svc := accounts.NewService(repo)
	h := accounts.NewHandler(svc)

	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "wire-shape@example.com",
		EmailVerified: true,
		FullName:      "Wire Shape",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/viewer", nil)
	req = req.WithContext(auth.WithViewer(context.Background(), viewer))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// Decode into a generic map and walk every value recursively.
	var top map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &top); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	bannedKeys := []string{
		"gates",
		"can_invite_members",
		"can_manage_api_keys",
		"allowedUnverifiedRoutes",
		"allowed_unverified_routes",
	}

	var walkMap func(m map[string]interface{}, path string)
	walkMap = func(m map[string]interface{}, path string) {
		for k, v := range m {
			fullKey := path + "." + k
			for _, banned := range bannedKeys {
				if k == banned {
					t.Errorf("wire-shape violation: banned key %q found at %q", banned, fullKey)
				}
			}
			if nested, ok := v.(map[string]interface{}); ok {
				walkMap(nested, fullKey)
			}
		}
	}
	walkMap(top, "root")
}

// TestViewerResponseWireShape_PermissionsIsArrayOfPermStrings asserts that
// the "permissions" key in the viewer response is a JSON array of strings,
// each of which decodes to a known authz.Permission constant.
func TestViewerResponseWireShape_PermissionsIsArrayOfPermStrings(t *testing.T) {
	repo := newStubRepo()

	userID := uuid.New()
	acctID := uuid.New()
	repo.accountsMap[acctID] = &accounts.Account{
		ID:          acctID,
		Slug:        "perms-workspace",
		DisplayName: "Perms Workspace",
		AccountType: "personal",
		OwnerUserID: userID,
	}
	repo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: acctID, UserID: userID, Role: "owner", Status: "active"},
	}

	svc := accounts.NewService(repo)
	h := accounts.NewHandler(svc)

	viewer := auth.Viewer{
		UserID:        userID,
		Email:         "perms@example.com",
		EmailVerified: true,
		FullName:      "Perms User",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/viewer", nil)
	req = req.WithContext(auth.WithViewer(context.Background(), viewer))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	raw, ok := resp["permissions"]
	if !ok {
		t.Fatal("wire-shape violation: 'permissions' key missing from viewer response")
	}

	perms, ok := raw.([]interface{})
	if !ok {
		t.Fatalf("wire-shape violation: 'permissions' must be JSON array, got %T", raw)
	}

	for i, item := range perms {
		s, ok := item.(string)
		if !ok {
			t.Errorf("permissions[%d] is not a string: %v", i, item)
			continue
		}
		if _, known := validPermStrings[s]; !known {
			t.Errorf("permissions[%d] = %q is not a known authz.Permission constant", i, s)
		}
	}

	// A verified owner must have at least members.invite and api_keys.write.
	containsPerm := func(needle string) bool {
		for _, item := range perms {
			if s, ok := item.(string); ok && s == needle {
				return true
			}
		}
		return false
	}
	if !containsPerm("members.invite") {
		t.Error("verified owner must have members.invite in permissions")
	}
	if !containsPerm("api_keys.write") {
		t.Error("verified owner must have api_keys.write in permissions")
	}
}
