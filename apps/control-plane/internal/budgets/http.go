package budgets

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hivegpt/hive/apps/control-plane/internal/accounts"
	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
	"github.com/hivegpt/hive/apps/control-plane/internal/platform"
)

// Handler is the HTTP handler for budget threshold endpoints.
//
// Phase 14 extension: in addition to the legacy /api/v1/accounts/current/budget
// endpoints, this handler now serves the new owner-gated workspace surface:
//
//	GET    /api/v1/budgets/{workspace_id}
//	PUT    /api/v1/budgets/{workspace_id}
//	DELETE /api/v1/budgets/{workspace_id}
//	GET    /api/v1/spend-alerts/{workspace_id}
//	POST   /api/v1/spend-alerts/{workspace_id}
//	PATCH  /api/v1/spend-alerts/{workspace_id}/{alert_id}
//	DELETE /api/v1/spend-alerts/{workspace_id}/{alert_id}
//
// And the internal-only edge-api integration:
//
//	GET    /internal/budgets/{workspace_id}/hard-cap
//
// Owner gating uses platform.RoleService.IsWorkspaceOwner — Phase 18 RBAC will
// swap the body without changing the call site (contract stub from Task 2).
type Handler struct {
	svc         *Service
	accountsSvc *accounts.Service
	roleSvc     *platform.RoleService // optional — when nil, workspace routes 503
}

// NewHandler creates a new budget HTTP handler with the legacy threshold surface.
func NewHandler(svc *Service, accountsSvc *accounts.Service) *Handler {
	return &Handler{svc: svc, accountsSvc: accountsSvc}
}

// WithRoleService returns a copy of the handler wired with platform role
// service so workspace and internal routes are active.
func (h *Handler) WithRoleService(roleSvc *platform.RoleService) *Handler {
	cloned := *h
	cloned.roleSvc = roleSvc
	return &cloned
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	// ---------- Legacy account-budget surface ----------
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/budget":
		h.handleGetBudget(w, r)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/accounts/current/budget":
		h.handleUpsertBudget(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/current/budget/dismiss":
		h.handleDismissAlert(w, r)

	// ---------- Phase 14 workspace budget surface ----------
	case strings.HasPrefix(r.URL.Path, "/api/v1/budgets/"):
		h.handleWorkspaceBudget(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/spend-alerts/"):
		h.handleSpendAlerts(w, r)

	// ---------- Internal (service-mesh) integration ----------
	case strings.HasPrefix(r.URL.Path, "/internal/budgets/"):
		h.handleInternalHardCap(w, r)

	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// =============================================================================
// Legacy account-budget handlers (preserved verbatim)
// =============================================================================

func (h *Handler) handleGetBudget(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	threshold, err := h.svc.GetThreshold(r.Context(), accountID)
	if err != nil {
		writeBudgetError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"threshold": threshold})
}

func (h *Handler) handleUpsertBudget(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	var input UpsertThresholdInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	threshold, err := h.svc.UpsertThreshold(r.Context(), accountID, input)
	if err != nil {
		writeBudgetError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, threshold)
}

func (h *Handler) handleDismissAlert(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	if err := h.svc.DismissAlert(r.Context(), accountID); err != nil {
		writeBudgetError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "dismissed"})
}

func (h *Handler) resolveCurrentAccountID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return uuid.Nil, false
	}

	viewerContext, err := h.accountsSvc.EnsureViewerContext(r.Context(), viewer, parseAccountHeader(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return uuid.Nil, false
	}

	if !viewerContext.User.EmailVerified {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "email must be verified before accessing billing",
			"code":  "email_verification_required",
		})
		return uuid.Nil, false
	}

	return viewerContext.CurrentAccount.ID, true
}

