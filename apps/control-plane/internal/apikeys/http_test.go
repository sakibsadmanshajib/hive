package apikeys

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
)

// newTestHandler builds a Handler backed by a stub repo and a test viewerContext override.
func newTestHandler(vc accounts.ViewerContext) (*Handler, *stubRepo) {
	repo := newStubRepo()
	svc := NewService(repo)
	h := &Handler{svc: svc, testVC: &vc}
	return h, repo
}

// doRequest runs a single HTTP request against the handler.
func doRequest(t *testing.T, h *Handler, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// decodeBody decodes a JSON response body into a map.
func decodeBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v, body=%s", err, rr.Body.String())
	}
	return m
}

func ownerVC() accounts.ViewerContext {
	return accounts.ViewerContext{
		User:           accounts.ViewerUser{ID: uuid.New(), Email: "test@hive.com", EmailVerified: true},
		CurrentAccount: accounts.AccountSummary{ID: uuid.New(), Role: "owner"},
		Gates:          accounts.Gates{CanManageAPIKeys: true},
	}
}

func TestCreateKeyReturnsSecretOnlyOnCreate(t *testing.T) {
	h, _ := newTestHandler(ownerVC())
	base := "/api/v1/accounts/current/api-keys"

	// Create should return secret.
	rr := doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "test-key"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	body := decodeBody(t, rr)
	secret, ok := body["secret"]
	if !ok || secret == "" {
		t.Fatal("create response must include secret")
	}

	// List should NOT return secrets.
	rr = doRequest(t, h, http.MethodGet, base, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", rr.Code)
	}
	listBody := decodeBody(t, rr)
	items := listBody["items"].([]interface{})
	if len(items) == 0 {
		t.Fatal("expected at least 1 item in list")
	}
	for _, item := range items {
		m := item.(map[string]interface{})
		if _, hasSecret := m["secret"]; hasSecret {
			t.Fatal("list response must NOT include secret")
		}
	}
}

func TestListKeysNeverReturnsSecret(t *testing.T) {
	h, _ := newTestHandler(ownerVC())
	base := "/api/v1/accounts/current/api-keys"

	doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "key1"})
	doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "key2"})

	rr := doRequest(t, h, http.MethodGet, base, nil)
	listBody := decodeBody(t, rr)
	items := listBody["items"].([]interface{})
	for i, item := range items {
		m := item.(map[string]interface{})
		if _, has := m["secret"]; has {
			t.Fatalf("item %d must not have secret", i)
		}
	}
}

func TestListKeysReturnsCustomerVisibleSummaries(t *testing.T) {
	h, _ := newTestHandler(ownerVC())
	base := "/api/v1/accounts/current/api-keys"

	doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "launch-key"})

	rr := doRequest(t, h, http.MethodGet, base, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	body := decodeBody(t, rr)
	items := body["items"].([]interface{})
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0].(map[string]interface{})
	if _, ok := item["created_at"].(string); !ok {
		t.Fatal("created_at must be a string timestamp")
	}
	if _, ok := item["updated_at"].(string); !ok {
		t.Fatal("updated_at must be a string timestamp")
	}
	if _, ok := item["expires_at"]; !ok {
		t.Fatal("expires_at must be present even when null")
	}
	if _, ok := item["last_used_at"]; !ok {
		t.Fatal("last_used_at must be present even when null")
	}

	expirationSummary := item["expiration_summary"].(map[string]interface{})
	if expirationSummary["kind"] != "never" || expirationSummary["label"] != "Never expires" {
		t.Fatalf("unexpected expiration summary: %#v", expirationSummary)
	}

	budgetSummary := item["budget_summary"].(map[string]interface{})
	if budgetSummary["kind"] != "none" || budgetSummary["label"] != "No budget cap" {
		t.Fatalf("unexpected budget summary: %#v", budgetSummary)
	}

	allowlistSummary := item["allowlist_summary"].(map[string]interface{})
	if allowlistSummary["mode"] != "groups" || allowlistSummary["label"] != "Default launch-safe models" {
		t.Fatalf("unexpected allowlist summary: %#v", allowlistSummary)
	}
	groupNames := allowlistSummary["group_names"].([]interface{})
	if len(groupNames) != 1 || groupNames[0] != "default" {
		t.Fatalf("unexpected allowlist group names: %#v", groupNames)
	}
}

