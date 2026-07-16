package marketplace

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

// Handler serves the admin-curated marketplace catalog surface (issue #309,
// agent-subsystem blueprint Step 2.3): CRUD over the shared catalog, per-
// tenant enable/disable, and the internal service-to-service read the
// agent-engine consumes to build its MCP config.
//
// Admin surface (owner-gated, mounted behind AuthMiddleware.Require +
// RoleSvc.RequirePlatformAdmin — see router.go, mirrors the featuregate and
// egress-policy admin surfaces):
//
//	GET    /api/v1/admin/marketplace              — catalog joined with this tenant's enablement
//	POST   /api/v1/admin/marketplace              — curate a new entry
//	PUT    /api/v1/admin/marketplace/{id}         — edit an entry
//	DELETE /api/v1/admin/marketplace/{id}         — remove an entry from the catalog
//	PUT    /api/v1/admin/marketplace/{id}/enable  — enable/disable an entry for this tenant
//
// Internal surface (shared-secret token, mounted behind RequireInternalToken):
//
//	GET /internal/marketplace/{tenant_id}/mcp-servers — enabled MCP server entries for tenant_id
//
// The internal surface deliberately returns the raw kind-specific Config
// blob per entry rather than an OpenHands-shaped mcpServers map: this
// package has no OpenHands dependency, and apps/agent-engine/internal/
// marketplaceclient is the one place that decodes Config into the SDK's
// native MCPServer fields.
type Handler struct {
	svc *Service
}

// NewHandler constructs the marketplace HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// AdminMux returns the owner-gated admin CRUD + enablement surface. Wrap
// with auth.Middleware.Require + platform-admin in router wiring.
func (h *Handler) AdminMux() http.Handler {
	return http.HandlerFunc(h.serveAdmin)
}

// InternalMux returns the service-to-service read surface. Wrap with
// RequireInternalToken in router wiring.
func (h *Handler) InternalMux() http.Handler {
	return http.HandlerFunc(h.serveInternal)
}

const adminPrefix = "/api/v1/admin/marketplace"
const internalPrefix = "/internal/marketplace/"

// maxBodyBytes caps request bodies this handler decodes. A curated catalog
// entry (an MCP server config, a rule, a skill, a prompt template) has no
// legitimate reason to approach this size; it exists so an oversized body
// is rejected before being decoded into memory at all.
const maxBodyBytes = 1 << 20 // 1 MiB

func (h *Handler) serveAdmin(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errBody("unauthorized"))
		return
	}
	if viewer.TenantID == uuid.Nil {
		writeJSON(w, http.StatusBadRequest, errBody("no tenant selected"))
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, adminPrefix), "/")
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			h.handleList(w, r, viewer.TenantID)
		case http.MethodPost:
			h.handleCreate(w, r, viewer.UserID)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
		}
		return
	}

	parts := strings.Split(rest, "/")
	id, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid entry id"))
		return
	}

	switch {
	case len(parts) == 1:
		switch r.Method {
		case http.MethodPut:
			h.handleUpdate(w, r, id)
		case http.MethodDelete:
			h.handleDelete(w, r, id)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
		}
	case len(parts) == 2 && parts[1] == "enable":
		if r.Method != http.MethodPut {
			writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
			return
		}
		h.handleSetEnabled(w, r, viewer.TenantID, id, viewer.UserID)
	default:
		writeJSON(w, http.StatusNotFound, errBody("not found"))
	}
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID) {
	entries, enabled, err := h.svc.Browse(r.Context(), tenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("failed to load marketplace catalog"))
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Entries: entryWires(entries, enabled)})
}

type createOrUpdateRequest struct {
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      json.RawMessage `json:"config"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request, createdBy uuid.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createOrUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	entry, err := h.svc.CreateEntry(r.Context(), Kind(req.Kind), req.Name, req.Description, req.Config, createdBy)
	if err != nil {
		writeEntryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, newEntryWire(entry, false))
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createOrUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	entry, err := h.svc.UpdateEntry(r.Context(), id, req.Name, req.Description, req.Config)
	if err != nil {
		writeEntryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newEntryWire(entry, false))
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	if err := h.svc.DeleteEntry(r.Context(), id); err != nil {
		writeEntryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) handleSetEnabled(w http.ResponseWriter, r *http.Request, tenantID, entryID, actorID uuid.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	if err := h.svc.SetEnabled(r.Context(), tenantID, entryID, req.Enabled, actorID); err != nil {
		writeEntryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": entryID.String(), "enabled": req.Enabled})
}

func (h *Handler) serveInternal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, internalPrefix), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "mcp-servers" {
		writeJSON(w, http.StatusNotFound, errBody("not found"))
		return
	}
	tenantID, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid tenant_id"))
		return
	}

	entries, err := h.svc.EnabledMCPServers(r.Context(), tenantID)
	if err != nil {
		// Provider-blind: the upstream error never reaches the response body.
		writeJSON(w, http.StatusInternalServerError, errBody("failed to resolve enabled MCP servers"))
		return
	}
	writeJSON(w, http.StatusOK, mcpServersResponse{TenantID: tenantID, Servers: mcpServerWires(entries)})
}

func writeEntryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody("marketplace entry not found"))
	case errors.Is(err, ErrDuplicate):
		writeJSON(w, http.StatusConflict, errBody(ErrDuplicate.Error()))
	case errors.Is(err, ErrInvalidKind), errors.Is(err, ErrInvalidName), errors.Is(err, ErrInvalidConfig):
		writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
	default:
		// Provider-blind: the underlying error (DB failure) is never echoed.
		writeJSON(w, http.StatusInternalServerError, errBody("marketplace request failed"))
	}
}

// =============================================================================
// Wire shapes
// =============================================================================

type entryWire struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      json.RawMessage `json:"config"`
	Enabled     bool            `json:"enabled"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type listResponse struct {
	Entries []entryWire `json:"entries"`
}

func newEntryWire(e Entry, enabled bool) entryWire {
	return entryWire{
		ID:          e.ID.String(),
		Kind:        string(e.Kind),
		Name:        e.Name,
		Description: e.Description,
		Config:      configOrEmptyObject(e.Config),
		Enabled:     enabled,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

func entryWires(entries []Entry, enabled map[uuid.UUID]TenantEntry) []entryWire {
	out := make([]entryWire, 0, len(entries))
	for _, e := range entries {
		_, on := enabled[e.ID]
		out = append(out, newEntryWire(e, on))
	}
	return out
}

// mcpServerEntryWire is one enabled MCP-server catalog entry as served over
// GET /internal/marketplace/{tenant_id}/mcp-servers.
type mcpServerEntryWire struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config"`
}

type mcpServersResponse struct {
	TenantID uuid.UUID            `json:"tenant_id"`
	Servers  []mcpServerEntryWire `json:"servers"`
}

func mcpServerWires(entries []Entry) []mcpServerEntryWire {
	out := make([]mcpServerEntryWire, 0, len(entries))
	for _, e := range entries {
		out = append(out, mcpServerEntryWire{Name: e.Name, Config: configOrEmptyObject(e.Config)})
	}
	return out
}

// configOrEmptyObject ensures the wire value is `{}` rather than `null` when
// empty — consumers (admin UI, marketplaceclient) should not need a nil check.
func configOrEmptyObject(config json.RawMessage) json.RawMessage {
	if len(config) == 0 {
		return json.RawMessage(`{}`)
	}
	return config
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}
