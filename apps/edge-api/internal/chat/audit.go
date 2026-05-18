package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	canonical "github.com/sakibsadmanshajib/hive/packages/audit-canonical"
)

type auditEvent struct {
	TenantID     uuid.UUID
	ActorID      uuid.UUID
	Action       string
	Severity     string
	After        any
	RequestID    uuid.UUID
	UserAgent    string
	DeploySHA    string
	Environment  string
	ResourceType string
	ResourceID   string
}

// insertAuditEvent writes one audit row from the edge-api hot path,
// chained per-tenant. The chain semantics, canonical byte string, and
// timestamp format are owned by packages/audit-canonical so the
// control-plane SyncWriter and this writer cannot drift apart silently.
//
// Cross-partition stitching: if no row exists in the current month for
// this tenant, the prev_hash query falls back to the most recent row
// for the same tenant across ALL partitions. Same behaviour as
// SyncWriter; the verifier mirrors it.
func insertAuditEvent(ctx context.Context, pool *pgxpool.Pool, event auditEvent) error {
	if pool == nil {
		return nil
	}
	after, err := canonical.StableJSON(event.After)
	if err != nil {
		return err
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Truncate to microseconds so the canonical hash, the canonical
	// timestamp string, and the value Postgres stores are all the
	// same. RFC3339Nano emitted 9 digits while Postgres timestamptz
	// keeps 6 — the verifier re-canonicalisation would then mismatch.
	ts := canonical.TruncateTS(time.Now())
	monthStart := time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	tenantArg := nullableUUID(event.TenantID)

	// Partition-wide advisory lock serialises writers in the same
	// month so the partition-global MAX(seq) read + INSERT cannot
	// race. The audit_log_YYYY_MM tables enforce UNIQUE(seq) per
	// partition; without this lock concurrent writers could collide.
	// The per-tenant chain is enforced separately by the prev_hash
	// read below.
	if _, err := tx.Exec(
		ctx,
		`SELECT pg_advisory_xact_lock(extract(epoch from date_trunc('month', $1::timestamptz))::int)`,
		ts,
	); err != nil {
		return err
	}

	// seq is partition-global so UNIQUE(seq) per partition holds.
	// prev_hash is per-tenant — read the most recent row_hash for
	// THIS tenant, falling back across partition boundaries if the
	// current month has no rows for the tenant.
	var maxSeq int64
	var prevHash []byte
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(seq), 0),
		       COALESCE(
		         (SELECT row_hash
		            FROM public.audit_log
		           WHERE tenant_id IS NOT DISTINCT FROM $1
		           ORDER BY seq DESC
		           LIMIT 1),
		         decode(repeat('00', 32), 'hex')
		       )
		  FROM public.audit_log
		 WHERE ts >= $2 AND ts < $3`,
		tenantArg,
		monthStart,
		monthEnd,
	).Scan(&maxSeq, &prevHash); err != nil {
		return err
	}

	canon, err := canonicalAudit(event, ts, nil, after)
	if err != nil {
		return err
	}
	sum := sha256.New()
	sum.Write(prevHash)
	sum.Write(canon)
	rowHash := sum.Sum(nil)

	_, err = tx.Exec(ctx, `
		INSERT INTO public.audit_log
		  (tenant_id, actor_id, actor_type, action, resource_type, resource_id,
		   severity, before_json, after_json, request_id, source_ip, user_agent,
		   jwt_claims_digest, deploy_sha, env, ts, seq, prev_hash, row_hash)
		VALUES ($1,$2,'USER',$3,$4,$5,$6,$7,$8,$9,NULL,$10,NULL,$11,$12,$13,$14,$15,$16)`,
		nullableUUID(event.TenantID),
		nullableUUID(event.ActorID),
		event.Action,
		nullableString(event.ResourceType),
		nullableString(event.ResourceID),
		event.Severity,
		nil,
		after,
		nullableUUID(event.RequestID),
		nullableString(event.UserAgent),
		event.DeploySHA,
		event.Environment,
		ts,
		maxSeq+1,
		prevHash,
		rowHash,
	)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func canonicalAudit(event auditEvent, ts time.Time, before, after json.RawMessage) ([]byte, error) {
	row := canonical.Row{
		ActorType:  "USER",
		Action:     event.Action,
		Severity:   event.Severity,
		BeforeJSON: before,
		AfterJSON:  after,
		UserAgent:  event.UserAgent,
		DeploySHA:  event.DeploySHA,
		Env:        event.Environment,
		TS:         canonical.FormatTS(ts),
	}
	row.ResourceType = event.ResourceType
	row.ResourceID = event.ResourceID
	(canonical.Identity{
		TenantID:  event.TenantID,
		ActorID:   event.ActorID,
		RequestID: event.RequestID,
	}).Apply(&row)
	return row.Marshal()
}