func TestGetKeyReturnsSummariesWithoutSecret(t *testing.T) {
	h, _ := newTestHandler(ownerVC())
	base := "/api/v1/accounts/current/api-keys"

	create := doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "inspect-me"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", create.Code, create.Body.String())
	}
	createBody := decodeBody(t, create)
	keyID := createBody["id"].(string)

	rr := doRequest(t, h, http.MethodGet, base+"/"+keyID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get key: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	body := decodeBody(t, rr)
	if _, hasSecret := body["secret"]; hasSecret {
		t.Fatal("detail response must not include secret")
	}

	expirationSummary := body["expiration_summary"].(map[string]interface{})
	if expirationSummary["kind"] != "never" || expirationSummary["label"] != "Never expires" {
		t.Fatalf("unexpected expiration summary: %#v", expirationSummary)
	}

	budgetSummary := body["budget_summary"].(map[string]interface{})
	if budgetSummary["kind"] != "none" || budgetSummary["label"] != "No budget cap" {
		t.Fatalf("unexpected budget summary: %#v", budgetSummary)
	}

	allowlistSummary := body["allowlist_summary"].(map[string]interface{})
	if allowlistSummary["mode"] != "groups" || allowlistSummary["label"] != "Default launch-safe models" {
		t.Fatalf("unexpected allowlist summary: %#v", allowlistSummary)
	}
}

func TestPolicyRouteUpdatesSummaries(t *testing.T) {
	h, _ := newTestHandler(ownerVC())
	base := "/api/v1/accounts/current/api-keys"

	create := doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "policy-target"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", create.Code, create.Body.String())
	}
	createBody := decodeBody(t, create)
	keyID := createBody["id"].(string)

	update := doRequest(t, h, http.MethodPost, base+"/"+keyID+"/policy", map[string]interface{}{
		"allow_all_models":     true,
		"budget_kind":          "monthly",
		"budget_limit_credits": 900,
	})
	if update.Code != http.StatusOK {
		t.Fatalf("policy update: expected 200, got %d: %s", update.Code, update.Body.String())
	}
	updateBody := decodeBody(t, update)
	if updateBody["policy_version"].(float64) != 2 {
		t.Fatalf("expected policy version 2, got %#v", updateBody["policy_version"])
	}

	detail := doRequest(t, h, http.MethodGet, base+"/"+keyID, nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d: %s", detail.Code, detail.Body.String())
	}
	body := decodeBody(t, detail)

	budgetSummary := body["budget_summary"].(map[string]interface{})
	if budgetSummary["kind"] != "monthly" || budgetSummary["label"] != "Monthly budget cap: 900 credits" {
		t.Fatalf("unexpected budget summary after policy update: %#v", budgetSummary)
	}

	allowlistSummary := body["allowlist_summary"].(map[string]interface{})
	if allowlistSummary["mode"] != "all" || allowlistSummary["label"] != "All models" {
		t.Fatalf("unexpected allowlist summary after policy update: %#v", allowlistSummary)
	}
}

