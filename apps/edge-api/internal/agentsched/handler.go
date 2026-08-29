package agentsched

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
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling"
)

// ScheduleClient is the minimal interface the handler needs from Client.
// Exported so tests can inject a fake without a real control-plane.
type ScheduleClient interface {
	Create(ctx context.Context, tenantID, userID uuid.UUID, name, instructions, schedule string) (Schedule, error)
	List(ctx context.Context, tenantID, userID uuid.UUID) ([]Schedule, error)
	Get(ctx context.Context, tenantID, userID, id uuid.UUID) (Schedule, error)
	Update(ctx context.Context, tenantID, userID, id uuid.UUID, body updateBody) (Schedule, error)
	Delete(ctx context.Context, tenantID, userID, id uuid.UUID) error
}

// Handler serves /v1/agent/schedules routes. Callers wrap with
// featuregate.Require(FeatureCowork) before mounting, mirroring
// apps/edge-api/internal/agenttask's gating contract.
type Handler struct {
	client     ScheduleClient
	accounting *inference.AccountingClient
	billing    sessionbilling.Resolver
}

func NewHandler(client ScheduleClient) *Handler {
	return &Handler{client: client}
}

// scheduleLaunchFloor is the credit balance a tenant must be able to cover
// before a routine is created: the same launch floor a direct submission
// holds, because a routine is a submission on a timer.
const scheduleLaunchFloor = inference.DefaultHoldText

// scheduleLabel is what the probe records where an inference request records
// its model alias. Operator-facing, never customer-visible.
const scheduleLabel = "agent-schedule"

// WithBilling wires the solvency gate onto an existing Handler and returns it
// for chaining, the same shape agenttask.Handler.WithBilling uses. Without it,
// creating a routine is refused.
//
// KNOWN GAP, stated rather than implied. This gates the CREATION of a routine
// only. Each firing of an existing routine is turned into a task by
// control-plane's own scheduler, which calls its task store directly and never
// passes through this process, so a routine created while solvent goes on
// firing after the balance runs out. Closing that needs the same probe inside
// control-plane's scheduler, which is not this package and not this PR.
func (h *Handler) WithBilling(accounting *inference.AccountingClient, billing sessionbilling.Resolver) *Handler {
	h.accounting = accounting
	h.billing = billing
	return h
}

// Register mounts all /v1/agent/schedules* routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/agent/schedules", h.routeCollection)
	mux.HandleFunc("/v1/agent/schedules/", h.routeItem)
}

func (h *Handler) routeCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleCreate(w, r)
	case http.MethodGet:
		h.handleList(w, r)
	default:
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
	}
}

func (h *Handler) routeItem(w http.ResponseWriter, r *http.Request) {
	id, err := extractSchedulePath(r.URL.Path)
	if err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid schedule id")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r, id)
	case http.MethodPut:
		h.handleUpdate(w, r, id)
	case http.MethodDelete:
		h.handleDelete(w, r, id)
	default:
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
	}
}

func extractSchedulePath(path string) (uuid.UUID, error) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/agent/schedules/"), "/")
	return uuid.Parse(rest)
}

type createReq struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Schedule     string `json:"schedule"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := authUser(w, r)
	if !ok {
		return
	}
	var req createReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}
	// Solvency gate (#669). A routine is a task submission on a timer, so the
	// same question is asked here as at POST /v1/agent/tasks: creating one
	// while insolvent would otherwise be the way around that gate. The hold is
	// taken and handed straight back; see WithBilling for what this does and
	// does not cover.
	if refusal := sessionbilling.Probe(r.Context(), sessionbilling.ProbeInput{
		Accounting:  h.accounting,
		Billing:     h.billing,
		TenantID:    user.TenantID,
		Endpoint:    inference.EndpointAgentTasks,
		Label:       scheduleLabel,
		RequestID:   uuid.New(),
		HoldCredits: scheduleLaunchFloor,
		Surface:     "agent schedule",
	}); refusal != nil {
		refusal.Write(w)
		return
	}

	schedule, err := h.client.Create(r.Context(), user.TenantID, user.ID, req.Name, req.Instructions, req.Schedule)
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, schedule)
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	user, ok := authUser(w, r)
	if !ok {
		return
	}
	schedules, err := h.client.List(r.Context(), user.TenantID, user.ID)
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": schedules})
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	user, ok := authUser(w, r)
	if !ok {
		return
	}
	schedule, err := h.client.Get(r.Context(), user.TenantID, user.ID, id)
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schedule)
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	user, ok := authUser(w, r)
	if !ok {
		return
	}
	var req updateBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}
	schedule, err := h.client.Update(r.Context(), user.TenantID, user.ID, id, req)
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, schedule)
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	user, ok := authUser(w, r)
	if !ok {
		return
	}
	if err := h.client.Delete(r.Context(), user.TenantID, user.ID, id); err != nil {
		writeScheduleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authUser resolves the authenticated user or writes the 401 and reports
// false. Mirrors agenttask's handler pattern with less repetition.
func authUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return auth.User{}, false
	}
	return *user, true
}

func writeScheduleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "schedule not found")
	case errors.Is(err, ErrInvalidInput):
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid schedule input")
	default:
		// Provider-blind: control-plane infra detail is never echoed.
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "agent schedule request failed")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
