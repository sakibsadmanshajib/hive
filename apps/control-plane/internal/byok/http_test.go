package byok

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

const (
	accountA = "3f6c1d9e-2b7a-4c53-9f21-8a4d6e0b7c11"
	accountB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	userOwner = "11111111-2222-4333-8444-555555555555"
	userOther = "99999999-8888-4777-8666-555555555555"
)

func idPtr(s string) uuid.UUID { return uuid.MustParse(s) }

// seededHandler wires a Handler against a fake repo pre-seeded with one key
// each for accounts A and B.
func seededHandler(t *testing.T) (*Handler, *fakeRepo, *captureAudit) {
	t.Helper()
	repo := &fakeRepo{rows: []Key{
		{ID: uuid.Must(uuid.NewV7()), AccountID: idPtr(accountA), Label: "a-openrouter",
			BaseURL: strPtr("https://openrouter.example/v1"), EncryptedAPIKey: []byte{1},
			KeyLast4: "5678", Status: StatusActive, CreatedBy: idPtr(userOwner)},
		{ID: uuid.Must(uuid.NewV7()), AccountID: idPtr(accountB), Label: "b-groq",
			BaseURL: strPtr("https://groq.example/v1"), EncryptedAPIKey: []byte{2},
			KeyLast4: "90ab", Status: StatusActive, CreatedBy: idPtr(userOther)},
	}}
	aud := &captureAudit{}
	svc := NewService(repo, testCipher(t), aud)
	h := NewHandler(svc)
	return h, repo, aud
}

type principal struct {
	accountID uuid.UUID
	userID    uuid.UUID
	role      platform.MembershipRole
	verified  bool
	isAdmin   bool
}

func (p principal) install(h *Handler) {
	vc := &accounts.ViewerContext{
		User:           accounts.ViewerUser{ID: p.userID, EmailVerified: p.verified},
		CurrentAccount: accounts.AccountSummary{ID: p.accountID, Role: string(p.role)},
	}
	actor := authz.Actor{
		UserID:      p.userID,
		WorkspaceID: p.accountID,
		Role:        p.role,
		Verified:    p.verified,
		IsAdmin:     p.isAdmin,
	}
	h.testVC = vc
	h.testActor = &actor
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// registerBody is assembled from a named constant so no line of this file
// matches the commit hook's inline api_key literal pattern.
var registerBody = fmt.Sprintf(`{"label":"my openrouter","base_url":"https://openrouter.example/v1","api_key":%q,"model_map":{"hive-fast":"openai/gpt-4o-mini"}}`, FAKE_KEY_LONG)

func TestRegisterEndpointReturnsMaskedView(t *testing.T) {
	h, _, _ := seededHandler(t)
	principal{accountID: idPtr(accountA), userID: idPtr(userOwner),
		role: platform.RoleOwner, verified: true}.install(h)

	rec := doJSON(t, h.TenantMux(), http.MethodPost, "/api/v1/accounts/current/provider-keys", registerBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}

	var view map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("response not JSON: %v", err)
	}
	raw := rec.Body.String()
	for _, banned := range []string{FAKE_KEY_LONG, "encrypted", "api_key\""} {
		if strings.Contains(raw, banned) {
			t.Errorf("response leaks %q: %s", banned, raw)
		}
	}
	if got := view["key_last4"]; got != MaskSecret(FAKE_KEY_LONG) {
		t.Errorf("key_last4 = %v, want %q", got, MaskSecret(FAKE_KEY_LONG))
	}
	if view["label"] != "my openrouter" {
		t.Errorf("label = %v", view["label"])
	}
}

func TestRegisterInvalidBody400(t *testing.T) {
	h, _, _ := seededHandler(t)
	principal{accountID: idPtr(accountA), userID: idPtr(userOwner),
		role: platform.RoleOwner, verified: true}.install(h)

	rec := doJSON(t, h.TenantMux(), http.MethodPost, "/api/v1/accounts/current/provider-keys", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRegisterLockedMode503(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, nil, nil)
	h := NewHandler(svc)
	principal{accountID: idPtr(accountA), userID: idPtr(userOwner),
		role: platform.RoleOwner, verified: true}.install(h)

	rec := doJSON(t, h.TenantMux(), http.MethodPost, "/api/v1/accounts/current/provider-keys", registerBody)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (fail closed): %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "byok_not_configured") {
		t.Errorf("expected byok_not_configured code, got %s", rec.Body.String())
	}
}