func TestRotateKeyRevokesOnlyTarget(t *testing.T) {
	h, _ := newTestHandler(ownerVC())
	base := "/api/v1/accounts/current/api-keys"

	rr1 := doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "key1"})
	doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "key2"})
	body1 := decodeBody(t, rr1)
	keyID1 := body1["id"].(string)

	rr := doRequest(t, h, http.MethodPost, base+"/"+keyID1+"/rotate", map[string]string{
		"nickname": "rotated-key1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	rotateBody := decodeBody(t, rr)
	if rotateBody["old_key_id"] != keyID1 {
		t.Fatal("old_key_id must match rotated key")
	}
	newKeyMap := rotateBody["new_key"].(map[string]interface{})
	if _, has := newKeyMap["secret"]; !has {
		t.Fatal("new_key must include secret")
	}
}

func TestDisableAndEnableKeyRoutes(t *testing.T) {
	h, _ := newTestHandler(ownerVC())
	base := "/api/v1/accounts/current/api-keys"

	rr := doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "toggle"})
	body := decodeBody(t, rr)
	keyID := body["id"].(string)

	// Disable.
	rr = doRequest(t, h, http.MethodPost, base+"/"+keyID+"/disable", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body = decodeBody(t, rr)
	if body["status"] != "disabled" {
		t.Fatalf("expected disabled, got %s", body["status"])
	}

	// Enable.
	rr = doRequest(t, h, http.MethodPost, base+"/"+keyID+"/enable", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("enable: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	body = decodeBody(t, rr)
	if body["status"] != "active" {
		t.Fatalf("expected active, got %s", body["status"])
	}
}

func TestAPIKeyRoutesRequireVerifiedOwner(t *testing.T) {
	vc := accounts.ViewerContext{
		User:           accounts.ViewerUser{ID: uuid.New(), Email: "test@hive.com", EmailVerified: true},
		CurrentAccount: accounts.AccountSummary{ID: uuid.New(), Role: "member"},
		Gates:          accounts.Gates{CanManageAPIKeys: false},
	}
	h, _ := newTestHandler(vc)
	base := "/api/v1/accounts/current/api-keys"

	rr := doRequest(t, h, http.MethodGet, base, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner, got %d", rr.Code)
	}
	body := decodeBody(t, rr)
	if body["code"] != "api_key_management_forbidden" {
		t.Fatalf("expected code api_key_management_forbidden, got %s", body["code"])
	}
}

func TestInternalResolveRouteIncludesSeparateRatePolicyFields(t *testing.T) {
	h, repo := newTestHandler(ownerVC())
	base := "/api/v1/accounts/current/api-keys"

	create := doRequest(t, h, http.MethodPost, base, map[string]string{"nickname": "resolver"})
	if create.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", create.Code, create.Body.String())
	}
	createBody := decodeBody(t, create)
	keyID, err := uuid.Parse(createBody["id"].(string))
	if err != nil {
		t.Fatalf("parse key id: %v", err)
	}
	key := repo.keys[keyID]

	limit := int64(900)
	repo.policies[keyID] = KeyPolicy{
		APIKeyID:           keyID,
		AllowedGroupNames:  []string{"default"},
		BudgetKind:         "monthly",
		BudgetLimitCredits: &limit,
		PolicyVersion:      2,
	}
	now := time.Now().UTC()
	windowStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	repo.budgetWindows[budgetWindowKey(keyID, "monthly", windowStart)] = BudgetWindow{
		APIKeyID:        keyID,
		WindowKind:      "monthly",
		WindowStart:     windowStart,
		ConsumedCredits: 111,
		ReservedCredits: 22,
	}
	repo.accountRatePolicy[key.AccountID] = RatePolicy{RateLimitRPM: 120, RateLimitTPM: 240000, FreeTokenWeightTenths: 1}
	repo.keyRatePolicy[keyID] = RatePolicy{RateLimitRPM: 12, RateLimitTPM: 24000, FreeTokenWeightTenths: 3}

	rr := doRequest(t, h, http.MethodPost, "/internal/apikeys/resolve", map[string]string{"token_hash": key.TokenHash})
	if rr.Code != http.StatusOK {
		t.Fatalf("resolve: expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	body := decodeBody(t, rr)
	if body["budget_consumed_credits"].(float64) != 111 {
		t.Fatalf("expected consumed credits 111, got %#v", body["budget_consumed_credits"])
	}
	if body["budget_reserved_credits"].(float64) != 22 {
		t.Fatalf("expected reserved credits 22, got %#v", body["budget_reserved_credits"])
	}
	if _, ok := body["account_rate_policy"].(map[string]interface{}); !ok {
		t.Fatalf("expected account_rate_policy map, got %#v", body["account_rate_policy"])
	}
	if _, ok := body["key_rate_policy"].(map[string]interface{}); !ok {
		t.Fatalf("expected key_rate_policy map, got %#v", body["key_rate_policy"])
	}
}
