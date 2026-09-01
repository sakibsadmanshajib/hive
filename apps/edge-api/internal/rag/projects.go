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
// check ownership: it returns OwnerUserID so the caller can decide, and
// Handler.authorizeProject is what a request path calls.
//
// The tenant_id predicate is in the statement rather than left to
// rag_projects_tenant_isolation. The policy is correct and is exercised by
// projects_rls_test.go, but it binds on hive_app and every deployment connects
// as postgres, which carries rolbypassrls (issues #1469, #1444, #1446), so on
// the box this WHERE clause is the tenant boundary and the policy is inert.
// Same reason SearchChunks and AttachDocument restate theirs.
func (r *Repo) GetProject(ctx context.Context, tenantID, projectID uuid.UUID) (ProjectRow, error) {
	var p ProjectRow
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		p, err = scanProject(tx.QueryRow(ctx,
			"SELECT "+projectColumns+" FROM public.rag_projects WHERE id = $1 AND tenant_id = $2",
			projectID, tenantID))
		return err
	})
	if err != nil {
		// Another tenant's project and a project id that never existed are both
		// ErrNoRows here, and both mean the same thing to the caller.
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
//
// Both predicates are in the statement. Unlike the three below, this one has no
// Go side restatement anywhere after it, so without tenant_id here a user who
// belongs to more than one tenant (personal tenants exist alongside workspaces,
// see 20260801_10_tenants_personal_owner.sql) would list every project they own
// in every tenant, whichever workspace they were acting in. Row level security
// does not supply it on a deployment that connects as a BYPASSRLS role.
func (r *Repo) ListProjects(ctx context.Context, tenantID, ownerUserID uuid.UUID) ([]ProjectRow, error) {
	var out []ProjectRow
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			"SELECT "+projectColumns+" FROM public.rag_projects"+
				" WHERE tenant_id = $1 AND owner_user_id = $2 ORDER BY created_at DESC",
			tenantID, ownerUserID)
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
// Both the owner_user_id and the tenant_id predicates travel in the UPDATE
// itself rather than only in a preceding read, so nothing can slip between the
// authorization check and the write, and neither half depends on a policy that
// does not bind on the deployed role.
func (r *Repo) UpdateProject(ctx context.Context, tenantID, ownerUserID, projectID uuid.UUID, name, instructions *string) (ProjectRow, error) {
	var p ProjectRow
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		p, err = scanProject(tx.QueryRow(ctx, `
			UPDATE public.rag_projects
			   SET name         = COALESCE($3, name),
			       instructions = COALESCE($4, instructions),
			       updated_at   = now()
			 WHERE id = $1 AND owner_user_id = $2 AND tenant_id = $5
			RETURNING `+projectColumns, projectID, ownerUserID, name, instructions, tenantID))
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
			"DELETE FROM public.rag_projects WHERE id = $1 AND owner_user_id = $2 AND tenant_id = $3",
			projectID, ownerUserID, tenantID)
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