func writeBudgetError(w http.ResponseWriter, err error) {
	var validationErr *ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationErr.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func parseAccountHeader(r *http.Request) uuid.UUID {
	val := r.Header.Get("X-Hive-Account-ID")
	if val == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil
	}
	return id
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// =============================================================================
// Phase 14 — Workspace budget handlers
// =============================================================================

// budgetWireFormat is the JSON shape returned to console clients. BDT subunits
// only — no USD / FX fields. *big.Int values render as int64 (BDT subunits fit;
// see types.go documentation).
type budgetWireFormat struct {
	WorkspaceID         uuid.UUID `json:"workspace_id"`
	PeriodStart         time.Time `json:"period_start"`
	SoftCapBDTSubunits  int64     `json:"soft_cap_bdt_subunits"`
	HardCapBDTSubunits  int64     `json:"hard_cap_bdt_subunits"`
	Currency            string    `json:"currency"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type alertWireFormat struct {
	ID              uuid.UUID  `json:"id"`
	WorkspaceID     uuid.UUID  `json:"workspace_id"`
	ThresholdPct    int        `json:"threshold_pct"`
	Email           *string    `json:"email,omitempty"`
	WebhookURL      *string    `json:"webhook_url,omitempty"`
	LastFiredAt     *time.Time `json:"last_fired_at,omitempty"`
	LastFiredPeriod *time.Time `json:"last_fired_period,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

type setBudgetRequest struct {
	SoftCapBDTSubunits int64  `json:"soft_cap_bdt_subunits"`
	HardCapBDTSubunits int64  `json:"hard_cap_bdt_subunits"`
	PeriodStart        string `json:"period_start"` // optional ISO date; defaults to month start
}

type createAlertRequest struct {
	ThresholdPct  int     `json:"threshold_pct"`
	Email         *string `json:"email,omitempty"`
	WebhookURL    *string `json:"webhook_url,omitempty"`
	WebhookSecret *string `json:"webhook_secret,omitempty"`
}

type updateAlertRequest struct {
	Email         *string `json:"email,omitempty"`
	WebhookURL    *string `json:"webhook_url,omitempty"`
	WebhookSecret *string `json:"webhook_secret,omitempty"`
}

func (h *Handler) handleWorkspaceBudget(w http.ResponseWriter, r *http.Request) {
	if h.svc.workspaceCtx == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace budget surface unavailable"})
		return
	}
	wsID, ok := parseWorkspacePathSuffix(r.URL.Path, "/api/v1/budgets/")
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid workspace_id path segment"})
		return
	}

	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		// Read-only: members can view their own workspace budget.
		// Cross-workspace is gated below.
		if err := h.requireWorkspaceMembership(r, viewer.UserID, wsID); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "workspace access denied"})
			return
		}
		b, err := h.svc.GetBudget(r.Context(), wsID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "budget read failed"})
			return
		}
		if b == nil {
			writeJSON(w, http.StatusOK, map[string]any{"budget": nil})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"budget": toBudgetWire(b)})

	case http.MethodPut:
		if !h.requireWorkspaceOwner(w, r, viewer.UserID, wsID) {
			return
		}
		var req setBudgetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		periodStart := startOfMonthUTC(time.Now().UTC())
		if req.PeriodStart != "" {
			t, err := time.Parse("2006-01-02", req.PeriodStart)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid period_start (want YYYY-MM-DD)"})
				return
			}
			periodStart = t.UTC()
		}
		b, err := h.svc.SetBudget(r.Context(), SetBudgetInput{
			WorkspaceID: wsID,
			PeriodStart: periodStart,
			SoftCap:     big.NewInt(req.SoftCapBDTSubunits),
			HardCap:     big.NewInt(req.HardCapBDTSubunits),
		})
		if err != nil {
			if errors.Is(err, ErrInvalidCaps) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "budget upsert failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"budget": toBudgetWire(b)})

	case http.MethodDelete:
		if !h.requireWorkspaceOwner(w, r, viewer.UserID, wsID) {
			return
		}
		if err := h.svc.DeleteBudget(r.Context(), wsID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "budget delete failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (h *Handler) handleSpendAlerts(w http.ResponseWriter, r *http.Request) {
	if h.svc.workspaceCtx == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "spend alert surface unavailable"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/spend-alerts/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing workspace_id"})
		return
	}
	wsID, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid workspace_id"})
		return
	}

	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	// /api/v1/spend-alerts/{ws}            — GET, POST
	// /api/v1/spend-alerts/{ws}/{alertID}  — PATCH, DELETE
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		if err := h.requireWorkspaceMembership(r, viewer.UserID, wsID); err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "workspace access denied"})
			return
		}
		alerts, err := h.svc.ListAlerts(r.Context(), wsID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list alerts failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"alerts": toAlertsWire(alerts)})

	case len(parts) == 1 && r.Method == http.MethodPost:
		if !h.requireWorkspaceOwner(w, r, viewer.UserID, wsID) {
			return
		}
		var req createAlertRequest
		if jsonErr := json.NewDecoder(r.Body).Decode(&req); jsonErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		a, err := h.svc.CreateAlert(r.Context(), CreateAlertInput{
			WorkspaceID:   wsID,
			ThresholdPct:  req.ThresholdPct,
			Email:         req.Email,
			WebhookURL:    req.WebhookURL,
			WebhookSecret: req.WebhookSecret,
		})
		if err != nil {
			if errors.Is(err, ErrInvalidThreshold) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create alert failed"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"alert": toAlertWire(*a)})

	case len(parts) == 2 && r.Method == http.MethodPatch:
		if !h.requireWorkspaceOwner(w, r, viewer.UserID, wsID) {
			return
		}
		alertID, parseErr := uuid.Parse(parts[1])
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid alert_id"})
			return
		}
		var req updateAlertRequest
		if jsonErr := json.NewDecoder(r.Body).Decode(&req); jsonErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		a, err := h.svc.UpdateAlert(r.Context(), UpdateAlertInput{
			ID:            alertID,
			Email:         req.Email,
			WebhookURL:    req.WebhookURL,
			WebhookSecret: req.WebhookSecret,
		})
		if err != nil {
			if errors.Is(err, ErrBudgetNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "alert not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update alert failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"alert": toAlertWire(*a)})

	case len(parts) == 2 && r.Method == http.MethodDelete:
		if !h.requireWorkspaceOwner(w, r, viewer.UserID, wsID) {
			return
		}
		alertID, parseErr := uuid.Parse(parts[1])
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid alert_id"})
			return
		}
		if err := h.svc.DeleteAlert(r.Context(), alertID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete alert failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// handleInternalHardCap serves the edge-api budget gate's read-through fallback.
// Path: /internal/budgets/{workspace_id}/hard-cap
//
// Service-mesh authentication is handled by the platform router (this endpoint
// MUST NOT be customer-facing — see router wiring in platform/http/router.go).
func (h *Handler) handleInternalHardCap(w http.ResponseWriter, r *http.Request) {
	if h.svc.workspaceCtx == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace budget surface unavailable"})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/internal/budgets/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "hard-cap" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	wsID, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid workspace_id"})
		return
	}

	cap, err := h.svc.HardCapForWorkspace(r.Context(), wsID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal hard-cap read failed"})
		return
	}
	if cap == nil {
		// No budget set → gate is pass-through; return null.
		writeJSON(w, http.StatusOK, map[string]any{"hard_cap_bdt_subunits": nil})
		return
	}
	// Always render as string — *big.Int may exceed JS Number safe range.
	writeJSON(w, http.StatusOK, map[string]any{"hard_cap_bdt_subunits": cap.String()})
}

