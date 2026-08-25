package budgets

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// HTTP behavior for the Phase 14 workspace spend-alert surface and the
// internal hard-cap read the edge-api gate falls back to.
//
// Invariants:
//   - Alert mutations are owner-gated: a verified member gets 403 on every
//     mutation verb, so a guessed workspace UUID grants nothing.
//   - A foreign alert UUID answers 404, never a partial mutation.
//   - Invalid threshold percentages are refused with 400 before any write.
//   - The internal hard-cap endpoint renders *big.Int as a string (it can
//     exceed the JS safe integer range) and null when no budget is set.

type spendAlertsRepo struct {
	*workspaceRepoStub
	alerts  map[uuid.UUID][]SpendAlert
	budgets map[uuid.UUID]*Budget
}

func newSpendAlertsRepo() *spendAlertsRepo {
	return &spendAlertsRepo{
		workspaceRepoStub: &workspaceRepoStub{},
		alerts:            map[uuid.UUID][]SpendAlert{},
		budgets:           map[uuid.UUID]*Budget{},
	}
}

func (s *spendAlertsRepo) GetBudget(_ context.Context, ws uuid.UUID) (*Budget, error) {
	if b, ok := s.budgets[ws]; ok && b != nil {
		cp := *b
		cp.SoftCap = new(big.Int).Set(b.SoftCap)
		cp.HardCap = new(big.Int).Set(b.HardCap)
		return &cp, nil
	}
	return nil, nil
}

func (s *spendAlertsRepo) UpsertBudget(_ context.Context, in SetBudgetInput) (*Budget, error) {
	b := &Budget{
		WorkspaceID: in.WorkspaceID,
		PeriodStart: in.PeriodStart,
		SoftCap:     new(big.Int).Set(in.SoftCap),
		HardCap:     new(big.Int).Set(in.HardCap),
		Currency:    "BDT",
	}
	s.budgets[in.WorkspaceID] = b
	return b, nil
}

func (s *spendAlertsRepo) DeleteBudget(_ context.Context, ws uuid.UUID) error {
	delete(s.budgets, ws)
	return nil
}

func (s *spendAlertsRepo) ListAlerts(_ context.Context, ws uuid.UUID) ([]SpendAlert, error) {
	list := s.alerts[ws]
	out := make([]SpendAlert, len(list))
	copy(out, list)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ThresholdPct < out[j-1].ThresholdPct; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, nil
}

func (s *spendAlertsRepo) CreateAlert(_ context.Context, in CreateAlertInput) (*SpendAlert, error) {
	a := SpendAlert{
		ID:            uuid.New(),
		WorkspaceID:   in.WorkspaceID,
		ThresholdPct:  in.ThresholdPct,
		Email:         in.Email,
		WebhookURL:    in.WebhookURL,
		WebhookSecret: in.WebhookSecret,
		CreatedAt:     time.Now().UTC(),
	}
	s.alerts[in.WorkspaceID] = append(s.alerts[in.WorkspaceID], a)
	return &a, nil
}

func (s *spendAlertsRepo) UpdateAlert(_ context.Context, in UpdateAlertInput) (*SpendAlert, error) {
	for ws, list := range s.alerts {
		for i := range list {
			if list[i].ID == in.ID {
				if ws != in.WorkspaceID {
					return nil, ErrBudgetNotFound
				}
				if in.Email != nil {
					list[i].Email = in.Email
				}
				cp := list[i]
				return &cp, nil
			}
		}
	}
	return nil, ErrBudgetNotFound
}

func (s *spendAlertsRepo) DeleteAlert(_ context.Context, ws, alertID uuid.UUID) error {
	list := s.alerts[ws]
	for i := range list {
		if list[i].ID == alertID {
			s.alerts[ws] = append(list[:i], list[i+1:]...)
			return nil
		}
	}
	return ErrBudgetNotFound
}

func newSpendAlertsHandler(repo *spendAlertsRepo, ownerID, wsID uuid.UUID) http.Handler {
	roleSvc := platform.NewRoleService(&stubRoleStore{
		adminUsers: map[uuid.UUID]bool{},
		owners:     map[uuid.UUID]uuid.UUID{wsID: ownerID},
	})
	svc := NewServiceWithWorkspace(&httpRepoStub{}, &notifierStub{}, repo, nil, nil)
	return NewHandler(svc, accounts.NewService(newAccountsRepoStub())).WithRoleService(roleSvc)
}

