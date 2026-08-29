package agenttask

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling"
)

// TaskClient is the minimal interface the handler needs from Client.
// Exported so tests can inject a fake without a real control-plane.
type TaskClient interface {
	Create(ctx context.Context, tenantID, userID uuid.UUID, pack, instructions, bearerJWT string) (Task, error)
	List(ctx context.Context, tenantID, userID uuid.UUID) ([]Task, error)
	Get(ctx context.Context, tenantID, userID, taskID uuid.UUID) (Task, error)
	Cancel(ctx context.Context, tenantID, userID, taskID uuid.UUID) (Task, error)
	Events(ctx context.Context, tenantID, userID, taskID uuid.UUID, afterSeq int64, limit int) ([]Event, error)
	Files(ctx context.Context, tenantID, userID, taskID uuid.UUID) ([]WorkspaceFile, error)
}

// Handler serves /v1/agent/tasks routes. Callers wrap with
// featuregate.Require(FeatureCowork) before mounting, mirroring
// apps/edge-api/internal/rag.Handler's gating contract.
type Handler struct {
	client     TaskClient
	accounting *inference.AccountingClient
	billing    sessionbilling.Resolver
}

// NewHandler constructs a Handler.
func NewHandler(client TaskClient) *Handler {
	return &Handler{client: client}
}

// agentTaskLaunchFloor is the credit balance a tenant must be able to cover
// before a sandbox is launched for it: the same figure one chat turn holds,
// because a task is at minimum one model turn and in practice many. It is an
// authorization floor, never a charge, and never an estimate of what the task
// will go on to spend.
const agentTaskLaunchFloor = inference.DefaultHoldText

// agentTaskLabel is what the probe records where an inference request records
// its model alias. Operator-facing, never customer-visible.
const agentTaskLabel = "agent-task"

// WithBilling wires the solvency gate onto an existing Handler and returns it
// for chaining, the same shape rag.Handler.WithBilling uses. Without it,
// submission is refused: a surface that cannot check whether the tenant can
// pay must not launch a sandbox, which is the #669 failure mode itself.
func (h *Handler) WithBilling(accounting *inference.AccountingClient, billing sessionbilling.Resolver) *Handler {
	h.accounting = accounting
	h.billing = billing
	return h
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
	taskID, suffix, ok := extractTaskPath(r.URL.Path)
	if !ok {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid task id")
		return
	}
	switch suffix {
	case "":
		if r.Method != http.MethodGet {
			apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
			return
		}
		h.handleGet(w, r, taskID)
	case "cancel":
		if r.Method != http.MethodPost {
			apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
			return
		}
		h.handleCancel(w, r, taskID)
	case "events":
		if r.Method != http.MethodGet {
			apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
			return
		}
		h.handleEvents(w, r, taskID)
	case "files":
		if r.Method != http.MethodGet {
			apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
			return
		}
		h.handleFiles(w, r, taskID)
	default:
		apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "not found")
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

	// Solvency gate (#669). Submitting a task launches a sandbox that then runs
	// model turns against this tenant, and until this gate existed the route
	// asked nothing about the tenant's balance: an account at zero credits
	// could submit indefinitely, so the insufficient-credit refusal could not
	// fire on this surface at all.
	//
	// The hold is taken and handed straight back inside Probe rather than held
	// for the task's life, because control-plane owns the task lifecycle and
	// nothing in this process would ever finalize a hold taken here (#600).
	// The gate is therefore solvency at submit time, not metering of what the
	// task goes on to spend, which is billed where it is dispatched.
	if refusal := sessionbilling.Probe(r.Context(), sessionbilling.ProbeInput{
		Accounting:  h.accounting,
		Billing:     h.billing,
		TenantID:    user.TenantID,
		Endpoint:    inference.EndpointAgentTasks,
		Label:       agentTaskLabel,
		RequestID:   uuid.New(),
		HoldCredits: agentTaskLaunchFloor,
		Surface:     "agent task",
	}); refusal != nil {
		refusal.Write(w)
		return
	}

	task, err := h.client.Create(r.Context(), user.TenantID, user.ID, req.Pack, req.Instructions, bearerJWT(r))
	if err != nil {
		writeTaskError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

// bearerJWT extracts a Supabase JWT from r's Authorization header, for
// forwarding to control-plane so a knowledge-work-pack session can later
// publish its output as an artifact under this same tenant/user (see
// apps/agent-engine/internal/artifactsclient's package doc). Returns "" for
// anything that is not that shape: a missing header, a non-Bearer scheme, or
// Hive's own "hk_"-prefixed API key (auth.UserFrom already accepted either
// shape for this route, but only a real Supabase JWT is any use to
// edge-api's JWT-gated /v1/artifacts route downstream) — every caller of
// this function already treats "" as "skip publishing", never as a reason to
// reject the request.
func bearerJWT(r *http.Request) string {
	scheme, raw, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "hk_") {
		return ""
	}
	return raw
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
	case errors.Is(err, ErrCursor):
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, ErrCursor.Error())
	default:
		// Provider-blind: the underlying error (control-plane infra detail) is never echoed.
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "agent task request failed")
	}
}

// extractTaskPath parses "/v1/agent/tasks/{id}", "/v1/agent/tasks/{id}/cancel",
// ".../events" or ".../files" into (id, suffix). ok is false for any other
// shape.
func extractTaskPath(path string) (uuid.UUID, string, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/agent/tasks/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, "", false
	}
	if len(parts) == 1 {
		return id, "", true
	}
	switch parts[1] {
	case "cancel", "events", "files":
		return id, parts[1], true
	default:
		return uuid.Nil, "", false
	}
}

// Events cursor bounds. The cap is a clamp: a client asking for "everything"
// gets the newest 500-event window instead of an error to special-case. A bad
// cursor is a 400, never silently zero.
const (
	defaultEventsLimit = 100
	maxEventsLimit     = 500
)

// handleEvents serves GET /v1/agent/tasks/{id}/events?after_seq=N&limit=M.
func (h *Handler) handleEvents(w http.ResponseWriter, r *http.Request, taskID uuid.UUID) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	var afterSeq int64
	var err error
	if raw := r.URL.Query().Get("after_seq"); raw != "" {
		if afterSeq, err = strconv.ParseInt(raw, 10, 64); err != nil || afterSeq < 0 {
			apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, ErrCursor.Error())
			return
		}
	}
	limit := defaultEventsLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		var n int
		if n, err = strconv.Atoi(raw); err != nil || n < 1 {
			apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid limit")
			return
		}
		limit = n
	}
	if limit > maxEventsLimit {
		limit = maxEventsLimit
	}

	events, err := h.client.Events(r.Context(), user.TenantID, user.ID, taskID, afterSeq, limit)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	if events == nil {
		events = []Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

// handleFiles serves GET /v1/agent/tasks/{id}/files: the running session's
// workspace listing, best-effort.
func (h *Handler) handleFiles(w http.ResponseWriter, r *http.Request, taskID uuid.UUID) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}
	files, err := h.client.Files(r.Context(), user.TenantID, user.ID, taskID)
	if err != nil {
		writeTaskError(w, err)
		return
	}
	if files == nil {
		files = []WorkspaceFile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
