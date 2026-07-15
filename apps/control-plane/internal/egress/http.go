package egress

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// Handler serves the egress-policy admin CRUD surface and the internal
// service-to-service read endpoint.
//
// Admin surface (owner-gated, mounted behind auth middleware):
//
//	GET    /api/v1/egress-policy/{tenant_id}
//	PUT    /api/v1/egress-policy/{tenant_id}
//	GET    /api/v1/egress-policy/{tenant_id}/users/{user_id}
//	PUT    /api/v1/egress-policy/{tenant_id}/users/{user_id}
//	DELETE /api/v1/egress-policy/{tenant_id}/users/{user_id}
//
// Internal surface (shared-secret token, mounted behind RequireInternalToken):
//
//	GET /internal/egress-policy/{tenant_id}
//	GET /internal/egress-policy/{tenant_id}/{user_id}
//
// The internal surface is the single read consumed identically by the
// server-side OpenHands allowed_hosts workspace config and the desktop
// firewall rule generator (issue #308). Neither consumer is wired here.
type Handler struct {
	svc *Service
}

// NewHandler constructs the egress-policy HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// AdminMux returns the owner-gated admin CRUD surface. Wrap with
// auth.Middleware.Require in router wiring.
func (h *Handler) AdminMux() http.Handler {
	return http.HandlerFunc(h.serveAdmin)
}

// InternalMux returns the service-to-service read surface. Wrap with
// RequireInternalToken in router wiring.
func (h *Handler) InternalMux() http.Handler {
	return http.HandlerFunc(h.serveInternal)
}

const adminPrefix = "/api/v1/egress-policy/"
const internalPrefix = "/internal/egress-policy/"

func (h *Handler) serveAdmin(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errBody("unauthorized"))
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, adminPrefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, errBody("tenant_id required"))
		return
	}
	tenantID, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid tenant_id"))
		return
	}

	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.handleGetTenantDefault(w, r, viewer.UserID, tenantID)
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.handlePutTenantDefault(w, r, viewer.UserID, tenantID)
	case len(parts) == 3 && parts[1] == "users" && r.Method == http.MethodGet:
		h.handleGetUserOverride(w, r, viewer.UserID, tenantID, parts[2])
	case len(parts) == 3 && parts[1] == "users" && r.Method == http.MethodPut:
		h.handlePutUserOverride(w, r, viewer.UserID, tenantID, parts[2])
	case len(parts) == 3 && parts[1] == "users" && r.Method == http.MethodDelete:
		h.handleDeleteUserOverride(w, r, viewer.UserID, tenantID, parts[2])
	default:
		writeJSON(w, http.StatusNotFound, errBody("not found"))
	}
}

func (h *Handler) serveInternal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, internalPrefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, errBody("tenant_id required"))
		return
	}
	tenantID, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid tenant_id"))
		return
	}

	userID := uuid.Nil
	if len(parts) == 2 && parts[1] != "" {
		userID, err = uuid.Parse(parts[1])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid user_id"))
			return
		}
	} else if len(parts) > 2 {
		writeJSON(w, http.StatusNotFound, errBody("not found"))
		return
	}

	p, err := h.svc.Effective(r.Context(), tenantID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("egress policy resolution failed"))
		return
	}
	writeJSON(w, http.StatusOK, effectiveWire(p))
}

func (h *Handler) handleGetTenantDefault(w http.ResponseWriter, r *http.Request, callerID, tenantID uuid.UUID) {
	p, err := h.svc.GetTenantDefault(r.Context(), callerID, tenantID)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tenantDefaultWire(p))
}

func (h *Handler) handlePutTenantDefault(w http.ResponseWriter, r *http.Request, callerID, tenantID uuid.UUID) {
	hosts, ok := decodeHostsBody(w, r)
	if !ok {
		return
	}
	p, err := h.svc.SetTenantDefault(r.Context(), callerID, tenantID, hosts)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tenantDefaultWire(p))
}

func (h *Handler) handleGetUserOverride(w http.ResponseWriter, r *http.Request, callerID, tenantID uuid.UUID, userIDStr string) {
	targetUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid user_id"))
		return
	}
	p, err := h.svc.GetUserOverride(r.Context(), callerID, tenantID, targetUserID)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userOverrideWire(p))
}

func (h *Handler) handlePutUserOverride(w http.ResponseWriter, r *http.Request, callerID, tenantID uuid.UUID, userIDStr string) {
	targetUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid user_id"))
		return
	}
	hosts, ok := decodeHostsBody(w, r)
	if !ok {
		return
	}
	p, err := h.svc.SetUserOverride(r.Context(), callerID, tenantID, targetUserID, hosts)
	if err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, userOverrideWire(p))
}

func (h *Handler) handleDeleteUserOverride(w http.ResponseWriter, r *http.Request, callerID, tenantID uuid.UUID, userIDStr string) {
	targetUserID, err := uuid.Parse(userIDStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid user_id"))
		return
	}
	if err := h.svc.DeleteUserOverride(r.Context(), callerID, tenantID, targetUserID); err != nil {
		writePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func decodeHostsBody(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var body struct {
		AllowedHosts []string `json:"allowed_hosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return nil, false
	}
	return body.AllowedHosts, true
}

func writePolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		writeJSON(w, http.StatusForbidden, errBody("workspace owner permission required"))
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody("egress policy not found"))
	case errors.Is(err, ErrInvalidHosts):
		writeJSON(w, http.StatusBadRequest, errBody(ErrInvalidHosts.Error()))
	default:
		// Provider-blind: the underlying error (DB failure, owner-check
		// transport failure) is never echoed to the caller.
		writeJSON(w, http.StatusInternalServerError, errBody("egress policy request failed"))
	}
}

// =============================================================================
// Wire shapes
// =============================================================================

type tenantDefaultResponse struct {
	TenantID     uuid.UUID `json:"tenant_id"`
	AllowedHosts []string  `json:"allowed_hosts"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

type userOverrideResponse struct {
	TenantID     uuid.UUID `json:"tenant_id"`
	UserID       uuid.UUID `json:"user_id"`
	AllowedHosts []string  `json:"allowed_hosts"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

// effectiveResponse is the canonical shape served over
// GET /internal/egress-policy/{tenant_id}[/{user_id}] — the exact shape both
// the OpenHands allowed_hosts workspace config and the desktop firewall rule
// generator consume (issue #308 acceptance check).
type effectiveResponse struct {
	TenantID     uuid.UUID `json:"tenant_id"`
	UserID       uuid.UUID `json:"user_id"`
	AllowedHosts []string  `json:"allowed_hosts"`
}

func tenantDefaultWire(p Policy) tenantDefaultResponse {
	return tenantDefaultResponse{TenantID: p.TenantID, AllowedHosts: hostsOrEmpty(p.AllowedHosts), UpdatedAt: p.UpdatedAt}
}

func userOverrideWire(p Policy) userOverrideResponse {
	return userOverrideResponse{TenantID: p.TenantID, UserID: p.UserID, AllowedHosts: hostsOrEmpty(p.AllowedHosts), UpdatedAt: p.UpdatedAt}
}

func effectiveWire(p Policy) effectiveResponse {
	return effectiveResponse{TenantID: p.TenantID, UserID: p.UserID, AllowedHosts: hostsOrEmpty(p.AllowedHosts)}
}

// hostsOrEmpty ensures the wire array is `[]` rather than `null` when empty —
// consumers (OpenHands config loader, firewall rule generator) should not
// need a nil check.
func hostsOrEmpty(hosts []string) []string {
	if hosts == nil {
		return []string{}
	}
	return hosts
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}
