package grants_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/grants"
)

// =============================================================================
// stub middleware that mirrors platform.RequirePlatformAdmin behaviour for
// HTTP-level tests. We do NOT import the platform package here to keep the
// grants tests free of cyclic concerns; the platform tests already prove the
// middleware's correctness.
// =============================================================================

func adminGate(admins map[uuid.UUID]bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		viewer, ok := auth.ViewerFromContext(r.Context())
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"authentication required"}`))
			return
		}
		if !admins[viewer.UserID] {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"insufficient permissions"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// =============================================================================
// Helpers — re-use the fake repo / admin from service_test (same package).
// =============================================================================

func newTestService(adminUserIDs ...uuid.UUID) (*grants.Service, *fakeRepo, map[uuid.UUID]bool) {
	repo := newFakeRepo()
	admins := map[uuid.UUID]bool{}
	for _, id := range adminUserIDs {
		admins[id] = true
	}
	admin := &fakeAdmin{admins: admins}
	return grants.NewService(repo, admin), repo, admins
}

// =============================================================================
// POST /v1/admin/credit-grants
// =============================================================================

func TestHandlerCreate_AdminSuccess(t *testing.T) {
	t.Parallel()
	adminID := uuid.New()
	svc, repo, admins := newTestService(adminID)
	h := grants.NewHandler(svc)
	mux := adminGate(admins, h.AdminMux())

	body := map[string]any{
		"granted_to_user_id":      uuid.New().String(),
		"granted_to_workspace_id": uuid.New().String(),
		"amount_bdt_subunits":     "75000",
		"reason_note":             "Apology credit",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credit-grants", bytes.NewReader(bodyBytes))
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: adminID}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(repo.grants) != 1 {
		t.Fatalf("expected 1 grant persisted, got %d", len(repo.grants))
	}
	var resp map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// Provider-blind: ensure no FX/USD keys appear in the response shape.
	rawBody := rec.Body.Bytes()
	for _, banned := range []string{"amount_usd", "usd_", "fx_", "exchange_rate", "price_per_credit_usd"} {
		if strings.Contains(string(rawBody), banned) {
			t.Fatalf("response leaked FX/USD key %q: %s", banned, rawBody)
		}
	}
}

func TestHandlerCreate_NonAdminForbidden(t *testing.T) {
	t.Parallel()
	adminID := uuid.New()
	svc, repo, admins := newTestService(adminID)
	h := grants.NewHandler(svc)
	mux := adminGate(admins, h.AdminMux())

	body := map[string]any{
		"granted_to_user_id":      uuid.New().String(),
		"granted_to_workspace_id": uuid.New().String(),
		"amount_bdt_subunits":     "1000",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credit-grants", bytes.NewReader(bodyBytes))
	// Non-admin viewer
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: uuid.New()}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if len(repo.grants) != 0 {
		t.Fatalf("expected zero grants persisted on 403, got %d", len(repo.grants))
	}
}

func TestHandlerCreate_Unauthenticated(t *testing.T) {
	t.Parallel()
	adminID := uuid.New()
	svc, _, admins := newTestService(adminID)
	h := grants.NewHandler(svc)
	mux := adminGate(admins, h.AdminMux())

	body := map[string]any{
		"granted_to_user_id":      uuid.New().String(),
		"granted_to_workspace_id": uuid.New().String(),
		"amount_bdt_subunits":     "1000",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credit-grants", bytes.NewReader(bodyBytes))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandlerCreate_InvalidAmount(t *testing.T) {
	t.Parallel()
	adminID := uuid.New()
	svc, _, admins := newTestService(adminID)
	h := grants.NewHandler(svc)
	mux := adminGate(admins, h.AdminMux())

	body := map[string]any{
		"granted_to_user_id":      uuid.New().String(),
		"granted_to_workspace_id": uuid.New().String(),
		"amount_bdt_subunits":     "0",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credit-grants", bytes.NewReader(bodyBytes))
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: adminID}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestHandlerCreate_InvalidAmountString(t *testing.T) {
	t.Parallel()
	adminID := uuid.New()
	svc, _, admins := newTestService(adminID)
	h := grants.NewHandler(svc)
	mux := adminGate(admins, h.AdminMux())

	body := map[string]any{
		"granted_to_user_id":      uuid.New().String(),
		"granted_to_workspace_id": uuid.New().String(),
		"amount_bdt_subunits":     "not-a-number",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credit-grants", bytes.NewReader(bodyBytes))
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: adminID}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// =============================================================================
// GET /v1/admin/credit-grants  (list-all)
// =============================================================================

func TestHandlerListAll_AdminSeesAll(t *testing.T) {
	t.Parallel()
	adminID := uuid.New()
	svc, _, admins := newTestService(adminID)
	h := grants.NewHandler(svc)
	mux := adminGate(admins, h.AdminMux())

	// seed 2 grants directly via service
	for i := 0; i < 2; i++ {
		_, err := svc.Create(context.Background(), grants.CreateInput{
			GrantedByUserID:      adminID,
			GrantedToUserID:      uuid.New(),
			GrantedToWorkspaceID: uuid.New(),
			AmountBDTSubunits:    big.NewInt(int64(1000 + i)),
		})
		if err != nil {
			t.Fatalf("seed grant: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/credit-grants", nil)
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: adminID}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
}

// =============================================================================
// GET /v1/credit-grants/me  (self-list, no admin gate)
// =============================================================================

func TestHandlerSelfList_AnyUser(t *testing.T) {
	t.Parallel()
	adminID := uuid.New()
	svc, _, _ := newTestService(adminID)
	h := grants.NewHandler(svc)
	selfMux := h.SelfMux()

	user := uuid.New()
	other := uuid.New()
	// admin grants to user
	if _, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      adminID,
		GrantedToUserID:      user,
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(5000),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// admin grants to other
	if _, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      adminID,
		GrantedToUserID:      other,
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(7000),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// user pulls own list — should see exactly 1
	req := httptest.NewRequest(http.MethodGet, "/v1/credit-grants/me", nil)
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: user}))
	rec := httptest.NewRecorder()
	selfMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 grant for self, got %d", len(resp.Items))
	}
}

func TestHandlerSelfList_Unauthenticated(t *testing.T) {
	t.Parallel()
	adminID := uuid.New()
	svc, _, _ := newTestService(adminID)
	h := grants.NewHandler(svc)
	selfMux := h.SelfMux()

	req := httptest.NewRequest(http.MethodGet, "/v1/credit-grants/me", nil)
	rec := httptest.NewRecorder()
	selfMux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

// =============================================================================
// Forbidden-error round-trip — when service returns ErrForbidden but the
// admin-gate middleware was bypassed (defensive double-check).
// =============================================================================

func TestHandlerCreate_ServiceForbiddenWhenGateBypassed(t *testing.T) {
	t.Parallel()
	// Construct a service with a non-admin viewer; bypass the admin gate
	// middleware to confirm the service-layer ErrForbidden also surfaces 403.
	svc, _, _ := newTestService() // no admins
	h := grants.NewHandler(svc)
	mux := h.AdminMux() // raw, no admin gate wrap

	user := uuid.New()
	body := map[string]any{
		"granted_to_user_id":      uuid.New().String(),
		"granted_to_workspace_id": uuid.New().String(),
		"amount_bdt_subunits":     "1000",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credit-grants", bytes.NewReader(bodyBytes))
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: user}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
	// Sanitised error
	var body2 map[string]string
	_ = json.NewDecoder(rec.Body).Decode(&body2)
	if !strings.Contains(body2["error"], "insufficient permissions") {
		t.Fatalf("expected sanitised error, got %v", body2)
	}
}

// =============================================================================
// Sanity: math/big invariant — validate the wire shape's amount_bdt_subunits
// is a string (decimal), not a number (which would be float-coerced by JSON).
// =============================================================================

func TestHandlerWireFormat_AmountIsString(t *testing.T) {
	t.Parallel()
	adminID := uuid.New()
	svc, _, admins := newTestService(adminID)
	h := grants.NewHandler(svc)
	mux := adminGate(admins, h.AdminMux())

	body := map[string]any{
		"granted_to_user_id":      uuid.New().String(),
		"granted_to_workspace_id": uuid.New().String(),
		"amount_bdt_subunits":     "999999999999",
	}
	bodyBytes, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credit-grants", bytes.NewReader(bodyBytes))
	req = req.WithContext(auth.WithViewer(req.Context(), auth.Viewer{UserID: adminID}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rec.Code)
	}
	var resp struct {
		Grant struct {
			AmountBDTSubunits json.RawMessage `json:"amount_bdt_subunits"`
		} `json:"grant"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(string(resp.Grant.AmountBDTSubunits), `"`) {
		t.Fatalf("amount_bdt_subunits MUST be string-encoded for math/big invariant, got %s", resp.Grant.AmountBDTSubunits)
	}
}

// =============================================================================
// Negative test: ErrForbidden is the expected path for a service.Create call
// with no admins configured (sanity: the contract is preserved when invoked
// directly).
// =============================================================================

func TestService_DirectCreateForbidden(t *testing.T) {
	t.Parallel()
	svc, _, _ := newTestService()
	_, err := svc.Create(context.Background(), grants.CreateInput{
		GrantedByUserID:      uuid.New(),
		GrantedToUserID:      uuid.New(),
		GrantedToWorkspaceID: uuid.New(),
		AmountBDTSubunits:    big.NewInt(1),
	})
	if !errors.Is(err, grants.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}
