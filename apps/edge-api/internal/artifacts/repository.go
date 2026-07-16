package artifacts

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VersionRow is one resolved artifact version, joined with its parent
// artifact's visibility flag.
type VersionRow struct {
	ArtifactID  uuid.UUID
	Version     int
	StoragePath string
	SizeBytes   int64
	IsPublic    bool
}

// queryer is the minimal pgx surface shared by *pgxpool.Pool and pgx.Tx, so
// queryVersion runs identically inside a tenant-scoped transaction (private
// artifact reads) or directly against the pool with no transaction at all
// (anonymous public reads, see Repo.GetVersion).
type queryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Repo handles artifacts DB operations in the edge-api.
// RLS is enforced by setting app.current_tenant_id before every
// tenant-scoped query; see withTenantTx and GetVersion.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo creates a Repo backed by pool.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// withTenantTx runs fn inside an explicit transaction with the RLS session
// variable set LOCAL (transaction-scoped) to tenantID. hive_app is NOT
// BYPASSRLS, so every query against public.artifacts / public.artifact_versions
// must see app.current_tenant_id set to the caller's tenant for the
// tenant-isolation policy to admit rows. Mirrors
// apps/edge-api/internal/rag/repository.go.withTenantTx.
func (r *Repo) withTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("artifacts.repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("artifacts.repo: set tenant: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CreateArtifact registers a new artifact (latest_version=0, no version rows
// yet) and returns its assigned id.
func (r *Repo) CreateArtifact(ctx context.Context, tenantID uuid.UUID, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO public.artifacts (tenant_id, name)
			VALUES ($1, $2)
			RETURNING id`,
			tenantID, name,
		).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("artifacts.repo: create artifact: %w", err)
	}
	return id, nil
}

// AddVersion inserts the next sequential version for artifactID and bumps
// artifacts.latest_version in the same transaction, so a same-id redeploy
// mints a new version at the same URL. SELECT ... FOR UPDATE on the
// artifacts row serializes concurrent redeploys of the same artifact so two
// requests never compute the same next version number.
func (r *Repo) AddVersion(ctx context.Context, tenantID, artifactID uuid.UUID, storagePath string, sizeBytes int64) (int, error) {
	var version int
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var current int
		if err := tx.QueryRow(ctx,
			`SELECT latest_version FROM public.artifacts WHERE id = $1 FOR UPDATE`,
			artifactID,
		).Scan(&current); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("artifacts.repo: lock artifact: %w", err)
		}
		version = current + 1
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.artifact_versions (artifact_id, tenant_id, version, storage_path, size_bytes)
			VALUES ($1, $2, $3, $4, $5)`,
			artifactID, tenantID, version, storagePath, sizeBytes,
		); err != nil {
			return fmt.Errorf("artifacts.repo: insert version: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE public.artifacts SET latest_version = $1, updated_at = now() WHERE id = $2`,
			version, artifactID,
		); err != nil {
			return fmt.Errorf("artifacts.repo: bump latest_version: %w", err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return version, nil
}

// SetPublic flips the share flag for artifactID, scoped to tenantID.
// Returns ErrNotFound when no row matched (wrong tenant or unknown id).
func (r *Repo) SetPublic(ctx context.Context, tenantID, artifactID uuid.UUID, public bool) error {
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE public.artifacts SET is_public = $1, updated_at = now() WHERE id = $2`,
			public, artifactID,
		)
		if err != nil {
			return fmt.Errorf("artifacts.repo: set public: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// GetVersion resolves the version to serve for an artifact. version == nil
// means "latest" (COALESCE to artifacts.latest_version).
//
// viewerTenantID is uuid.Nil for anonymous requests: the query then runs
// with no app.current_tenant_id session variable set at all, so only the
// public-read RLS policy (is_public = true) can admit the row -- the
// tenant-isolation policy never matches a NULL current_tenant_id. An
// authenticated viewerTenantID additionally sees their own tenant's private
// artifacts, because Postgres OR-combines permissive RLS policies for
// SELECT: either policy passing admits the row.
func (r *Repo) GetVersion(ctx context.Context, viewerTenantID, artifactID uuid.UUID, version *int) (VersionRow, error) {
	if viewerTenantID == uuid.Nil {
		return r.queryVersion(ctx, r.pool, artifactID, version)
	}
	var row VersionRow
	err := r.withTenantTx(ctx, viewerTenantID, func(tx pgx.Tx) error {
		v, err := r.queryVersion(ctx, tx, artifactID, version)
		row = v
		return err
	})
	if err != nil {
		return VersionRow{}, err
	}
	return row, nil
}

func (r *Repo) queryVersion(ctx context.Context, q queryer, artifactID uuid.UUID, version *int) (VersionRow, error) {
	var v VersionRow
	err := q.QueryRow(ctx, `
		SELECT av.artifact_id, av.version, av.storage_path, av.size_bytes, a.is_public
		FROM public.artifact_versions av
		JOIN public.artifacts a ON a.id = av.artifact_id
		WHERE av.artifact_id = $1
		  AND av.version = COALESCE($2, a.latest_version)`,
		artifactID, version,
	).Scan(&v.ArtifactID, &v.Version, &v.StoragePath, &v.SizeBytes, &v.IsPublic)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VersionRow{}, ErrNotFound
		}
		return VersionRow{}, fmt.Errorf("artifacts.repo: get version: %w", err)
	}
	return v, nil
}
