package audit

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	canonical "github.com/sakibsadmanshajib/hive/packages/audit-canonical"
)

// maxSerializationRetries bounds the SERIALIZABLE retry loop so a
// pathological hot-spot cannot live-lock a writer. Three attempts is
// the conventional default — Postgres rarely needs more than one
// retry to break a serialisation conflict.
const maxSerializationRetries = 3

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
// Chain semantics are per tenant — prev_hash links the most recent
// row IN THE SAME TENANT regardless of month. seq is partition-
// global; UNIQUE(seq) per-partition catches concurrent same-month
// duplicates. The advisory lock on a per-month bigint key serialises
// writers in the same month so the MAX(seq) read + INSERT is atomic.
//
// Cross-partition stitching: the prev_hash lookup falls back across
// all partitions when no row exists in the current month for this
// tenant. The lookup orders by (ts DESC, seq DESC, id DESC) — NOT
// raw seq — because seq counters restart per partition: June seq=100
// is chronologically before July seq=1, but `ORDER BY seq DESC`
// would return June's row as "latest" and break the July chain.
// `ts` is the canonical ordering of when a row was committed.
//
// SERIALIZABLE + retry: under contention Postgres can abort COMMIT
// with serialisation_failure (40001). We retry the entire transaction
// up to maxSerializationRetries because each attempt is short and
// retrying with a fresh `ts` keeps the row attributable to its true
// commit time, not its first attempt. As of #1182 the advisory lock
// is held from before BeginTx through the explicit unlock (see
// writeOnce), so two SyncWriter.Write calls racing each other no
// longer produce 40001 at all — the retry loop is defense-in-depth
// against a conflict from something else entirely serializable on
// public.audit_log, not the writer's own concurrency.
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

	for attempt := 0; ; attempt++ {
		err := w.writeOnce(ctx, e, before, after)
		if err == nil {
			return nil
		}
		if !isSerializationFailure(err) || attempt >= maxSerializationRetries {
			return err
		}
	}
}

func (w *SyncWriter) writeOnce(ctx context.Context, e Event, before, after []byte) error {
	// Truncate to microseconds so the canonical hash, the canonical
	// timestamp string, and the value Postgres stores are identical.
	// Re-captured per attempt so a retried row carries its actual
	// commit time, not the first attempt's stale time.
	ts := canonical.TruncateTS(time.Now())
	monthStart := time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	// Lock key is the month's epoch as bigint (not ::int, which is
	// 32-bit and would silently overflow in January 2038), computed
	// once here in Go and reused for both lock and unlock below.
	lockKey := monthStart.Unix()

	conn, err := w.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("audit: acquire conn: %w", err)
	}
	defer conn.Release()

	// Session-scoped advisory lock, acquired BEFORE the SERIALIZABLE
	// transaction opens, held until the explicit unlock below.
	//
	// Postgres takes a SERIALIZABLE transaction's snapshot at the
	// first statement inside it, not at BEGIN. The previous version of
	// this writer took the lock with pg_advisory_xact_lock as that
	// first statement: every concurrent writer's snapshot was formed
	// the instant it started waiting on the lock, not the instant it
	// actually got its turn, so each one committed against a MAX(seq)
	// read that was already stale by the time it ran — the textbook
	// SSI write-skew shape, and exactly why 6 of 10 fully-simultaneous
	// writers failed with SQLSTATE 40001 (#1182). Acquiring the lock
	// on the plain session first, before BEGIN, means the transaction
	// below can only take its snapshot once this writer already has
	// exclusive possession of the month, so nothing downstream of
	// BEGIN can observe a stale one.
	//
	// Session- not transaction-scoped deliberately: it must survive
	// past BeginTx to Commit. If the connection drops mid-hold,
	// Postgres releases the session lock when the backend terminates,
	// so the failure mode is one orphaned physical connection (which
	// the pool detects and evicts on the next health check), never a
	// permanently stuck lock. Same acquire/exec/defer-release shape as
	// accounting.PgxAccountLocker (internal/accounting/pglock.go); no
	// in-process gate is needed here the way that locker needs one,
	// because this critical section never asks the pool for a second
	// connection while holding the first.
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("audit: advisory lock: %w", err)
	}
	defer func() {
		// Background context: unlock must run even if ctx was cancelled.
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey)
	}()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("audit: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tenantArg := nullableUUID(e.TenantID)

	var maxSeq int64
	var prevHash []byte
	err = tx.QueryRow(ctx, `
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

// isSerializationFailure reports whether err is a Postgres
// serialisation_failure (40001), the only error class we retry. Any
// other SQLSTATE is a real bug or hard schema violation and must
// surface to the caller.
func isSerializationFailure(err error) bool {
	var pgErr interface{ SQLState() string }
	if errors.As(err, &pgErr) {
		return pgErr.SQLState() == pgerrcode.SerializationFailure
	}
	return false
}