func spendAlertsRequest(handler http.Handler, viewer auth.Viewer, method, path string, body io.Reader) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req = req.WithContext(viewerCtx(viewer))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// TestSpendAlertsHTTPCRUD walks the alert lifecycle over HTTP as the verified
// owner and pins the refusal contract on every branch.
func TestSpendAlertsHTTPCRUD(t *testing.T) {
	owner := auth.Viewer{UserID: uuid.New(), Email: "owner@example.com", EmailVerified: true}
	member := auth.Viewer{UserID: uuid.New(), Email: "member@example.com", EmailVerified: true}
	wsID := uuid.New()
	repo := newSpendAlertsRepo()
	handler := newSpendAlertsHandler(repo, owner.UserID, wsID)
	base := "/api/v1/spend-alerts/" + wsID.String()

	t.Run("create, list, update, delete round trip", func(t *testing.T) {
		email := "ops@example.com"
		body := `{"threshold_pct":50,"email":"` + email + `"}`
		rr := spendAlertsRequest(handler, owner, http.MethodPost, base, strings.NewReader(body))
		if rr.Code != http.StatusCreated {
			t.Fatalf("create got %d: %s", rr.Code, rr.Body.String())
		}
		var created struct {
			Alert alertWireFormat `json:"alert"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode create: %v", err)
		}
		if created.Alert.ID == uuid.Nil || created.Alert.ThresholdPct != 50 {
			t.Fatalf("unexpected created alert: %+v", created.Alert)
		}

		rr = spendAlertsRequest(handler, owner, http.MethodGet, base, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("list got %d: %s", rr.Code, rr.Body.String())
		}

		patchBody := `{"email":"new-ops@example.com"}`
		rr = spendAlertsRequest(handler, owner, http.MethodPatch, base+"/"+created.Alert.ID.String(), strings.NewReader(patchBody))
		if rr.Code != http.StatusOK {
			t.Fatalf("patch got %d: %s", rr.Code, rr.Body.String())
		}

		rr = spendAlertsRequest(handler, owner, http.MethodDelete, base+"/"+created.Alert.ID.String(), nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("delete got %d: %s", rr.Code, rr.Body.String())
		}

		// Deleting again must 404: the row is gone, not silently tolerated.
		rr = spendAlertsRequest(handler, owner, http.MethodDelete, base+"/"+created.Alert.ID.String(), nil)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("second delete got %d, want 404", rr.Code)
		}
	})

	t.Run("member mutation is forbidden", func(t *testing.T) {
		rr := spendAlertsRequest(handler, member, http.MethodPost, base, strings.NewReader(`{"threshold_pct":80}`))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("member create got %d, want 403 (owner-gated)", rr.Code)
		}
	})

	t.Run("invalid threshold is refused with 400", func(t *testing.T) {
		rr := spendAlertsRequest(handler, owner, http.MethodPost, base, strings.NewReader(`{"threshold_pct":75}`))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid pct got %d, want 400", rr.Code)
		}
	})
}

// TestLegacyBudgetSurfaceHTTP covers the legacy account-threshold HTTP
// endpoints: PUT upserts the threshold, POST dismiss clears it, invalid JSON
// answers 400, and a repository failure answers a fixed 500 whose body never
// carries the underlying error text (provider-blind response posture).
func TestLegacyBudgetSurfaceHTTP(t *testing.T) {
	userID := uuid.New()
	accountID := uuid.New()
	accountRepo := newAccountsRepoStub()
	accountRepo.accountsMap[accountID] = &accounts.Account{
		ID:          accountID,
		Slug:        "legacy-budget-ws",
		DisplayName: "Legacy Budget WS",
		AccountType: "personal",
		OwnerUserID: userID,
	}
	accountRepo.memberships = []accounts.Membership{
		{ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner", Status: "active"},
	}

	repo := &httpRepoStub{}
	svc := NewService(repo, &notifierStub{})
	handler := NewHandler(svc, accounts.NewService(accountRepo))
	viewer := auth.Viewer{UserID: userID, Email: "owner@example.com", EmailVerified: true}

	do := func(method, path, body string) *httptest.ResponseRecorder {
		if body == "" {
			return spendAlertsRequest(handler, viewer, method, path, nil)
		}
		return spendAlertsRequest(handler, viewer, method, path, strings.NewReader(body))
	}

	t.Run("upsert and dismiss round trip", func(t *testing.T) {
		rr := do(http.MethodPut, "/api/v1/accounts/current/budget", `{"threshold_credits":1000}`)
		if rr.Code != http.StatusOK {
			t.Fatalf("put got %d: %s", rr.Code, rr.Body.String())
		}
		rr = do(http.MethodPost, "/api/v1/accounts/current/budget/dismiss", "")
		if rr.Code != http.StatusOK {
			t.Fatalf("dismiss got %d: %s", rr.Code, rr.Body.String())
		}
	})

	t.Run("invalid JSON answers 400", func(t *testing.T) {
		rr := do(http.MethodPut, "/api/v1/accounts/current/budget", `{"threshold_credits":`)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("invalid JSON got %d, want 400", rr.Code)
		}
	})

	t.Run("repository failure answers opaque 500", func(t *testing.T) {
		failer := &httpRepoStub{upsertErr: errors.New("pgx: connection reset")}
		svcFailer := NewService(failer, &notifierStub{})
		handlerFailer := NewHandler(svcFailer, accounts.NewService(accountRepo))

		rr := spendAlertsRequest(handlerFailer, viewer, http.MethodPut, "/api/v1/accounts/current/budget", strings.NewReader(`{"threshold_credits":1000}`))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("repo failure got %d, want 500", rr.Code)
		}
		if strings.Contains(rr.Body.String(), "pgx") || strings.Contains(rr.Body.String(), "connection") {
			t.Fatal("raw repository error text leaked into the response body")
		}
	})
}

// TestWorkspaceBudgetHTTPLifecycle walks the workspace-budget surface over
// HTTP as its verified owner: PUT upserts caps, GET reads them back, DELETE
// removes the row, invalid caps answer 400 before any write, and a malformed
// workspace UUID is refused.
func TestWorkspaceBudgetHTTPLifecycle(t *testing.T) {
	owner := auth.Viewer{UserID: uuid.New(), Email: "owner@example.com", EmailVerified: true}
	wsID := uuid.New()
	repo := newSpendAlertsRepo()
	handler := newSpendAlertsHandler(repo, owner.UserID, wsID)

	t.Run("put get delete round trip", func(t *testing.T) {
		put := spendAlertsRequest(handler, owner, http.MethodPut,
			"/api/v1/budgets/"+wsID.String(),
			strings.NewReader(`{"soft_cap_bdt_subunits":1000,"hard_cap_bdt_subunits":2000}`))
		if put.Code != http.StatusOK {
			t.Fatalf("put got %d: %s", put.Code, put.Body.String())
		}

		get := spendAlertsRequest(handler, owner, http.MethodGet, "/api/v1/budgets/"+wsID.String(), nil)
		if get.Code != http.StatusOK {
			t.Fatalf("get got %d: %s", get.Code, get.Body.String())
		}
		var body struct {
			Budget struct {
				HardCapBDTSubunits int64 `json:"hard_cap_bdt_subunits"`
			} `json:"budget"`
		}
		if err := json.Unmarshal(get.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Budget.HardCapBDTSubunits != 2000 {
			t.Fatalf("read back hard cap %d, want 2000", body.Budget.HardCapBDTSubunits)
		}

		del := spendAlertsRequest(handler, owner, http.MethodDelete, "/api/v1/budgets/"+wsID.String(), nil)
		if del.Code != http.StatusOK {
			t.Fatalf("delete got %d: %s", del.Code, del.Body.String())
		}
	})

	t.Run("invalid caps answer 400 before any write", func(t *testing.T) {
		put := spendAlertsRequest(handler, owner, http.MethodPut,
			"/api/v1/budgets/"+wsID.String(),
			strings.NewReader(`{"soft_cap_bdt_subunits":5000,"hard_cap_bdt_subunits":1000}`))
		if put.Code != http.StatusBadRequest {
			t.Fatalf("invalid caps got %d, want 400", put.Code)
		}
	})

	t.Run("malformed workspace id answers 400", func(t *testing.T) {
		get := spendAlertsRequest(handler, owner, http.MethodGet, "/api/v1/budgets/not-a-uuid", nil)
		if get.Code != http.StatusBadRequest {
			t.Fatalf("malformed id got %d, want 400", get.Code)
		}
	})

	t.Run("get after delete renders null budget", func(t *testing.T) {
		put := spendAlertsRequest(handler, owner, http.MethodPut,
			"/api/v1/budgets/"+wsID.String(),
			strings.NewReader(`{"soft_cap_bdt_subunits":100,"hard_cap_bdt_subunits":300}`))
		if put.Code != http.StatusOK {
			t.Fatalf("put got %d: %s", put.Code, put.Body.String())
		}
		del := spendAlertsRequest(handler, owner, http.MethodDelete, "/api/v1/budgets/"+wsID.String(), nil)
		if del.Code != http.StatusOK {
			t.Fatalf("delete got %d", del.Code)
		}
		gone := spendAlertsRequest(handler, owner, http.MethodGet, "/api/v1/budgets/"+wsID.String(), nil)
		if gone.Code != http.StatusOK || !strings.Contains(gone.Body.String(), "null") {
			t.Fatalf("deleted-budget read got (%d, %s), want 200 with null budget", gone.Code, gone.Body.String())
		}
	})

	t.Run("invalid period_start answers 400", func(t *testing.T) {
		put := spendAlertsRequest(handler, owner, http.MethodPut,
			"/api/v1/budgets/"+wsID.String(),
			strings.NewReader(`{"soft_cap_bdt_subunits":100,"hard_cap_bdt_subunits":300,"period_start":"tomorrow"}`))
		if put.Code != http.StatusBadRequest {
			t.Fatalf("bad period_start got %d, want 400", put.Code)
		}
	})

	t.Run("spend alert edge refusals", func(t *testing.T) {
		alertsBase := "/api/v1/spend-alerts/" + wsID.String()
		email := "x@example.com"
		createBody := `{"threshold_pct":80,"email":"` + email + `"}`
		rr := spendAlertsRequest(handler, owner, http.MethodPost, alertsBase, strings.NewReader(createBody))
		if rr.Code != http.StatusCreated {
			t.Fatalf("seed create got %d", rr.Code)
		}
		var created struct {
			Alert alertWireFormat `json:"alert"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &created)

		member := auth.Viewer{UserID: uuid.New(), Email: "m@example.com", EmailVerified: true}

		cases := []struct {
			name   string
			viewer auth.Viewer
			method string
			path   string
			body   string
			want   int
		}{
			{"member list forbidden", member, http.MethodGet, alertsBase, "", http.StatusForbidden},
			{"patch unknown alert 404", owner, http.MethodPatch, alertsBase + "/" + uuid.New().String(), `{"email":"y@example.com"}`, http.StatusNotFound},
			{"patch bad json 400", owner, http.MethodPatch, alertsBase + "/" + created.Alert.ID.String(), `{`, http.StatusBadRequest},
			{"patch bad alert id 400", owner, http.MethodPatch, alertsBase + "/nope", `{"email":"y@example.com"}`, http.StatusBadRequest},
			{"delete unknown alert 404", owner, http.MethodDelete, alertsBase + "/" + uuid.New().String(), "", http.StatusNotFound},
			{"unsupported verb on collection 405", owner, http.MethodPut, alertsBase, `{"threshold_pct":50}`, http.StatusMethodNotAllowed},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var body io.Reader
				if tc.body != "" {
					body = strings.NewReader(tc.body)
				}
				rr := spendAlertsRequest(handler, tc.viewer, tc.method, tc.path, body)
				if rr.Code != tc.want {
					t.Fatalf("%s: got %d, want %d (body %s)", tc.name, rr.Code, tc.want, rr.Body.String())
				}
			})
		}

		// Clean up the seeded alert so the earlier round-trip assertions stay
		// independent of execution order.
		_ = spendAlertsRequest(handler, owner, http.MethodDelete, alertsBase+"/"+created.Alert.ID.String(), nil)
	})
}

