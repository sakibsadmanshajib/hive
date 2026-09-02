package agenttask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/rag"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling"
)

// TaskClient is the minimal interface the handler needs from Client.
// Exported so tests can inject a fake without a real control-plane.
type TaskClient interface {
	Create(ctx context.Context, tenantID, userID uuid.UUID, pack, instructions string, projectID uuid.UUID, attachments []Attachment, bearerJWT string) (Task, error)
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

	// projects authorizes an attached project id. Nil when the deployment has
	// no database wired, in which case any project id on a create is refused
	// rather than passed through unchecked: a surface that cannot verify
	// ownership must not act on a client's claim of it.
	projects ProjectAuthorizer
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

// WithProjectAuthorizer wires project ownership verification onto an existing
// Handler and returns it for chaining, the same shape WithBilling uses. Without
// it, a create carrying a project_id is refused: this surface accepts a client
// supplied project id, and the one thing it must never do is trust it.
func (h *Handler) WithProjectAuthorizer(p ProjectAuthorizer) *Handler {
	h.projects = p
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
	// ProjectID optionally attaches a project to this run (issue #1595,
	// advancing issue #1312). Client supplied, so it is authorized before
	// control-plane is called at all; see handleCreate.
	ProjectID string `json:"project_id,omitempty"`
	// Attachments are the documents the person attached in the composer
	// before starting the run (issue #1065), already extracted to text by the
	// surface they uploaded on. Carried inline rather than by id on purpose:
	// the sandbox has no credential for Hive's storage and no route to it, so
	// something has to hand it the bytes, and the browser that uploaded them
	// is the one party already authorized to read them. No new read path, no
	// new permission, nothing widened.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is one document travelling with a task, validated here and
// forwarded to control-plane and from there to the launcher, which writes it
// into the sandbox's working directory.
type Attachment struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

const (
	// maxAttachments bounds how many documents one run can carry.
	maxAttachments = 5
	// maxAttachmentBytes bounds their combined extracted text.
	//
	// ponytail: an inline cap, not a general file transfer. It comfortably
	// holds an ordinary document (a 100 page report extracts to well under
	// this) and it is the honest ceiling of carrying content on the create
	// request at all. The upgrade path, when a run needs a 25 MB PDF, is for
	// the launcher to fetch the document itself, which needs a credential and
	// a route it does not have today.
	maxAttachmentBytes = 256 << 10
	// maxAttachmentNameBytes matches what a POSIX file name can be.
	maxAttachmentNameBytes = 255
	// maxCreateBodyBytes is derived from the content cap rather than picked,
	// because JSON escaping is what decides how much wire a legal attachment
	// takes. A control character encodes as \uXXXX, six bytes for one, so the
	// worst case is six times maxAttachmentBytes; eight leaves room for the
	// names, the pack, the instructions and the quoting around all of it.
	//
	// Getting this wrong is not a size error, it is a wrong error: a legal
	// attachment would come back as "invalid request body", and the person
	// would read a malformed-JSON refusal for a document that was fine.
	maxCreateBodyBytes = 8 * maxAttachmentBytes
)

// validateAttachments refuses anything the launcher would refuse, before a
// credit hold is taken and before control-plane is called. The launcher checks
// the names again because it is the process that turns one into a path; this
// check exists so a bad request never becomes a row.
func validateAttachments(in []Attachment) error {
	if len(in) == 0 {
		return nil
	}
	if len(in) > maxAttachments {
		return fmt.Errorf("a task can carry at most %d attachments", maxAttachments)
	}
	total := 0
	for _, a := range in {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return errors.New("attachment name required")
		}
		if len(name) > maxAttachmentNameBytes {
			return errors.New("attachment name is too long")
		}
		if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
			return fmt.Errorf("attachment name %q is not a file name", name)
		}
		// Control characters, NUL and newline included. A newline in a name is
		// not merely an odd file name: the name is repeated back to the model
		// as a bullet in the run's initial message, so one would let the person
		// forge extra lines there.
		if strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return fmt.Errorf("attachment name %q is not a file name", name)
		}
		if a.Content == "" {
			return fmt.Errorf("attachment %q is empty", name)
		}
		total += len(a.Content)
	}
	if total > maxAttachmentBytes {
		return fmt.Errorf("attachments total more than %d KiB of text", maxAttachmentBytes>>10)
	}
	return nil
}

// ProjectAuthorizer answers whether the submitting user may attach the project
// they named. One method, declared here at the point of use rather than
// exported as an abstraction from the package that implements it;
// apps/edge-api/internal/rag.ProjectAuthorizer satisfies it.
type ProjectAuthorizer interface {
	AuthorizeProject(ctx context.Context, tenantID, userID, projectID uuid.UUID) error
}

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	var req createTaskRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxCreateBodyBytes)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}
	// An absent pack is a normal submission since issue #1623, not a bad
	// request: the composer stopped asking a customer to choose between two
	// words that name a system prompt, and control-plane resolves it from the
	// instructions instead (agenttask.InferPack). Whitespace normalises to the
	// same empty value so a hand-rolled client sending " " takes the same
	// route rather than reaching the pack CHECK constraint.
	//
	// Deliberately no default substituted here. Two layers each holding their
	// own idea of the default is how the two ends come to disagree about what
	// actually ran; this edge forwards what it was given and control-plane is
	// the single place that decides.
	req.Pack = strings.TrimSpace(req.Pack)

	// Ahead of the project check and the solvency gate, for the same reason
	// that check is ahead of the gate: a request that cannot be honoured
	// should not take a hold or create a row first.
	if err := validateAttachments(req.Attachments); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, err.Error())
		return
	}

	// Project authorization, deliberately ahead of the solvency gate below
	// (issue #1595 acceptance criterion 6). project_id arrives from the client
	// and public.rag_documents is tenant scoped rather than user scoped, so
	// without this check one member of a tenant could launch a run over
	// another member's private project: row level security keys on the tenant
	// and both members present the same tenant. Ordering it first also means an
	// unauthorized id never takes a credit hold and never reaches
	// control-plane.
	projectID := uuid.Nil
	if raw := strings.TrimSpace(req.ProjectID); raw != "" {
		parsed, perr := uuid.Parse(raw)
		if perr != nil {
			apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid project_id")
			return
		}
		if h.projects == nil {
			// Fail closed. A deployment with no authorizer cannot tell whose
			// project this is, and passing it through unchecked is the whole
			// defect. The operator gets the reason; the customer gets the same
			// refusal any unowned project id gets.
			log.Printf("agenttask: refusing a task carrying project_id=%s: no project authorizer is wired on this deployment", parsed)
			apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "project not found")
			return
		}
		if aerr := h.projects.AuthorizeProject(r.Context(), user.TenantID, user.ID, parsed); aerr != nil {
			writeProjectRefusal(w, aerr)
			return
		}
		projectID = parsed
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

	task, err := h.client.Create(r.Context(), user.TenantID, user.ID, req.Pack, req.Instructions, projectID, req.Attachments, bearerJWT(r))
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

// writeProjectRefusal renders a project authorization failure. A project the
// caller does not own is a 404 naming nothing: a 403 would confirm the project
// exists, which is exactly what lets a caller enumerate project ids. Anything
// else is an infrastructure failure and must NOT be flattened into the same
// 404, because a database blip would then silently look like "that project is
// not yours" and quietly discard a legitimate task.
func writeProjectRefusal(w http.ResponseWriter, err error) {
	if errors.Is(err, rag.ErrProjectForbidden) {
		apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "project not found")
		return
	}
	log.Printf("agenttask: project authorization failed: %v", err)
	apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "agent task request failed")
}
