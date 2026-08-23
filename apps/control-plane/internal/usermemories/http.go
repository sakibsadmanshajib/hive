package usermemories

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Handler serves the service-to-service memory surface (issue #172, ruling
// D-020), mirroring apps/control-plane/internal/agenttask/http.go's
// InternalMux shape: tenant_id and user_id travel as URL path segments, not
// query string, header, or JSON body field, and are never echoed back.
//
//	POST   /internal/user-memories/{tenant_id}/{user_id}        creates {content, source_chat_id?}
//	GET    /internal/user-memories/{tenant_id}/{user_id}         lists (newest first)
//	PATCH  /internal/user-memories/{tenant_id}/{user_id}/{id}     updates content
//	DELETE /internal/user-memories/{tenant_id}/{user_id}/{id}     deletes one
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// InternalMux returns the shared-secret-guarded service-to-service surface.
// Wrap with RequireInternalToken in router wiring.
func (h *Handler) InternalMux() http.Handler {
	return http.HandlerFunc(h.serveInternal)
}

const internalPrefix = "/internal/user-memories/"

// maxBodyBytes caps request bodies this handler decodes. Content itself is
// capped far lower at the service boundary; the headroom covers JSON
// framing only.
const maxBodyBytes = 8 << 10 // 8 KiB

func (h *Handler) serveInternal(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, internalPrefix), "/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		writeJSON(w, http.StatusNotFound, errBody("not found"))
		return
	}
	tenantID, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid tenant_id"))
		return
	}
	userID, err := uuid.Parse(parts[1])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid user_id"))
		return
	}

	switch len(parts) {
	case 2:
		switch r.Method {
		case http.MethodPost:
			h.handleCreate(w, r, tenantID, userID)
		case http.MethodGet:
			h.handleList(w, r, tenantID, userID)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
		}
	case 3:
		id, err := uuid.Parse(parts[2])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid memory id"))
			return
		}
		switch r.Method {
		case http.MethodPatch:
			h.handleUpdate(w, r, tenantID, userID, id)
		case http.MethodDelete:
			h.handleDelete(w, r, tenantID, userID, id)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
		}
	default:
		writeJSON(w, http.StatusNotFound, errBody("not found"))
	}
}

type createRequest struct {
	Content      string  `json:"content"`
	SourceChatID *string `json:"source_chat_id"`
}

type updateRequest struct {
	Content string `json:"content"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request, tenantID, userID uuid.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	memory, err := h.svc.Create(r.Context(), tenantID, userID, req.Content, req.SourceChatID)
	if err != nil {
		writeMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, newMemoryWire(memory))
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request, tenantID, userID uuid.UUID) {
	memories, err := h.svc.List(r.Context(), tenantID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("failed to list memories"))
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Memories: memoryWires(memories)})
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request, tenantID, userID, id uuid.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	memory, err := h.svc.Update(r.Context(), tenantID, userID, id, req.Content)
	if err != nil {
		writeMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newMemoryWire(memory))
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, tenantID, userID, id uuid.UUID) {
	err := h.svc.Delete(r.Context(), tenantID, userID, id)
	if err != nil {
		writeMemoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id.String()})
}

func writeMemoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody("memory not found"))
	case errors.Is(err, ErrEmptyContent):
		writeJSON(w, http.StatusBadRequest, errBody(ErrEmptyContent.Error()))
	default:
		// Provider-blind: the underlying error (DB failure) is never echoed.
		writeJSON(w, http.StatusInternalServerError, errBody("memory request failed"))
	}
}

// =============================================================================
// Wire shapes: deliberately carry no tenant_id/user_id, both are implied by
// the URL path this handler was called with, never echoed back.
// =============================================================================

type memoryWire struct {
	ID           string    `json:"id"`
	Content      string    `json:"content"`
	SourceChatID *string   `json:"source_chat_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type listResponse struct {
	Memories []memoryWire `json:"memories"`
}

func newMemoryWire(m Memory) memoryWire {
	return memoryWire{
		ID:           m.ID.String(),
		Content:      m.Content,
		SourceChatID: m.SourceChatID,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

func memoryWires(memories []Memory) []memoryWire {
	out := make([]memoryWire, 0, len(memories))
	for _, m := range memories {
		out = append(out, newMemoryWire(m))
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}
