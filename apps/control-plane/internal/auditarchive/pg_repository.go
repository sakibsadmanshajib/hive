package auditarchive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgRepository implements Repository against public.audit_log and
// public.audit_cold_archive_manifest.
type PgRepository struct {
	pool *pgxpool.Pool
}

// NewPgRepository returns a PgRepository backed by pool.
func NewPgRepository(pool *pgxpool.Pool) *PgRepository {
	return &PgRepository{pool: pool}
}

// FetchTenants returns the distinct set of tenant_ids present in audit_log.
func (r *PgRepository) FetchTenants(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT tenant_id FROM public.audit_log WHERE tenant_id IS NOT NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("auditarchive pg: fetch tenants: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("auditarchive pg: scan tenant: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// FetchOlderThan returns all audit_log rows for tenantID with ts < cutoff,
// ordered by seq ASC so the JSONL preserves chain order.
func (r *PgRepository) FetchOlderThan(ctx context.Context, cutoff time.Time, tenantID uuid.UUID) ([]AuditRow, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, seq, tenant_id, actor_id, actor_type, action,
		       resource_type, resource_id, severity, before_json, after_json,
		       request_id, source_ip, user_agent, jwt_claims_digest,
		       deploy_sha, env, prev_hash, row_hash, ts
		  FROM public.audit_log
		 WHERE tenant_id = $1 AND ts < $2
		 ORDER BY seq ASC
	`, tenantID, cutoff)
	if err != nil {
		return nil, fmt.Errorf("auditarchive pg: fetch older than: %w", err)
	}
	defer rows.Close()

	var out []AuditRow
	for rows.Next() {
		var (
			row             AuditRow
			actorID         sql.NullString
			actorType       sql.NullString
			resourceType    sql.NullString
			resourceID      sql.NullString
			requestID       sql.NullString
			sourceIP        sql.NullString
			userAgent       sql.NullString
			jwtClaimsDigest sql.NullString
			deploySHA       sql.NullString
			env             sql.NullString
			beforeJSON      []byte
			afterJSON       []byte
		)
		if err := rows.Scan(
			&row.ID, &row.Seq, &row.TenantID,
			&actorID, &actorType, &row.Action,
			&resourceType, &resourceID, &row.Severity,
			&beforeJSON, &afterJSON,
			&requestID, &sourceIP, &userAgent, &jwtClaimsDigest,
			&deploySHA, &env,
			&row.PrevHash, &row.RowHash, &row.TS,
		); err != nil {
			return nil, fmt.Errorf("auditarchive pg: scan row: %w", err)
		}
		row.ActorID = actorID.String
		row.ActorType = actorType.String
		row.ResourceType = resourceType.String
		row.ResourceID = resourceID.String
		row.RequestID = requestID.String
		row.SourceIP = sourceIP.String
		row.UserAgent = userAgent.String
		row.JWTClaimsDigest = jwtClaimsDigest.String
		row.DeploySHA = deploySHA.String
		row.Env = env.String
		if len(beforeJSON) > 0 {
			row.BeforeJSON = json.RawMessage(beforeJSON)
		}
		if len(afterJSON) > 0 {
			row.AfterJSON = json.RawMessage(afterJSON)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// ManifestExists reports whether a manifest entry for (tenantID, month) already exists.
func (r *PgRepository) ManifestExists(ctx context.Context, tenantID uuid.UUID, month time.Time) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM public.audit_cold_archive_manifest
			 WHERE tenant_id = $1 AND partition_month = $2
		)
	`, tenantID, month).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("auditarchive pg: manifest exists: %w", err)
	}
	return exists, nil
}

// InsertManifest inserts a new manifest entry. The unique index on
// (tenant_id, partition_month) causes a duplicate to fail loudly rather than
// silently overwrite, preserving immutability.
func (r *PgRepository) InsertManifest(ctx context.Context, entry ManifestEntry) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.audit_cold_archive_manifest
		  (id, tenant_id, partition_month, object_key, sha256_hash,
		   row_count, first_seq, last_seq, archived_at, purge_after)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`,
		entry.ID, entry.TenantID, entry.PartitionMonth, entry.ObjectKey, entry.SHA256Hash,
		entry.RowCount, entry.FirstSeq, entry.LastSeq, entry.ArchivedAt, entry.PurgeAfter,
	)
	if err != nil {
		return fmt.Errorf("auditarchive pg: insert manifest: %w", err)
	}
	return nil
}

// DeleteArchived removes audit_log rows for the given (tenantID, month) after
// the manifest has been safely written. Returns the number of rows deleted.
func (r *PgRepository) DeleteArchived(ctx context.Context, tenantID uuid.UUID, month time.Time) (int64, error) {
	monthEnd := month.AddDate(0, 1, 0)
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM public.audit_log
		 WHERE tenant_id = $1 AND ts >= $2 AND ts < $3
	`, tenantID, month, monthEnd)
	if err != nil {
		return 0, fmt.Errorf("auditarchive pg: delete archived: %w", err)
	}
	return tag.RowsAffected(), nil
}

// FetchExpiredManifests returns manifest entries whose purge_after is in the past.
func (r *PgRepository) FetchExpiredManifests(ctx context.Context, now time.Time) ([]ManifestEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, tenant_id, partition_month, object_key, sha256_hash,
		       row_count, first_seq, last_seq, archived_at, purge_after
		  FROM public.audit_cold_archive_manifest
		 WHERE purge_after <= $1
		 ORDER BY purge_after ASC
	`, now)
	if err != nil {
		return nil, fmt.Errorf("auditarchive pg: fetch expired: %w", err)
	}
	defer rows.Close()
	var out []ManifestEntry
	for rows.Next() {
		var m ManifestEntry
		if err := rows.Scan(
			&m.ID, &m.TenantID, &m.PartitionMonth, &m.ObjectKey, &m.SHA256Hash,
			&m.RowCount, &m.FirstSeq, &m.LastSeq, &m.ArchivedAt, &m.PurgeAfter,
		); err != nil {
			return nil, fmt.Errorf("auditarchive pg: scan manifest: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteManifest removes a manifest entry by ID after its cold object has been purged.
func (r *PgRepository) DeleteManifest(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		DELETE FROM public.audit_cold_archive_manifest WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("auditarchive pg: delete manifest %s: %w", id, err)
	}
	return nil
}