// TestInternalHardCapEndpoint covers the edge-api gate fallback read: null
// when no budget is set, string rendering of big.Int when set, and strict
// method/path handling.
func TestInternalHardCapEndpoint(t *testing.T) {
	wsID := uuid.New()
	repo := newSpendAlertsRepo()
	repo.budgets[wsID] = &Budget{
		WorkspaceID: wsID,
		PeriodStart: time.Now().UTC(),
		SoftCap:     big.NewInt(1000),
		HardCap:     big.NewInt(2000),
		Currency:    "BDT",
	}
	handler := newSpendAlertsHandler(repo, uuid.New(), wsID)

	t.Run("set budget renders cap as string", func(t *testing.T) {
		rr := spendAlertsRequest(handler, auth.Viewer{}, http.MethodGet, "/internal/budgets/"+wsID.String()+"/hard-cap", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("got %d: %s", rr.Code, rr.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["hard_cap_bdt_subunits"] != "2000" {
			t.Fatalf("hard cap %v, want string \"2000\"", body["hard_cap_bdt_subunits"])
		}
	})

	t.Run("no budget renders null", func(t *testing.T) {
		other := uuid.New()
		rr := spendAlertsRequest(handler, auth.Viewer{}, http.MethodGet, "/internal/budgets/"+other.String()+"/hard-cap", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("got %d", rr.Code)
		}
		var body map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		v, ok := body["hard_cap_bdt_subunits"]
		if !ok || v != nil {
			t.Fatalf("expected JSON null hard cap for unbudgeted workspace, got %v", v)
		}
	})

	t.Run("wrong method and malformed path are refused", func(t *testing.T) {
		rr := spendAlertsRequest(handler, auth.Viewer{}, http.MethodPost, "/internal/budgets/"+wsID.String()+"/hard-cap", strings.NewReader("{}"))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST to read-only endpoint got %d, want 405", rr.Code)
		}
		rr = spendAlertsRequest(handler, auth.Viewer{}, http.MethodGet, "/internal/budgets/not-a-uuid/hard-cap", nil)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("malformed workspace id got %d, want 400", rr.Code)
		}
	})
}