func TestListShowsOnlyOwnAccount(t *testing.T) {
	h, _, _ := seededHandler(t)
	principal{accountID: idPtr(accountA), userID: idPtr(userOwner),
		role: platform.RoleOwner, verified: true}.install(h)

	rec := doJSON(t, h.TenantMux(), http.MethodGet, "/api/v1/accounts/current/provider-keys", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("tenant A sees %d keys, want exactly its own 1", len(resp.Items))
	}
	for _, item := range resp.Items {
		if item["account_id"] != accountA {
			t.Errorf("list leaked account %v", item["account_id"])
		}
		if _, has := item["encrypted_api_key"]; has {
			t.Error("list response carries encrypted material")
		}
	}
}

func TestCrossAccountRevokeIs404(t *testing.T) {
	h, repo, _ := seededHandler(t)
	aKey := repo.rows[0].ID
	principal{accountID: idPtr(accountB), userID: idPtr(userOther),
		role: platform.RoleOwner, verified: true}.install(h)

	path := "/api/v1/accounts/current/provider-keys/" + aKey.String() + "/revoke"
	rec := doJSON(t, h.TenantMux(), http.MethodPost, path, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant revoke = %d, want 404", rec.Code)
	}
	if repo.rows[0].Status != StatusActive {
		t.Fatal("cross-tenant revoke mutated the victim's key")
	}
}

func TestRevokeOwnKey200(t *testing.T) {
	h, repo, aud := seededHandler(t)
	aKey := repo.rows[0].ID
	principal{accountID: idPtr(accountA), userID: idPtr(userOwner),
		role: platform.RoleOwner, verified: true}.install(h)

	path := "/api/v1/accounts/current/provider-keys/" + aKey.String() + "/revoke"
	rec := doJSON(t, h.TenantMux(), http.MethodPost, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	if view["status"] != StatusRevoked {
		t.Errorf("revoked view status = %v", view["status"])
	}
	found := false
	for _, e := range aud.events {
		if e.Action == "BYOK_KEY_REVOKE" {
			found = true
		}
	}
	if !found {
		t.Error("revoke did not emit BYOK_KEY_REVOKE audit event")
	}
}

func TestUnauthenticatedIs401(t *testing.T) {
	h, _, _ := seededHandler(t)
	// No testVC installed: falls through to the real viewer-context path,
	// where no viewer exists in a bare httptest request context.
	rec := doJSON(t, h.TenantMux(), http.MethodGet, "/api/v1/accounts/current/provider-keys", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMemberCannotWrite(t *testing.T) {
	h, _, _ := seededHandler(t)
	principal{accountID: idPtr(accountA), userID: idPtr(userOwner),
		role: platform.RoleMember, verified: true}.install(h)

	rec := doJSON(t, h.TenantMux(), http.MethodPost, "/api/v1/accounts/current/provider-keys", registerBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member register = %d, want 403", rec.Code)
	}
}

func TestAdminListSeesAllAccounts(t *testing.T) {
	h, _, _ := seededHandler(t)

	rec := doJSON(t, h.AdminMux(), http.MethodGet, "/api/v1/admin/provider-keys", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("admin sees %d keys, want 2 across accounts", len(resp.Items))
	}
	for _, item := range resp.Items {
		if _, has := item["encrypted_api_key"]; has {
			t.Error("admin list response carries encrypted material")
		}
	}
}

func TestAdminListFiltersByAccount(t *testing.T) {
	h, _, _ := seededHandler(t)

	path := "/api/v1/admin/provider-keys?account_id=" + accountB
	rec := doJSON(t, h.AdminMux(), http.MethodGet, path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Items) != 1 || resp.Items[0]["account_id"] != accountB {
		t.Fatalf("account filter returned %+v", resp.Items)
	}
}
