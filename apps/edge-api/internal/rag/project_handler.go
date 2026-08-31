package rag

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

const (
	// maxProjectBodyBytes bounds a project create or update body. Both fields
	// are free text, and instructions are prose a user writes, so this is
	// larger than a bare name needs and far smaller than a document.
	maxProjectBodyBytes = 64 * 1024

	// maxProjectNameBytes is what a sidebar row can render without becoming a
	// denial-of-service on the reader.
	maxProjectNameBytes = 200

	// maxProjectInstructionsBytes bounds the per project instructions. These
	// reach a model on every turn of an attached conversation, so an unbounded
	// value would be an unbounded per turn prompt cost.
	maxProjectInstructionsBytes = 32 * 1024
)

// authorizeProjectOwnership is THE authorization decision for every project
// scoped operation, in this package and outside it. There is exactly one
// implementation on purpose: a second copy is how two surfaces end up
// disagreeing about who owns what.
//
// Why it has to exist at all, stated plainly because it is the point of this
// seam. A project id arrives from the client, and public.rag_documents is
// tenant scoped rather than user scoped. Row level security on
// app.current_tenant_id therefore stops a caller in tenant A from reading
// tenant B's project, and does nothing whatever about two members of the same
// tenant: they present the identical app.current_tenant_id, so filtering by a
// supplied project id without checking who owns it hands one member another
// member's private passages.
//
// Both refusals return ErrProjectForbidden and nothing else, so a caller cannot
// tell "no such project" from "not yours" and cannot enumerate project ids.
//
// Called BEFORE any filtering, never after it and never instead of it.
func authorizeProjectOwnership(ctx context.Context, s Store, tenantID, userID, projectID uuid.UUID) error {
	project, err := s.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return err
	}
	// project.TenantID is already guaranteed by row level security. Restated
	// here as defence in depth for the same reason SearchChunks restates its
	// tenant filter: a SECURITY DEFINER path or a BYPASSRLS role would
	// otherwise remove the only check standing.
	if project.TenantID != tenantID || project.OwnerUserID != userID {
		return ErrProjectForbidden
	}
	return nil
}

// authorizeProject is the Handler's own entry point to the decision above.
func (h *Handler) authorizeProject(ctx context.Context, tenantID, userID, projectID uuid.UUID) error {
	return authorizeProjectOwnership(ctx, h.store, tenantID, userID, projectID)
}

// writeProjectError renders a project failure. ErrProjectForbidden is a 404
// with a message that names nothing: a 403 would confirm the project exists,
// which is exactly what the single-refusal rule above avoids. Anything else is
// an infrastructure failure, logged for an operator and answered
// provider-blind.
func writeProjectError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrProjectForbidden) {
		apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "project not found")
		return
	}
	log.Printf("rag: project operation failed: %v", err)
	apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "project request failed")
}

func projectRowToResponse(p ProjectRow) ProjectResponse {
	return ProjectResponse{
		ID:           p.ID.String(),
		Name:         p.Name,
		Instructions: p.Instructions,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

// decodeProjectRequest reads and validates a project body. Trimming happens
// here so a name of pure whitespace is rejected rather than stored, which the
// migration's own CHECK would refuse anyway.
func decodeProjectRequest(r *http.Request) (ProjectRequest, *uploadError) {
	var req ProjectRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, maxProjectBodyBytes))
	if err := dec.Decode(&req); err != nil {
		return req, &uploadError{status: http.StatusBadRequest, msg: "invalid request body"}
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			return req, &uploadError{status: http.StatusBadRequest, msg: "name required"}
		}
		if len(trimmed) > maxProjectNameBytes {
			return req, &uploadError{status: http.StatusBadRequest, msg: "name too long"}
		}
		req.Name = &trimmed
	}
	if req.Instructions != nil && len(*req.Instructions) > maxProjectInstructionsBytes {
		return req, &uploadError{status: http.StatusRequestEntityTooLarge,
			code: apierrors.CodeRequestTooLarge, msg: "instructions too long"}
	}
	return req, nil
}

// routeProjects serves the collection: POST creates, GET lists.
func (h *Handler) routeProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleCreateProject(w, r)
	case http.MethodGet:
		h.handleListProjects(w, r)
	default:
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
	}
}

// routeProjectByID serves /v1/rag/projects/{id} and
// /v1/rag/projects/{id}/documents.
func (h *Handler) routeProjectByID(w http.ResponseWriter, r *http.Request) {
	projectID, suffix, ok := extractProjectPath(r.URL.Path)
	if !ok {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid project id")
		return
	}
	switch {
	case suffix == "" && r.Method == http.MethodGet:
		h.handleGetProject(w, r, projectID)
	case suffix == "" && r.Method == http.MethodPatch:
		h.handleUpdateProject(w, r, projectID)
	case suffix == "" && r.Method == http.MethodDelete:
		h.handleDeleteProject(w, r, projectID)
	case suffix == "documents" && r.Method == http.MethodPost:
		h.handleAttachDocument(w, r, projectID)
	case suffix == "" || suffix == "documents":
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
	default:
		apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "not found")
	}
}

