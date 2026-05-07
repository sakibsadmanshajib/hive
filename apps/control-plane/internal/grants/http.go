package grants

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

// =============================================================================
// Phase 14 — Grant HTTP handler.
//
// Routes:
//
//	POST /v1/admin/credit-grants            owner-only (RequirePlatformAdmin)
//	GET  /v1/admin/credit-grants            owner-only
//	GET  /v1/admin/credit-grants/{id}       owner-only
//	GET  /v1/credit-grants/me               any authed user (read-only)
//
// Wire format: BDT subunits as decimal strings (math/big invariant).
// Zero `amount_usd|usd_|fx_|exchange_rate|price_per_credit_usd` keys
// (regulatory; lint primitive verifies in Task 7).
//
// The Handler does not own owner-gate middleware itself — main.go wraps the
// /v1/admin/credit-grants prefix with platform.RequirePlatformAdmin so this
// handler can assume the viewer is a platform admin on every admin path.
// The single non-admin path (GET /v1/credit-grants/me) is wired separately
// behind plain auth middleware.
// =============================================================================

// Handler routes the four grant endpoints.
type Handler struct {
	svc *Service
}

// NewHandler constructs the grant HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// AdminMux returns the http.Handler for the owner-gated /v1/admin/credit-grants
// surface. Wrap with platform.RequirePlatformAdmin in main.go.
func (h *Handler) AdminMux() http.Handler {
	return http.HandlerFunc(h.serveAdmin)
}

// SelfMux returns the http.Handler for the self-list surface
// (GET /v1/credit-grants/me). Wrap with auth.Middleware only.
func (h *Handler) SelfMux() http.Handler {
	return http.HandlerFunc(h.serveSelf)
}

func (h *Handler) serveAdmin(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/admin/credit-grants":
		h.handleCreate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/admin/credit-grants":
		h.handleList(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/admin/credit-grants/"):
		idStr := strings.TrimPrefix(r.URL.Path, "/v1/admin/credit-grants/")
		h.handleGet(w, r, idStr)
	default:
		writeJSON(w, http.StatusNotFound, errBody("not found"))
	}
}

func (h *Handler) serveSelf(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/v1/credit-grants/me" {
		writeJSON(w, http.StatusNotFound, errBody("not found"))
		return
	}
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errBody("authentication required"))
		return
	}
	limit := parseLimit(r)
	cursor := parseCursor(r)
	items, err := h.svc.ListForGrantee(r.Context(), viewer.UserID, cursor, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("list failed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": toWireSlice(items)})
}

// =============================================================================
// Wire shape
// =============================================================================

type grantWire struct {
	ID                   uuid.UUID `json:"id"`
	GrantedByUserID      uuid.UUID `json:"granted_by_user_id"`
	GrantedToUserID      uuid.UUID `json:"granted_to_user_id"`
	GrantedToWorkspaceID uuid.UUID `json:"granted_to_workspace_id"`
	AmountBDTSubunits    string    `json:"amount_bdt_subunits"`
	ReasonNote           string    `json:"reason_note,omitempty"`
	LedgerEntryID        uuid.UUID `json:"ledger_entry_id"`
	Currency             string    `json:"currency"`
	CreatedAt            time.Time `json:"created_at"`
}

type createRequest struct {
	GrantedToUserID      string `json:"granted_to_user_id"`
	GrantedToWorkspaceID string `json:"granted_to_workspace_id"`
	AmountBDTSubunits    string `json:"amount_bdt_subunits"`
	ReasonNote           string `json:"reason_note,omitempty"`
	IdempotencyKey       string `json:"idempotency_key,omitempty"`
}

func toWire(g CreditGrant) grantWire {
	return grantWire{
		ID:                   g.ID,
		GrantedByUserID:      g.GrantedByUserID,
		GrantedToUserID:      g.GrantedToUserID,
		GrantedToWorkspaceID: g.GrantedToWorkspaceID,
		AmountBDTSubunits:    AmountString(g.AmountBDTSubunits),
		ReasonNote:           g.ReasonNote,
		LedgerEntryID:        g.LedgerEntryID,
		Currency:             g.Currency,
		CreatedAt:            g.CreatedAt,
	}
}

func toWireSlice(items []CreditGrant) []grantWire {
	out := make([]grantWire, 0, len(items))
	for _, g := range items {
		out = append(out, toWire(g))
	}
	return out
}

// =============================================================================
// Handlers
// =============================================================================

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errBody("authentication required"))
		return
	}

	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid json body"))
		return
	}

	granteeUser, err := uuid.Parse(req.GrantedToUserID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid granted_to_user_id"))
		return
	}
	granteeWS, err := uuid.Parse(req.GrantedToWorkspaceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid granted_to_workspace_id"))
		return
	}

	amount, ok := new(big.Int).SetString(strings.TrimSpace(req.AmountBDTSubunits), 10)
	if !ok || amount == nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid amount_bdt_subunits"))
		return
	}
	if amount.Sign() <= 0 {
		writeJSON(w, http.StatusBadRequest, errBody("amount_bdt_subunits must be positive"))
		return
	}

	result, err := h.svc.Create(r.Context(), CreateInput{
		GrantedByUserID:      viewer.UserID,
		GrantedToUserID:      granteeUser,
		GrantedToWorkspaceID: granteeWS,
		AmountBDTSubunits:    amount,
		ReasonNote:           req.ReasonNote,
		IdempotencyKey:       req.IdempotencyKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrForbidden):
			writeJSON(w, http.StatusForbidden, errBody("insufficient permissions"))
		case errors.Is(err, ErrInvalidAmount):
			writeJSON(w, http.StatusBadRequest, errBody("amount_bdt_subunits must be positive"))
		case errors.Is(err, ErrInvalidGrantee):
			writeJSON(w, http.StatusBadRequest, errBody("grantee user_id and workspace_id required"))
		default:
			writeJSON(w, http.StatusInternalServerError, errBody("create grant failed"))
		}
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"grant": toWire(result.Grant)})
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r)
	cursor := parseCursor(r)
	items, err := h.svc.ListAll(r.Context(), cursor, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("list failed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": toWireSlice(items)})
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, idStr string) {
	id, err := uuid.Parse(strings.TrimSuffix(idStr, "/"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid grant id"))
		return
	}
	g, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSON(w, http.StatusNotFound, errBody("grant not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errBody("get failed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"grant": toWire(g)})
}

// =============================================================================
// Helpers
// =============================================================================

func parseLimit(r *http.Request) int {
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 50
}

func parseCursor(r *http.Request) *uuid.UUID {
	v := r.URL.Query().Get("cursor")
	if v == "" {
		return nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil
	}
	return &id
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}
