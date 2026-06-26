package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	canonical "github.com/sakibsadmanshajib/hive/packages/audit-canonical"
)

const maxSerializationRetries = 3

// AuditEvent is the audit row shape. Exported so cross-package callers
// (e.g. the RAG handler) can construct events without a circular import.
type AuditEvent struct {
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

// auditEvent aliases AuditEvent so existing internal callers need no change.
type auditEvent = AuditEvent

// InsertAuditEvent writes one audit row from any edge-api handler.
// It uses the same chain writer as the chat hot path so all events
// land in the same durable store.
func InsertAuditEvent(ctx context.Context, pool *pgxpool.Pool, event AuditEvent) error {
	return insertAuditEvent(ctx, pool, event)
}

// insertAuditEvent writes one audit row, chained per-tenant. The chain
// semantics, canonical byte string, and timestamp format are owned by
// packages/audit-canonical so the control-plane SyncWriter and this
// writer cannot drift apart silently.
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
	for attempt := 0; ; attempt++ {
		err := insertAuditEventOnce(ctx, pool, event, after)
		if err == nil {
			return nil
		}
		if !isSerializationFailure(err) || attempt >= maxSerializationRetries {
			return err
		}
	}
}

func insertAuditEventOnce(ctx context.Context, pool *pgxpool.Pool, event auditEvent, after json.RawMessage) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Truncate to microseconds so the canonical hash, the timestamp
	// string in the hash, and the value Postgres stores all match.
	// Re-captured per attempt so a retried row's `ts` reflects its
	// real commit time.
	ts := canonical.TruncateTS(time.Now())

	// Advisory lock keyed on the month's epoch as bigint. ::int was
	// 32-bit and would overflow in 2038; ::bigint matches the
	// pg_advisory_xact_lock(bigint) overload so the namespace is
	// unambiguous.
	if _, err := tx.Exec(
		ctx,
		`SELECT pg_advisory_xact_lock(extract(epoch from date_trunc('month', $1::timestamptz))::bigint)`,
		ts,
	); err != nil {
		return err
	}

	monthStart := time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	tenantArg := nullableUUID(event.TenantID)

	// seq is partition-global (UNIQUE(seq) per partition); prev_hash
	// is per-tenant. The prev_hash fallback orders by ts DESC, seq
	// DESC, id DESC — NOT raw seq — because seq counters restart per
	// partition: a June seq=100 row is older than July seq=1 but
	// would be returned by `ORDER BY seq DESC`, breaking the chain.
	var maxSeq int64
	var prevHash []byte
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(seq), 0),
		       COALESCE(
		         (SELECT row_hash
		            FROM public.audit_log
		           WHERE tenant_id IS NOT DISTINCT FROM $1
		           ORDER BY ts DESC, seq DESC, id DESC
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

// isSerializationFailure reports whether err is a Postgres
// serialisation_failure (40001), the only error class we retry.
func isSerializationFailure(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == pgerrcode.SerializationFailure
	}
	return false
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