// =============================================================================
// Owner / membership gating
// =============================================================================

func (h *Handler) requireWorkspaceOwner(w http.ResponseWriter, r *http.Request, userID, workspaceID uuid.UUID) bool {
	if h.roleSvc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "role service unavailable"})
		return false
	}
	isOwner, err := h.roleSvc.IsWorkspaceOwner(r.Context(), userID, workspaceID)
	if err != nil {
		// IsWorkspaceOwner — provider-blind sanitized response.
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "permission check failed"})
		return false
	}
	if !isOwner {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "owner permission required"})
		return false
	}
	return true
}

// requireWorkspaceMembership is intentionally simple: until Phase 18 swaps in
// the tier-aware matrix, "membership" is "owner" (read also requires owner).
// A non-owner would already be rejected by the same call.
func (h *Handler) requireWorkspaceMembership(r *http.Request, userID, workspaceID uuid.UUID) error {
	if h.roleSvc == nil {
		return errors.New("role service unavailable")
	}
	isOwner, err := h.roleSvc.IsWorkspaceOwner(r.Context(), userID, workspaceID)
	if err != nil {
		return err
	}
	if !isOwner {
		return errors.New("not a member")
	}
	return nil
}

// parseWorkspacePathSuffix extracts the workspace UUID from the URL after the
// given prefix. Returns false when the suffix is not a valid UUID or there is
// extra path content.
func parseWorkspacePathSuffix(path, prefix string) (uuid.UUID, bool) {
	rest := strings.TrimPrefix(path, prefix)
	if strings.Contains(rest, "/") {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rest)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func toBudgetWire(b *Budget) budgetWireFormat {
	return budgetWireFormat{
		WorkspaceID:         b.WorkspaceID,
		PeriodStart:         b.PeriodStart,
		SoftCapBDTSubunits:  b.SoftCap.Int64(),
		HardCapBDTSubunits:  b.HardCap.Int64(),
		Currency:            b.Currency,
		CreatedAt:           b.CreatedAt,
		UpdatedAt:           b.UpdatedAt,
	}
}

func toAlertWire(a SpendAlert) alertWireFormat {
	return alertWireFormat{
		ID:              a.ID,
		WorkspaceID:     a.WorkspaceID,
		ThresholdPct:    a.ThresholdPct,
		Email:           a.Email,
		WebhookURL:      a.WebhookURL,
		LastFiredAt:     a.LastFiredAt,
		LastFiredPeriod: a.LastFiredPeriod,
		CreatedAt:       a.CreatedAt,
	}
}

func toAlertsWire(alerts []SpendAlert) []alertWireFormat {
	out := make([]alertWireFormat, 0, len(alerts))
	for _, a := range alerts {
		out = append(out, toAlertWire(a))
	}
	return out
}
