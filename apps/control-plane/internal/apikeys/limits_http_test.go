package apikeys

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
)

// nonOwnerVC returns a viewer context whose CanManageAPIKeys gate is closed —
// matches a workspace member without owner role.
func nonOwnerVC(accountID uuid.UUID) accounts.ViewerContext {
	return accounts.ViewerContext{
		User:           accounts.ViewerUser{ID: uuid.New(), Email: "member@hive.com", EmailVerified: true},
		CurrentAccount: accounts.AccountSummary{ID: accountID, Role: "member"},
		Gates:          accounts.Gates{CanManageAPIKeys: false},
	}
}

// seedKey is a tiny helper that sets up a key for an owner viewer.
func seedKey(t *testing.T, h *Handler, repo *stubRepo, accountID uuid.UUID) uuid.UUID {
	t.Helper()
	rr := doRequest(t, h, http.MethodPost, "/api/v1/accounts/current/api-keys", map[string]string{"nickname": "k"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed key: expected 201, got %d", rr.Code)
	}
	body := decodeBody(t, rr)
	id, err := uuid.Parse(body["id"].(string))
	if err != nil {
		t.Fatalf("seed key id: %v", err)
	}
	// repo unused but kept in signature for potential future seeding
	_ = repo
	return id
}

func TestLimitsRoundTripOwner(t *testing.T) {
	owner := ownerVC()
	h, repo := newTestHandler(owner)
	keyID := seedKey(t, h, repo, owner.CurrentAccount.ID)

	// PUT new limits.
	put := map[string]interface{}{
		"rpm": 250,
		"tpm": 50000,
		"tier_overrides": map[string]interface{}{
			"verified": map[string]int{"rpm": 200, "tpm": 40000},
		},
	}
	rr := doRequest(t, h, http.MethodPut, "/api/v1/accounts/current/api-keys/"+keyID.String()+"/limits", put)
	if rr.Code != http.StatusOK {
		t.Fatalf("put limits: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	if body["rpm"].(float64) != 250 {
		t.Fatalf("expected rpm=250, got %#v", body["rpm"])
	}
	if body["tpm"].(float64) != 50000 {
		t.Fatalf("expected tpm=50000, got %#v", body["tpm"])
	}

	// GET round-trip.
	rr = doRequest(t, h, http.MethodGet, "/api/v1/accounts/current/api-keys/"+keyID.String()+"/limits", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get limits: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body = decodeBody(t, rr)
	overrides, ok := body["tier_overrides"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tier_overrides map, got %#v", body["tier_overrides"])
	}
	verified, ok := overrides["verified"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected verified override, got %#v", overrides)
	}
	if verified["rpm"].(float64) != 200 {
		t.Fatalf("expected verified rpm=200, got %#v", verified["rpm"])
	}
}

func TestLimitsForbiddenForNonOwner(t *testing.T) {
	owner := ownerVC()
	h, repo := newTestHandler(owner)
	keyID := seedKey(t, h, repo, owner.CurrentAccount.ID)

	// Swap viewer context to a non-owner.
	h.testVC = ptrViewerContext(nonOwnerVC(owner.CurrentAccount.ID))

	rr := doRequest(t, h, http.MethodGet, "/api/v1/accounts/current/api-keys/"+keyID.String()+"/limits", nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("get limits as non-owner: expected 403, got %d", rr.Code)
	}
	rr = doRequest(t, h, http.MethodPut, "/api/v1/accounts/current/api-keys/"+keyID.String()+"/limits", map[string]int{"rpm": 1, "tpm": 1})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("put limits as non-owner: expected 403, got %d", rr.Code)
	}
}

func TestLimitsForeignAccountReturns404(t *testing.T) {
	owner := ownerVC()
	h, repo := newTestHandler(owner)
	keyID := seedKey(t, h, repo, owner.CurrentAccount.ID)

	// Foreign-workspace owner — different account id, gate still open.
	foreign := ownerVC()
	h.testVC = ptrViewerContext(foreign)

	rr := doRequest(t, h, http.MethodGet, "/api/v1/accounts/current/api-keys/"+keyID.String()+"/limits", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get limits across accounts: expected 404, got %d", rr.Code)
	}
}

func TestLimitsOutOfRangeReturns422(t *testing.T) {
	owner := ownerVC()
	h, repo := newTestHandler(owner)
	keyID := seedKey(t, h, repo, owner.CurrentAccount.ID)

	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{name: "negative rpm", body: map[string]interface{}{"rpm": -1, "tpm": 100}},
		{name: "rpm too large", body: map[string]interface{}{"rpm": 999999, "tpm": 100}},
		{name: "tpm too large", body: map[string]interface{}{"rpm": 60, "tpm": 999999999}},
		{name: "unknown tier", body: map[string]interface{}{"rpm": 60, "tpm": 100, "tier_overrides": map[string]map[string]int{"platinum": {"rpm": 1, "tpm": 1}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doRequest(t, h, http.MethodPut, "/api/v1/accounts/current/api-keys/"+keyID.String()+"/limits", tc.body)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected 422, got %d: %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func ptrViewerContext(vc accounts.ViewerContext) *accounts.ViewerContext { return &vc }
