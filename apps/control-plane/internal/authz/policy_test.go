package authz

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hivegpt/hive/apps/control-plane/internal/platform"
)

// matrixCase is one cell in the permission decision matrix.
// This file IS the permission specification — read it to understand what each
// role/verified/perm combination resolves to.
type matrixCase struct {
	name    string
	role    platform.MembershipRole
	verified bool
	isAdmin  bool
	perm    Permission
	want    bool
}

// TestPolicyMatrix enumerates all 55 cells of the decision matrix (11 perms x 5 actor states).
// Actor states: owner+verified, owner+unverified, member+verified, member+unverified, admin(any).
func TestPolicyMatrix(t *testing.T) {
	t.Parallel()

	// Shorthand role constants used in the table below.
	const (
		owner  = platform.RoleOwner
		member = platform.RoleMember
	)

	cases := []matrixCase{
		// --- billing.view ---
		// owner+verified: Y (RequiresVerified=false, owner role)
		{perm: PermBillingView, role: owner, verified: true, isAdmin: false, want: true},
		// owner+unverified: Y (RequiresVerified=false, no verification gate)
		{perm: PermBillingView, role: owner, verified: false, isAdmin: false, want: true},
		// member+verified: N (owner-only resource)
		{perm: PermBillingView, role: member, verified: true, isAdmin: false, want: false},
		// member+unverified: N
		{perm: PermBillingView, role: member, verified: false, isAdmin: false, want: false},
		// admin (any role/verified): Y
		{perm: PermBillingView, role: "", verified: false, isAdmin: true, want: true},

		// --- billing.write ---
		// owner+verified: Y
		{perm: PermBillingWrite, role: owner, verified: true, isAdmin: false, want: true},
		// owner+unverified: N (RequiresVerified=true)
		{perm: PermBillingWrite, role: owner, verified: false, isAdmin: false, want: false},
		// member+verified: N (owner only)
		{perm: PermBillingWrite, role: member, verified: true, isAdmin: false, want: false},
		// member+unverified: N
		{perm: PermBillingWrite, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y
		{perm: PermBillingWrite, role: "", verified: false, isAdmin: true, want: true},

		// --- api_keys.read ---
		// owner+verified: Y (RequiresVerified=false, owner role)
		{perm: PermAPIKeysRead, role: owner, verified: true, isAdmin: false, want: true},
		// owner+unverified: Y (RequiresVerified=false)
		{perm: PermAPIKeysRead, role: owner, verified: false, isAdmin: false, want: true},
		// member+verified: N (owner only)
		{perm: PermAPIKeysRead, role: member, verified: true, isAdmin: false, want: false},
		// member+unverified: N
		{perm: PermAPIKeysRead, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y
		{perm: PermAPIKeysRead, role: "", verified: false, isAdmin: true, want: true},

		// --- api_keys.write ---
		// owner+verified: Y
		{perm: PermAPIKeysWrite, role: owner, verified: true, isAdmin: false, want: true},
		// owner+unverified: N (RequiresVerified=true)
		{perm: PermAPIKeysWrite, role: owner, verified: false, isAdmin: false, want: false},
		// member+verified: N (owner only)
		{perm: PermAPIKeysWrite, role: member, verified: true, isAdmin: false, want: false},
		// member+unverified: N
		{perm: PermAPIKeysWrite, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y
		{perm: PermAPIKeysWrite, role: "", verified: false, isAdmin: true, want: true},

		// --- analytics.view ---
		// owner+verified: Y (any verified actor; audit: no role check in accounting/usage)
		{perm: PermAnalyticsView, role: owner, verified: true, isAdmin: false, want: true},
		// owner+unverified: N (RequiresVerified=true)
		{perm: PermAnalyticsView, role: owner, verified: false, isAdmin: false, want: false},
		// member+verified: Y (any verified actor per audit)
		{perm: PermAnalyticsView, role: member, verified: true, isAdmin: false, want: true},
		// member+unverified: N
		{perm: PermAnalyticsView, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y
		{perm: PermAnalyticsView, role: "", verified: false, isAdmin: true, want: true},

		// --- members.invite ---
		// owner+verified: Y
		{perm: PermMembersInvite, role: owner, verified: true, isAdmin: false, want: true},
		// owner+unverified: N
		{perm: PermMembersInvite, role: owner, verified: false, isAdmin: false, want: false},
		// member+verified: N (owner only)
		{perm: PermMembersInvite, role: member, verified: true, isAdmin: false, want: false},
		// member+unverified: N
		{perm: PermMembersInvite, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y
		{perm: PermMembersInvite, role: "", verified: false, isAdmin: true, want: true},

		// --- members.manage ---
		// owner+verified: Y
		{perm: PermMembersManage, role: owner, verified: true, isAdmin: false, want: true},
		// owner+unverified: N
		{perm: PermMembersManage, role: owner, verified: false, isAdmin: false, want: false},
		// member+verified: N (owner only)
		{perm: PermMembersManage, role: member, verified: true, isAdmin: false, want: false},
		// member+unverified: N
		{perm: PermMembersManage, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y
		{perm: PermMembersManage, role: "", verified: false, isAdmin: true, want: true},

		// --- workspace.settings ---
		// owner+verified: Y
		{perm: PermWorkspaceSettings, role: owner, verified: true, isAdmin: false, want: true},
		// owner+unverified: N
		{perm: PermWorkspaceSettings, role: owner, verified: false, isAdmin: false, want: false},
		// member+verified: N (owner only)
		{perm: PermWorkspaceSettings, role: member, verified: true, isAdmin: false, want: false},
		// member+unverified: N
		{perm: PermWorkspaceSettings, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y
		{perm: PermWorkspaceSettings, role: "", verified: false, isAdmin: true, want: true},

		// --- grants.create ---
		// owner+verified: N (admin-only)
		{perm: PermGrantsCreate, role: owner, verified: true, isAdmin: false, want: false},
		// owner+unverified: N
		{perm: PermGrantsCreate, role: owner, verified: false, isAdmin: false, want: false},
		// member+verified: N
		{perm: PermGrantsCreate, role: member, verified: true, isAdmin: false, want: false},
		// member+unverified: N
		{perm: PermGrantsCreate, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y (only admin can create grants)
		{perm: PermGrantsCreate, role: "", verified: false, isAdmin: true, want: true},

		// --- ledger.view ---
		// owner+verified: Y (any verified actor; audit: ledger/http.go:165 gates on !EmailVerified only)
		{perm: PermLedgerView, role: owner, verified: true, isAdmin: false, want: true},
		// owner+unverified: N (RequiresVerified=true)
		{perm: PermLedgerView, role: owner, verified: false, isAdmin: false, want: false},
		// member+verified: Y (any verified actor per audit)
		{perm: PermLedgerView, role: member, verified: true, isAdmin: false, want: true},
		// member+unverified: N
		{perm: PermLedgerView, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y
		{perm: PermLedgerView, role: "", verified: false, isAdmin: true, want: true},

		// --- platform.admin ---
		// owner+verified: N (admin-only overlay)
		{perm: PermPlatformAdmin, role: owner, verified: true, isAdmin: false, want: false},
		// owner+unverified: N
		{perm: PermPlatformAdmin, role: owner, verified: false, isAdmin: false, want: false},
		// member+verified: N
		{perm: PermPlatformAdmin, role: member, verified: true, isAdmin: false, want: false},
		// member+unverified: N
		{perm: PermPlatformAdmin, role: member, verified: false, isAdmin: false, want: false},
		// admin: Y
		{perm: PermPlatformAdmin, role: "", verified: false, isAdmin: true, want: true},
	}

	pol := Policy{}

	for _, tc := range cases {
		tc := tc
		name := fmt.Sprintf("%s/%s/v=%t/admin=%t", tc.perm, tc.role, tc.verified, tc.isAdmin)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			actor := Actor{
				Role:     tc.role,
				Verified: tc.verified,
				IsAdmin:  tc.isAdmin,
			}
			got := pol.Can(actor, tc.perm)
			if got != tc.want {
				t.Errorf("Policy.Can(%q) with role=%q verified=%t isAdmin=%t: got %v, want %v",
					tc.perm, tc.role, tc.verified, tc.isAdmin, got, tc.want)
			}
		})
	}
}

// TestPolicyAllGrantedReturnsSorted asserts AllGranted returns lexically sorted strings.
func TestPolicyAllGrantedReturnsSorted(t *testing.T) {
	t.Parallel()
	pol := Policy{}
	actor := Actor{Role: platform.RoleOwner, Verified: true, IsAdmin: false}
	granted := pol.AllGranted(actor)
	if len(granted) == 0 {
		t.Fatal("expected non-empty granted list for verified owner")
	}
	for i := 1; i < len(granted); i++ {
		if granted[i] < granted[i-1] {
			t.Errorf("AllGranted not sorted: %q < %q at index %d", granted[i], granted[i-1], i)
		}
	}
}

// TestRequirePermissionMiddleware covers the 3 core middleware paths.
func TestRequirePermissionMiddleware(t *testing.T) {
	t.Parallel()

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	t.Run("no viewer returns 401", func(t *testing.T) {
		nextCalled = false
		resolver := ActorResolver(func(r *http.Request) (Actor, error) {
			return Actor{}, ErrNoViewer
		})
		mw := NewMiddleware(resolver)
		h := mw.RequirePermission(PermBillingView)(next)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("want 401, got %d", rec.Code)
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["error"] != "authentication required" {
			t.Errorf("want error=authentication required, got %q", body["error"])
		}
		if nextCalled {
			t.Error("next should not have been called")
		}
	})

	t.Run("denied actor returns 403", func(t *testing.T) {
		nextCalled = false
		// member actor cannot hold billing.view
		resolver := ActorResolver(func(r *http.Request) (Actor, error) {
			return Actor{Role: platform.RoleMember, Verified: true, IsAdmin: false}, nil
		})
		mw := NewMiddleware(resolver)
		h := mw.RequirePermission(PermBillingView)(next)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if rec.Code != http.StatusForbidden {
			t.Errorf("want 403, got %d", rec.Code)
		}
		var body map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["error"] != "permission denied" {
			t.Errorf("want error=permission denied, got %q", body["error"])
		}
		if nextCalled {
			t.Error("next should not have been called")
		}
	})

	t.Run("allowed actor calls next", func(t *testing.T) {
		nextCalled = false
		resolver := ActorResolver(func(r *http.Request) (Actor, error) {
			return Actor{Role: platform.RoleOwner, Verified: true, IsAdmin: false}, nil
		})
		mw := NewMiddleware(resolver)
		h := mw.RequirePermission(PermBillingView)(next)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if rec.Code != http.StatusOK {
			t.Errorf("want 200, got %d", rec.Code)
		}
		if !nextCalled {
			t.Error("next should have been called")
		}
	})
}

// TestUnknownPermissionRequiresVerified asserts unknown perms return false.
func TestUnknownPermissionRequiresVerified(t *testing.T) {
	t.Parallel()
	if RequiresVerified(Permission("not.a.perm")) {
		t.Error("expected RequiresVerified=false for unknown permission")
	}
}

// TestPolicyCanDefaultDeny asserts that Policy.Can returns false for any
// Permission not present in the registry, even for the most privileged actor.
// Guards against silent fail-open behaviour when typos slip through or when
// a new Permission constant is added without a matching switch case.
func TestPolicyCanDefaultDeny(t *testing.T) {
	t.Parallel()
	pol := NewPolicy()
	unknown := Permission("not.a.real.perm")
	actors := []struct {
		name  string
		actor Actor
	}{
		{"owner+verified", Actor{Role: platform.RoleOwner, Verified: true}},
		{"owner+unverified", Actor{Role: platform.RoleOwner, Verified: false}},
		{"member+verified", Actor{Role: platform.RoleMember, Verified: true}},
		{"member+unverified", Actor{Role: platform.RoleMember, Verified: false}},
		{"admin+verified", Actor{Role: platform.RoleOwner, Verified: true, IsAdmin: true}},
		{"admin+unverified", Actor{IsAdmin: true}},
	}
	for _, tc := range actors {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if pol.Can(tc.actor, unknown) {
				t.Errorf("Policy.Can returned true for unknown perm with actor %+v; want default-deny", tc.actor)
			}
		})
	}
}

// TestRequirePermissionMiddlewareResolverError covers non-ErrNoViewer resolver errors.
func TestRequirePermissionMiddlewareResolverError(t *testing.T) {
	t.Parallel()
	resolver := ActorResolver(func(r *http.Request) (Actor, error) {
		return Actor{}, errors.New("db connection failed")
	})
	mw := NewMiddleware(resolver)
	h := mw.RequirePermission(PermBillingView)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", rec.Code)
	}
}