// extractProjectPath parses "/v1/rag/projects/{id}" and
// "/v1/rag/projects/{id}/documents" into (id, suffix). ok is false for
// any other shape, including an id that is not a UUID, so a malformed id never
// reaches a query.
func extractProjectPath(path string) (uuid.UUID, string, bool) {
	rest := strings.Trim(strings.TrimPrefix(path, "/v1/rag/projects/"), "/")
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
	return id, parts[1], true
}

func (h *Handler) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}
	req, uerr := decodeProjectRequest(r)
	if uerr != nil {
		writeUploadError(w, uerr)
		return
	}
	if req.Name == nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "name required")
		return
	}
	instructions := ""
	if req.Instructions != nil {
		instructions = *req.Instructions
	}
	project, err := h.store.CreateProject(r.Context(), user.TenantID, user.ID, *req.Name, instructions)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	h.audit(r.Context(), "RAG_PROJECT_CREATE", "rag_project", project.ID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), map[string]any{"name": project.Name})
	writeProjectJSON(w, http.StatusCreated, projectRowToResponse(project))
}

func (h *Handler) handleListProjects(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}
	projects, err := h.store.ListProjects(r.Context(), user.TenantID, user.ID)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	out := make([]ProjectResponse, len(projects))
	for i, p := range projects {
		out[i] = projectRowToResponse(p)
	}
	writeProjectJSON(w, http.StatusOK, map[string]any{"data": out, "object": "list"})
}

func (h *Handler) handleGetProject(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}
	if err := h.authorizeProject(r.Context(), user.TenantID, user.ID, projectID); err != nil {
		writeProjectError(w, err)
		return
	}
	project, err := h.store.GetProject(r.Context(), user.TenantID, projectID)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeProjectJSON(w, http.StatusOK, projectRowToResponse(project))
}

func (h *Handler) handleUpdateProject(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}
	req, uerr := decodeProjectRequest(r)
	if uerr != nil {
		writeUploadError(w, uerr)
		return
	}
	if req.Name == nil && req.Instructions == nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "name or instructions required")
		return
	}
	// UpdateProject carries the owner predicate in its own UPDATE, so this is
	// not the only guard; it is here so a caller who owns nothing gets the same
	// 404 as every other project refusal rather than a bare no-rows path.
	if err := h.authorizeProject(r.Context(), user.TenantID, user.ID, projectID); err != nil {
		writeProjectError(w, err)
		return
	}
	project, err := h.store.UpdateProject(r.Context(), user.TenantID, user.ID, projectID, req.Name, req.Instructions)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	h.audit(r.Context(), "RAG_PROJECT_UPDATE", "rag_project", project.ID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), map[string]any{"name": project.Name})
	writeProjectJSON(w, http.StatusOK, projectRowToResponse(project))
}

func (h *Handler) handleDeleteProject(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}
	if err := h.authorizeProject(r.Context(), user.TenantID, user.ID, projectID); err != nil {
		writeProjectError(w, err)
		return
	}
	if err := h.store.DeleteProject(r.Context(), user.TenantID, user.ID, projectID); err != nil {
		writeProjectError(w, err)
		return
	}
	// Audit only fires when a row was actually removed, matching handleDelete.
	h.audit(r.Context(), "RAG_PROJECT_DELETE", "rag_project", projectID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleAttachDocument(w http.ResponseWriter, r *http.Request, projectID uuid.UUID) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}
	var req AttachDocumentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxProjectBodyBytes)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}
	docID, err := uuid.Parse(strings.TrimSpace(req.DocumentID))
	if err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid document_id")
		return
	}
	if aerr := h.authorizeProject(r.Context(), user.TenantID, user.ID, projectID); aerr != nil {
		writeProjectError(w, aerr)
		return
	}
	if aerr := h.store.AttachDocument(r.Context(), user.TenantID, user.ID, projectID, docID); aerr != nil {
		writeProjectError(w, aerr)
		return
	}
	h.audit(r.Context(), "RAG_PROJECT_DOCUMENT_ATTACH", "rag_document", docID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), map[string]any{"project_id": projectID.String()})
	w.WriteHeader(http.StatusNoContent)
}

func writeProjectJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ProjectAuthorizer exposes the ownership decision to surfaces outside this
// package, today POST /v1/agent/tasks, which accepts the same client supplied
// project id and needs the same answer. It deliberately exposes nothing else:
// the caller gets to ask "may this user use this project" and cannot reach the
// documents, the passages, or the project's own fields through it.
type ProjectAuthorizer struct {
	store Store
}

// NewProjectAuthorizer wraps a store. A nil store is a programming error rather
// than a posture: callers that have no database must not construct one, so the
// surface can refuse a project id outright instead of authorizing against
// nothing.
func NewProjectAuthorizer(s Store) *ProjectAuthorizer {
	return &ProjectAuthorizer{store: s}
}

// AuthorizeProject reports whether userID within tenantID owns projectID,
// returning ErrProjectForbidden when they do not, whatever the reason.
func (a *ProjectAuthorizer) AuthorizeProject(ctx context.Context, tenantID, userID, projectID uuid.UUID) error {
	return authorizeProjectOwnership(ctx, a.store, tenantID, userID, projectID)
}
