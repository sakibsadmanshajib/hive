package agentsched

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Handler serves the service-to-service schedule CRUD surface edge-api calls.
// tenant_id and user_id travel as URL path segments, not body/header/query:
// the process boundary means the caller's own authenticated context cannot
// cross it. edge-api resolves both solely from auth.TenantID(ctx) /
// auth.UserFrom(ctx) before building the path. Mirrors
// apps/control-plane/internal/agenttask/http.go's InternalMux.
type Handler struct {
	svc *Service
}

// NewHandler constructs the agentsched HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// InternalMux returns the shared-secret-guarded service-to-service surface.
// Wrap with RequireInternalToken in router wiring.
func (h *Handler) InternalMux() http.Handler {
	return http.HandlerFunc(h.serveInternal)
}

const internalPrefix = "/internal/agent-schedules/"

// maxBodyBytes caps request bodies this handler decodes: a create/update body
// carries up to 4000 chars of instructions plus a name and a cadence string,
// and 64 KiB is generous headroom for that. Matches agenttask's handler.
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
			return
		case http.MethodGet:
			h.handleList(w, r, tenantID, userID)
			return
		}
	case 3:
		id, parseErr := uuid.Parse(parts[2])
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, errBody("invalid schedule id"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.handleGet(w, r, tenantID, userID, id)
			return
		case http.MethodPut:
			h.handleUpdate(w, r, tenantID, userID, id)
			return
		case http.MethodDelete:
			h.handleDelete(w, r, tenantID, userID, id)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, errBody("not found"))
}

type createRequest struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Schedule     string `json:"schedule"`
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request, tenantID, userID uuid.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	sched, err := h.svc.Create(r.Context(), tenantID, userID, CreateInput{
		Name:         req.Name,
		Instructions: req.Instructions,
		Schedule:     req.Schedule,
	})
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, newScheduleWire(sched))
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request, tenantID, userID uuid.UUID) {
	schedules, err := h.svc.List(r.Context(), tenantID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody("failed to list schedules"))
		return
	}
	out := make([]scheduleWire, 0, len(schedules))
	for _, s := range schedules {
		out = append(out, newScheduleWire(s))
	}
	writeJSON(w, http.StatusOK, listResponse{Schedules: out})
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, tenantID, userID, id uuid.UUID) {
	sched, err := h.svc.Get(r.Context(), tenantID, userID, id)
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newScheduleWire(sched))
}

// updateRequest carries the full replacement body. PUT semantics: every field
// is required; absent means explicitly empty, and validation rejects that
// where empty is invalid.
type updateRequest struct {
	Name         string `json:"name"`
	Instructions string `json:"instructions"`
	Schedule     string `json:"schedule"`
	Enabled      bool   `json:"enabled"`
}

func (h *Handler) handleUpdate(w http.ResponseWriter, r *http.Request, tenantID, userID, id uuid.UUID) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid JSON body"))
		return
	}
	sched, err := h.svc.Update(r.Context(), tenantID, userID, id, UpdateInput{
		Name:         req.Name,
		Instructions: req.Instructions,
		Schedule:     req.Schedule,
		Enabled:      req.Enabled,
	})
	if err != nil {
		writeScheduleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newScheduleWire(sched))
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request, tenantID, userID, id uuid.UUID) {
	if err := h.svc.Delete(r.Context(), tenantID, userID, id); err != nil {
		writeScheduleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeScheduleError maps service sentinels onto status codes. Provider-blind
// default: the underlying error (DB failure detail) is never echoed.
func writeScheduleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody("schedule not found"))
	case errors.Is(err, ErrInvalidName),
		errors.Is(err, ErrInvalidInstructions),
		errors.Is(err, ErrInvalidSchedule):
		writeJSON(w, http.StatusBadRequest, errBody(err.Error()))
	case errors.Is(err, ErrInsufficientCredits):
		writeJSON(w, http.StatusPaymentRequired, errBody("insufficient credits to create a scheduled task"))
	case errors.Is(err, ErrSolvencyUnavailable):
		// 503, not 402 and not 500. The balance is unknown, so the honest
		// answer is "ask again", and a fixed string is written rather than
		// err.Error() because the wrapped cause is a driver message that can
		// carry an internal host and port (#1490).
		writeJSON(w, http.StatusServiceUnavailable, errBody("could not verify the account credit balance; try again"))
	default:
		writeJSON(w, http.StatusInternalServerError, errBody("agent schedule request failed"))
	}
}

type scheduleWire struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Instructions string     `json:"instructions"`
	Schedule     string     `json:"schedule"`
	Enabled      bool       `json:"enabled"`
	NextRunAt    *time.Time `json:"next_run_at"`
	LastRunAt    *time.Time `json:"last_run_at"`
	LastTaskID   string     `json:"last_task_id"`
	LastError    string     `json:"last_error"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type listResponse struct {
	Schedules []scheduleWire `json:"schedules"`
}

func newScheduleWire(s Schedule) scheduleWire {
	lastTaskID := ""
	if s.LastTaskID != nil {
		lastTaskID = s.LastTaskID.String()
	}
	return scheduleWire{
		ID:           s.ID.String(),
		Name:         s.Name,
		Instructions: s.Instructions,
		Schedule:     s.Schedule,
		Enabled:      s.Enabled,
		NextRunAt:    s.NextRunAt,
		LastRunAt:    s.LastRunAt,
		LastTaskID:   lastTaskID,
		LastError:    s.LastError,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}
