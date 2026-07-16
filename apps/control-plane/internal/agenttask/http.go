package agenttask

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Handler serves the service-to-service task lifecycle surface edge-api
// calls (issue #311, agent-subsystem blueprint Step 3.4). tenant_id and
// user_id travel as URL path segments — not query string, form value,
// header, or JSON body field — because the process boundary means the
// caller's own authenticated request context cannot cross it; edge-api
// (the only caller) resolves both solely from auth.TenantID(ctx) /
// auth.UserFrom(ctx) before building the path. Mirrors
// apps/control-plane/internal/marketplace/http.go's InternalMux, which
// resolves tenant_id the same way for GET /internal/marketplace/{tenant_id}/mcp-servers.
//
//	POST /internal/agent-tasks/{tenant_id}/{user_id}                 — create {pack}
//	GET  /internal/agent-tasks/{tenant_id}/{user_id}                 — list
//	GET  /internal/agent-tasks/{tenant_id}/{user_id}/{task_id}        — get one
//	POST /internal/agent-tasks/{tenant_id}/{user_id}/{task_id}/cancel — cancel
type Handler struct {
	svc *Service
}

// NewHandler constructs the agenttask HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// InternalMux returns the shared-secret-guarded service-to-service surface.
// Wrap with RequireInternalToken in router wiring.
func (h *Handler) InternalMux() http.Handler {
	return http.HandlerFunc(h.serveInternal)
}

const internalPrefix = "/internal/agent-tasks/"

// maxBodyBytes caps request bodies this handler decodes. A create request
// carries pack plus a free-text instructions/prompt field, which needs more
// headroom than a bare pack name; other bodies on this handler (cancel) send
// none at all.
const maxBodyBytes = 64 << 10 // 64 KiB

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
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
			return
		}
		taskID, err := uuid.Parse(parts[2])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid task id"))
			return
		}
		h.handleGet(w, r, tenantID, userID, taskID)
	case 4:
		if r.Method != http.MethodPost || parts[3] != "cancel" {
			writeJSON(w, http.StatusNotFound, errBody("not found"))
			return
		}
		taskID, err := uuid.Parse(parts[2])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid task id"))
			return
		}
		h.handleCancel(w, r, tenantID, userID, taskID)
	default:
		writeJSON(w, http.StatusNotFound, errBody("not found"))
	}
}

type createRequest struct {
	Pack         string `json:"pack"`
	Instructions string `json:"instructions"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request, tenantID, userID uuid.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	task, err := h.svc.CreateTask(r.Context(), tenantID, userID, Pack(req.Pack), req.Instructions)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, newTaskWire(task))
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request, tenantID, userID uuid.UUID) {
	tasks, err := h.svc.List(r.Context(), tenantID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("failed to list tasks"))
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Tasks: taskWires(tasks)})
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, tenantID, userID, taskID uuid.UUID) {
	task, err := h.svc.Get(r.Context(), tenantID, userID, taskID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newTaskWire(task))
}

func (h *Handler) handleCancel(w http.ResponseWriter, r *http.Request, tenantID, userID, taskID uuid.UUID) {
	task, err := h.svc.Cancel(r.Context(), tenantID, userID, taskID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newTaskWire(task))
}

func writeTaskError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody("task not found"))
	case errors.Is(err, ErrInvalidPack):
		writeJSON(w, http.StatusBadRequest, errBody(ErrInvalidPack.Error()))
	case errors.Is(err, ErrTerminalState):
		writeJSON(w, http.StatusConflict, errBody(ErrTerminalState.Error()))
	default:
		// Provider-blind: the underlying error (DB failure, engine detail) is
		// never echoed.
		writeJSON(w, http.StatusInternalServerError, errBody("agent task request failed"))
	}
}

// =============================================================================
// Wire shapes — deliberately carry no tenant_id/user_id: both are implied by
// the URL path this handler was called with, never echoed back.
// =============================================================================

type taskWire struct {
	ID               string     `json:"id"`
	Pack             string     `json:"pack"`
	Instructions     string     `json:"instructions"`
	Status           string     `json:"status"`
	EngineSessionRef string     `json:"engine_session_ref"`
	ResultSummaryRef string     `json:"result_summary_ref"`
	ErrorMessage     string     `json:"error_message"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
}

type listResponse struct {
	Tasks []taskWire `json:"tasks"`
}

func newTaskWire(t Task) taskWire {
	return taskWire{
		ID:               t.ID.String(),
		Pack:             string(t.Pack),
		Instructions:     t.Instructions,
		Status:           string(t.Status),
		EngineSessionRef: t.EngineSessionRef,
		ResultSummaryRef: t.ResultSummaryRef,
		ErrorMessage:     t.ErrorMessage,
		CreatedAt:        t.CreatedAt,
		UpdatedAt:        t.UpdatedAt,
		StartedAt:        t.StartedAt,
		FinishedAt:       t.FinishedAt,
	}
}

func taskWires(tasks []Task) []taskWire {
	out := make([]taskWire, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, newTaskWire(t))
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
