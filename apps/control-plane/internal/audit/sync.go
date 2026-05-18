package audit

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	canonical "github.com/sakibsadmanshajib/hive/packages/audit-canonical"
)

type WriterConfig struct {
	DeploySHA string
	Env       string
}

type SyncWriter struct {
	pool *pgxpool.Pool
	cfg  WriterConfig
}

func NewSyncWriter(pool *pgxpool.Pool, cfg WriterConfig) *SyncWriter {
	return &SyncWriter{pool: pool, cfg: cfg}
}

// Write inserts a single audit row and chains it off the last row in
// the same tenant's chain under SERIALIZABLE isolation.
//
// The chain semantics are scoped per tenant: prev_hash is the row_hash
// of the most recent row IN THE SAME TENANT, not the most recent row
// in the partition. The previous implementation read the partition-
// global MAX(seq) and prev_hash without a tenant filter, which linked
// rows from different tenants into the same chain and made the
// verifier flag every cross-tenant boundary as a chain break.
//
// seq remains partition-global so the UNIQUE(seq) per-partition index
// catches duplicate writes; the chain integrity is per-tenant via
// prev_hash. Both writers (this and edge-api chat/audit) agree on this
// model.
//
// Cross-partition stitching: if no row exists in the current month for
// this tenant, the query falls back to the most recent row for the
// same tenant ACROSS ALL PARTITIONS. The verifier mirrors this so the
// first row of each new month for a tenant links to that tenant's
// last row from the previous month rather than the all-zero sentinel.
// Without this, a tampered or deleted row on a month boundary would
// be either falsely flagged or silently accepted depending on which
// side it landed on.
func (w *SyncWriter) Write(ctx context.Context, e Event) error {
	if e.Action == "" {
		return errors.New("audit: action required")
	}
	if e.Severity == "" {
		return errors.New("audit: severity required")
	}

	before, err := stableJSON(e.Before)
	if err != nil {
		return fmt.Errorf("audit: marshal before: %w", err)
	}
	after, err := stableJSON(e.After)
	if err != nil {
		return fmt.Errorf("audit: marshal after: %w", err)
	}

	tx, err := w.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("audit: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Truncate to microseconds so the canonical hash, the timestamp
	// string embedded in the hash, and the Postgres timestamptz column
	// all carry exactly the same value. RFC3339Nano + clock_timestamp()
	// down-rounding was the original source of the canonical-hash
	// divergence the post-merge review flagged.
	ts := canonical.TruncateTS(time.Now())
	monthStart := time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	tenantArg := nullableUUID(e.TenantID)

	// Partition-wide advisory lock — serialises every writer in the
	// month so the partition-global MAX(seq) read + INSERT cannot
	// race. The audit_log_YYYY_MM tables enforce UNIQUE(seq) per
	// partition, so without this lock two concurrent writers could
	// both pick seq = N+1 and one would crash on duplicate-key. The
	// per-tenant chain is enforced separately by the prev_hash read
	// below.
	if _, err := tx.Exec(
		ctx,
		`SELECT pg_advisory_xact_lock(extract(epoch from date_trunc('month', $1::timestamptz))::int)`,
		ts,
	); err != nil {
		return fmt.Errorf("audit: advisory lock: %w", err)
	}

	var maxSeq int64
	var prevHash []byte
	err = tx.QueryRow(ctx, `
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
		 WHERE ts >= $2 AND ts < $3
	`, tenantArg, monthStart, monthEnd).Scan(&maxSeq, &prevHash)
	if err != nil {
		return fmt.Errorf("audit: read prev hash: %w", err)
	}

	canon, err := canonicalize(e, w.cfg.DeploySHA, w.cfg.Env, ts, before, after)
	if err != nil {
		return fmt.Errorf("audit: canonicalize: %w", err)
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
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
	`,
		nullableUUID(e.TenantID), nullableUUID(e.Actor.ID), string(e.Actor.Type),
		e.Action, nullableString(e.ResourceType), nullableString(e.ResourceID),
		string(e.Severity), before, after, nullableUUID(e.RequestID),
		nullableString(e.SourceIP), nullableString(e.UserAgent),
		nullableString(e.JWTClaimsDigest), w.cfg.DeploySHA, w.cfg.Env,
		ts, maxSeq+1, prevHash, rowHash,
	)
	if err != nil {
		return fmt.Errorf("audit: insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("audit: commit: %w", err)
	}
	return nil
}
