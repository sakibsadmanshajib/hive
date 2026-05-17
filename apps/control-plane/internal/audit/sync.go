package audit

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

// Write inserts a single row and chains it off the last row in the same
// partition under SERIALIZABLE isolation.
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

	ts := time.Now().UTC()
	// Read MAX(seq) + last row_hash through the partitioned parent table
	// with a ts-range filter. Postgres routes the scan to the active
	// monthly partition automatically, so we no longer hard-fail with
	// `relation does not exist` when the daily partition cron has not
	// yet created next month's partition. The (tenant_id, ts DESC) and
	// per-partition seq indexes keep this O(1) inside the partition.
	monthStart := time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)

	var maxSeq int64
	var prevHash []byte
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(MAX(seq), 0),
		       COALESCE(
		         (SELECT row_hash
		            FROM public.audit_log
		           WHERE ts >= $1 AND ts < $2
		           ORDER BY seq DESC
		           LIMIT 1),
		         decode(repeat('00', 32), 'hex')
		       )
		  FROM public.audit_log
		 WHERE ts >= $1 AND ts < $2
	`, monthStart, monthEnd).Scan(&maxSeq, &prevHash)
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
