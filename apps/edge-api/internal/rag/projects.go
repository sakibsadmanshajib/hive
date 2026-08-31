package rag

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrProjectForbidden is the single refusal every failed project authorization
// returns, whatever the reason: the project does not exist, it belongs to
// another tenant, or it belongs to another member of the caller's own tenant.
//
// One error for all three is deliberate. Distinguishing "no such project" from
// "not yours" tells a caller which project ids are real, and a project id is a
// bare UUID any client can supply freely. Callers render this as 404 with a
// message that names nothing, so the response is identical in all three cases.
var ErrProjectForbidden = errors.New("rag: project not found or not owned by the caller")

// ProjectRow mirrors the public.rag_projects columns the surface reads.
type ProjectRow struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	OwnerUserID  uuid.UUID
	Name         string
	Instructions string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const projectColumns = "id, tenant_id, owner_user_id, name, instructions, created_at, updated_at"

// scanProject reads one rag_projects row in projectColumns order.
func scanProject(row pgx.Row) (ProjectRow, error) {
	var p ProjectRow
	err := row.Scan(&p.ID, &p.TenantID, &p.OwnerUserID, &p.Name, &p.Instructions, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// CreateProject inserts one project owned by ownerUserID within tenantID.
func (r *Repo) CreateProject(ctx context.Context, tenantID, ownerUserID uuid.UUID, name, instructions string) (ProjectRow, error) {
	var p ProjectRow
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		p, err = scanProject(tx.QueryRow(ctx, `
			INSERT INTO public.rag_projects (tenant_id, owner_user_id, name, instructions)
			VALUES ($1, $2, $3, $4)
			RETURNING `+projectColumns, tenantID, ownerUserID, name, instructions))
		return err
	})
	if err != nil {
		return ProjectRow{}, fmt.Errorf("rag.repo: create project: %w", err)
	}
	return p, nil
}

// GetProject fetches one project scoped to tenantID. It deliberately does NOT
// check ownership: RLS covers the tenant boundary and this returns OwnerUserID
// so the caller can decide. Handler.authorizeProject is what a request path
// calls; this exists for the paths that need the row itself.
func (r *Repo) GetProject(ctx context.Context, tenantID, projectID uuid.UUID) (ProjectRow, error) {
	var p ProjectRow
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		p, err = scanProject(tx.QueryRow(ctx,
			"SELECT "+projectColumns+" FROM public.rag_projects WHERE id = $1", projectID))
		return err
	})
	if err != nil {
		// A row filtered out by RLS (another tenant's project) and a project id
		// that never existed are both ErrNoRows here, and both mean the same
		// thing to the caller.
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectRow{}, ErrProjectForbidden
		}
		return ProjectRow{}, fmt.Errorf("rag.repo: get project: %w", err)
	}
	return p, nil
}

// ListProjects returns the projects ownerUserID owns within tenantID, newest
// first. Owner scoped rather than merely tenant scoped: a project belongs to
// one user, per group sharing is out of scope (issue #1595 section 14), and
// listing every tenant member's projects would be the same cross-member
// exposure the retrieval path refuses.
func (r *Repo) ListProjects(ctx context.Context, tenantID, ownerUserID uuid.UUID) ([]ProjectRow, error) {
	var out []ProjectRow
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			"SELECT "+projectColumns+" FROM public.rag_projects WHERE owner_user_id = $1 ORDER BY created_at DESC",
			ownerUserID)
		if err != nil {
			return fmt.Errorf("rag.repo: list projects: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			p, serr := scanProject(rows)
			if serr != nil {
				return fmt.Errorf("rag.repo: read project row: %w", serr)
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateProject renames a project and/or replaces its instructions. Both
// arguments are pointers so "leave this field alone" is expressible: a rename
// must not blank the instructions and an instructions edit must not blank the
// name.
//
// The owner_user_id predicate travels in the UPDATE itself rather than only in
// a preceding read, so nothing can slip between the authorization check and the
// write.
func (r *Repo) UpdateProject(ctx context.Context, tenantID, ownerUserID, projectID uuid.UUID, name, instructions *string) (ProjectRow, error) {
	var p ProjectRow
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		p, err = scanProject(tx.QueryRow(ctx, `
			UPDATE public.rag_projects
			   SET name         = COALESCE($3, name),
			       instructions = COALESCE($4, instructions),
			       updated_at   = now()
			 WHERE id = $1 AND owner_user_id = $2
			RETURNING `+projectColumns, projectID, ownerUserID, name, instructions))
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectRow{}, ErrProjectForbidden
		}
		return ProjectRow{}, fmt.Errorf("rag.repo: update project: %w", err)
	}
	return p, nil
}

// DeleteProject removes one project owned by ownerUserID. Its documents survive
// with project_id NULL (ON DELETE SET NULL in the migration): deleting a
// project must not silently destroy the tenant's uploaded documents.
func (r *Repo) DeleteProject(ctx context.Context, tenantID, ownerUserID, projectID uuid.UUID) error {
	return r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			"DELETE FROM public.rag_projects WHERE id = $1 AND owner_user_id = $2",
			projectID, ownerUserID)
		if err != nil {
			return fmt.Errorf("rag.repo: delete project: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrProjectForbidden
		}
		return nil
	})
}

// AttachDocument points one document at one project. Both predicates travel in
// the statement: the project must be owned by ownerUserID and the document must
// belong to the caller's tenant, which RLS enforces and the WHERE clause
// restates as defence in depth.
//
// A document id that does not exist, one that belongs to another tenant, and a
// project the caller does not own all produce ErrProjectForbidden. Same
// reasoning as GetProject: the caller supplied both ids and learns nothing
// about which of them was the problem.
func (r *Repo) AttachDocument(ctx context.Context, tenantID, ownerUserID, projectID, docID uuid.UUID) error {
	return r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE public.rag_documents
			   SET project_id = $1, updated_at = now()
			 WHERE id = $2
			   AND tenant_id = $3
			   AND EXISTS (
			       SELECT 1 FROM public.rag_projects
			        WHERE id = $1 AND tenant_id = $3 AND owner_user_id = $4
			   )`, projectID, docID, tenantID, ownerUserID)
		if err != nil {
			return fmt.Errorf("rag.repo: attach document: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrProjectForbidden
		}
		return nil
	})
}
