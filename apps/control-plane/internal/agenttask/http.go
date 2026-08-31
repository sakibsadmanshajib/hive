package agenttask

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
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
		taskID, err := uuid.Parse(parts[2])
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid task id"))
			return
		}
		switch {
		case parts[3] == "cancel" && r.Method == http.MethodPost:
			h.handleCancel(w, r, tenantID, userID, taskID)
		case parts[3] == "events" && r.Method == http.MethodGet:
			h.handleEvents(w, r, tenantID, userID, taskID)
		case parts[3] == "files" && r.Method == http.MethodGet:
			h.handleFiles(w, r, tenantID, userID, taskID)
		case parts[3] == "cancel" || parts[3] == "events" || parts[3] == "files":
			writeJSON(w, http.StatusMethodNotAllowed, errBody("method not allowed"))
		default:
			writeJSON(w, http.StatusNotFound, errBody("not found"))
		}
	default:
		writeJSON(w, http.StatusNotFound, errBody("not found"))
	}
}

type createRequest struct {
	Pack         string `json:"pack"`
	Instructions string `json:"instructions"`
	// ProjectID is the project the run consults, already authorized by edge-api
	// (apps/edge-api/internal/agenttask.Handler.handleCreate) against the
	// submitting user's ownership. This is a service-to-service surface guarded
	// by RequireInternalToken, so accepting it here widens no trust boundary,
	// but it also means this handler is NOT the place the check lives: it is
	// upstream, before a hold is taken and before this call is made.
	ProjectID string `json:"project_id"`
	// BearerJWT is forwarded by edge-api's task-create handler from the
	// original request's own Authorization header. This is a
	// service-to-service surface guarded by RequireInternalToken, not a
	// customer-reachable one, so carrying it in the body here widens no
	// trust boundary: edge-api is the only caller and it is relaying the
	// same user's own already-validated JWT, never minting one. See
	// Task.BearerJWT's doc comment.
	BearerJWT string `json:"bearer_jwt"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request, tenantID, userID uuid.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	// An unparseable project_id is a 400 rather than a silent Nil. Silently
	// dropping it would launch a run that consults nothing while the caller
	// believes a project is attached, which is worse than refusing.
	projectID := uuid.Nil
	if raw := strings.TrimSpace(req.ProjectID); raw != "" {
		parsed, perr := uuid.Parse(raw)
		if perr != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid project_id"))
			return
		}
		projectID = parsed
	}
	task, err := h.svc.CreateTask(r.Context(), tenantID, userID, Pack(req.Pack), req.Instructions, projectID, req.BearerJWT)
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
	task, callerErr := h.svc.Cancel(r.Context(), tenantID, userID, taskID)
	if callerErr != nil {
		writeTaskError(w, callerErr)
		return
	}
	writeJSON(w, http.StatusOK, newTaskWire(task))
}

// defaultEventsLimit / maxEventsLimit are the cursor read's bounds. The same
// numbers apply on the customer surface (edge-api caps at 500 too); the cap
// is a clamp rather than a 400 so a client asking for "everything" gets the
// newest window instead of an error to special-case.
const (
	defaultEventsLimit = 100
	maxEventsLimit     = 500
)

// handleEvents serves GET .../{task_id}/events?after_seq=N&limit=M.
// A non-numeric or negative after_seq is a 400 (ErrCursor), never silently
// zero — acceptance item 3.
func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request, tenantID, userID, taskID uuid.UUID) {
	q := r.URL.Query()
	var afterSeq int64
	var err error
	if raw := q.Get("after_seq"); raw != "" {
		if afterSeq, err = strconv.ParseInt(raw, 10, 64); err != nil || afterSeq < 0 {
			writeJSON(w, http.StatusBadRequest, errBody(ErrCursor.Error()))
			return
		}
	}
	limit := defaultEventsLimit
	if raw := q.Get("limit"); raw != "" {
		var n int
		if n, err = strconv.Atoi(raw); err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, errBody("invalid limit"))
			return
		}
		limit = n
	}
	if limit > maxEventsLimit {
		limit = maxEventsLimit
	}
	events, err := h.svc.Events(r.Context(), tenantID, userID, taskID, afterSeq, limit)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	if events == nil {
		events = []TaskEvent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// handleFiles serves GET .../{task_id}/files: the running session's workspace
// listing, best-effort.
func (h *Handler) handleFiles(w http.ResponseWriter, r *http.Request, tenantID, userID, taskID uuid.UUID) {
	files, err := h.svc.Files(r.Context(), tenantID, userID, taskID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	if files == nil {
		files = []WorkspaceFile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
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
