package agenttask

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// TaskClient is the minimal interface the handler needs from Client.
// Exported so tests can inject a fake without a real control-plane.
type TaskClient interface {
	Create(ctx context.Context, tenantID, userID uuid.UUID, pack, instructions string) (Task, error)
	List(ctx context.Context, tenantID, userID uuid.UUID) ([]Task, error)
	Get(ctx context.Context, tenantID, userID, taskID uuid.UUID) (Task, error)
	Cancel(ctx context.Context, tenantID, userID, taskID uuid.UUID) (Task, error)
}

// Handler serves /v1/agent/tasks routes. Callers wrap with
// featuregate.Require(FeatureCowork) before mounting, mirroring
// apps/edge-api/internal/rag.Handler's gating contract.
type Handler struct {
	client TaskClient
}

// NewHandler constructs a Handler.
func NewHandler(client TaskClient) *Handler {
	return &Handler{client: client}
}

// Register mounts all /v1/agent/tasks* routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/agent/tasks", h.routeTasks)
	mux.HandleFunc("/v1/agent/tasks/", h.routeTaskByID)
}

func (h *Handler) routeTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleCreate(w, r)
	case http.MethodGet:
		h.handleList(w, r)
	default:
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
	}
}

func (h *Handler) routeTaskByID(w http.ResponseWriter, r *http.Request) {
	taskID, cancel, err := extractTaskPath(r.URL.Path)
	if err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid task id")
		return
	}
	switch {
	case cancel:
		if r.Method != http.MethodPost {
			apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
			return
		}
		h.handleCancel(w, r, taskID)
	default:
		if r.Method != http.MethodGet {
			apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
			return
		}
		h.handleGet(w, r, taskID)
	}
}

type createTaskRequest struct {
	Pack         string `json:"pack"`
	Instructions string `json:"instructions"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	var req createTaskRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Pack) == "" {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "pack required")
		return
	}

	task, err := h.client.Create(r.Context(), user.TenantID, user.ID, req.Pack, req.Instructions)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	tasks, err := h.client.List(r.Context(), user.TenantID, user.ID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, taskID uuid.UUID) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	task, err := h.client.Get(r.Context(), user.TenantID, user.ID, taskID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (h *Handler) handleCancel(w http.ResponseWriter, r *http.Request, taskID uuid.UUID) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	task, err := h.client.Cancel(r.Context(), user.TenantID, user.ID, taskID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func writeTaskError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "task not found")
	case errors.Is(err, ErrInvalidPack):
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid pack")
	case errors.Is(err, ErrTerminalState):
		apierrors.Write(w, http.StatusConflict, apierrors.CodeInvalidRequest, "task already reached a terminal state")
	default:
		// Provider-blind: the underlying error (control-plane infra detail) is never echoed.
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "agent task request failed")
	}
}

// extractTaskPath parses "/v1/agent/tasks/{id}" or
// "/v1/agent/tasks/{id}/cancel" into (id, isCancel).
func extractTaskPath(path string) (uuid.UUID, bool, error) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/agent/tasks/"), "/")
	parts := strings.Split(rest, "/")
	switch len(parts) {
	case 1:
		id, err := uuid.Parse(parts[0])
		return id, false, err
	case 2:
		if parts[1] != "cancel" {
			return uuid.Nil, false, errors.New("unknown suffix")
		}
		id, err := uuid.Parse(parts[0])
		return id, true, err
	default:
		return uuid.Nil, false, errors.New("unexpected path shape")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
