package auditworker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker/sinks"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fanoutParallelism caps the per-batch goroutine pool that dispatches
// outbox rows to sinks. With LeaseTTL of 2 minutes and a per-sink 5 s
// timeout, processing 50 rows serially could exceed the lease window
// (50×5s = 250s ≫ 120s). Concurrent fanout keeps total wall-clock
// well under the TTL even at worst case.
const fanoutParallelism = 16

type Sink = sinks.Sink

type Config struct {
	Pool         *pgxpool.Pool
	Sinks        []Sink
	MaxAttempts  int
	BackoffStart time.Duration
	BackoffMax   time.Duration
	PollInterval time.Duration
	// LeaseTTL controls how long a claimed row is reserved for the worker
	// that picked it. After the TTL elapses without a delivery or failure
	// update, the row becomes claimable again so a crashed worker does not
	// pin its in-flight rows indefinitely.
	LeaseTTL time.Duration
	// WorkerID is recorded in audit_outbox.claimed_by for traceability.
	// Defaults to hostname-pid.
	WorkerID string
}

type Worker struct {
	cfg    Config
	bySink map[string]Sink
}

func New(cfg Config) *Worker {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 250 * time.Millisecond
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 8
	}
	if cfg.BackoffStart == 0 {
		cfg.BackoffStart = time.Second
	}
	if cfg.BackoffMax == 0 {
		cfg.BackoffMax = 5 * time.Minute
	}
	if cfg.LeaseTTL == 0 {
		cfg.LeaseTTL = 2 * time.Minute
	}
	if cfg.WorkerID == "" {
		host, _ := os.Hostname()
		cfg.WorkerID = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	bySink := make(map[string]Sink, len(cfg.Sinks))
	for _, sink := range cfg.Sinks {
		bySink[sink.Name()] = sink
	}
	return &Worker{cfg: cfg, bySink: bySink}
}

func (w *Worker) Run(ctx context.Context) {
	tick := time.NewTicker(w.cfg.PollInterval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := w.drainOnce(ctx); err != nil {
				slog.Warn("auditworker drain error", "err", err)
			}
		}
	}
}

type outboxJob struct {
	id       int64
	auditID  int64
	auditTS  time.Time
	sink     string
	attempts int
}

// drainOnce claims a batch of eligible outbox rows atomically using
// `FOR UPDATE SKIP LOCKED` plus a persistent claimed_at lease, then
// dispatches them concurrently to their sinks. Concurrent fanout is
// essential: with the previous serial loop a batch of 50 × 5 s per
// sink (250 s) easily exceeded the 2-min lease TTL and other workers
// would reclaim mid-batch, producing duplicate deliveries.
//
// The lease interval is passed via `make_interval(secs => $1::numeric)`
// instead of `LeaseTTL.String()` because Go's Duration.String() emits
// values like "2m0s" or "1h30m" that Postgres ::interval does not
// always parse cleanly across versions. make_interval is a typed
// constructor with no format-string surprises.
func (w *Worker) drainOnce(ctx context.Context) error {
	rows, err := w.cfg.Pool.Query(ctx, `
		WITH eligible AS (
			SELECT id
			  FROM public.audit_outbox
			 WHERE delivered_at IS NULL
			   AND (next_retry_at IS NULL OR next_retry_at <= now())
			   AND (claimed_at   IS NULL OR claimed_at + make_interval(secs => $1::numeric) <= now())
			 ORDER BY next_retry_at NULLS FIRST, created_at
			 LIMIT 50
			 FOR UPDATE SKIP LOCKED
		)
		UPDATE public.audit_outbox o
		   SET claimed_at = now(),
		       claimed_by = $2
		  FROM eligible
		 WHERE o.id = eligible.id
		RETURNING o.id, o.audit_id, o.audit_ts, o.sink, o.attempts`,
		w.cfg.LeaseTTL.Seconds(),
		w.cfg.WorkerID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	jobs := make([]outboxJob, 0, 50)
	for rows.Next() {
		var j outboxJob
		if err := rows.Scan(&j.id, &j.auditID, &j.auditTS, &j.sink, &j.attempts); err != nil {
			return err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Bounded concurrent fanout. A semaphore channel of capacity
	// fanoutParallelism keeps memory + connection pressure predictable
	// while letting slow sinks not block fast ones.
	sem := make(chan struct{}, fanoutParallelism)
	var wg sync.WaitGroup
	for _, j := range jobs {
		j := j
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			w.processJob(ctx, j)
		}()
	}
	wg.Wait()
	return nil
}

// processJob handles a single claimed outbox row end-to-end. Errors
// are routed through handleFailure (markFailed or toDLQ) so a sink
// outage retries with exponential backoff and the row eventually
// settles to DLQ rather than pinning audit_outbox forever.
func (w *Worker) processJob(ctx context.Context, j outboxJob) {
	sink, ok := w.bySink[j.sink]
	if !ok {
		slog.Warn("auditworker unknown sink — routing to DLQ on max attempts",
			"sink", j.sink, "outbox_id", j.id)
		w.handleFailure(ctx, j, errSinkNotConfigured)
		return
	}
	payload, err := w.loadPayload(ctx, j.auditID, j.auditTS)
	if err != nil {
		w.handleFailure(ctx, j, err)
		return
	}
	if err := sink.Send(ctx, payload); err != nil {
		w.handleFailure(ctx, j, err)
		return
	}
	if err := w.markDelivered(ctx, j.id); err != nil {
		// The sink already accepted the event but we failed to record
		// delivery — the lease will expire and another worker will
		// retry, producing a duplicate. Log loudly so operators can
		// catch a runaway Postgres before duplicate volume grows.
		slog.Error("auditworker mark-delivered failed; row may be redelivered after lease",
			"err", err, "outbox_id", j.id, "sink", j.sink)
	}
}

// errSinkNotConfigured is set on audit_outbox.last_error when an enqueued
// row references a sink name that is no longer present in the worker's
// configured sink set. The row is retried up to MaxAttempts and then moved
// to the DLQ — never silently dropped.
type sinkNotConfiguredError struct{}

func (sinkNotConfiguredError) Error() string { return "sink not configured" }

var errSinkNotConfigured = sinkNotConfiguredError{}

func (w *Worker) loadPayload(ctx context.Context, auditID int64, auditTS time.Time) (map[string]any, error) {
	// Explicit column projection. The integrity-chain columns row_hash and
	// prev_hash and the fingerprintable jwt_claims_digest must never leave
	// Postgres for third-party sinks (ELK, Datadog, Splunk, Langfuse,
	// Sentry, Loki). Tamper-evidence depends on the chain remaining
	// internal-only.
	var raw []byte
	err := w.cfg.Pool.QueryRow(ctx, `
		SELECT json_build_object(
		         'id',            a.id,
		         'ts',            a.ts,
		         'tenant_id',     a.tenant_id,
		         'actor_id',      a.actor_id,
		         'actor_type',    a.actor_type,
		         'action',        a.action,
		         'resource_type', a.resource_type,
		         'resource_id',   a.resource_id,
		         'severity',      a.severity,
		         'before_json',   a.before_json,
		         'after_json',    a.after_json,
		         'request_id',    a.request_id,
		         'source_ip',     a.source_ip,
		         'user_agent',    a.user_agent,
		         'deploy_sha',    a.deploy_sha,
		         'env',           a.env,
		         'seq',           a.seq
		       )
		  FROM public.audit_log a
		 WHERE a.id=$1 AND a.ts=$2`,
		auditID,
		auditTS,
	).Scan(&raw)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (w *Worker) markDelivered(ctx context.Context, id int64) error {
	_, err := w.cfg.Pool.Exec(ctx,
		`UPDATE public.audit_outbox
		    SET delivered_at = now(),
		        claimed_at   = NULL,
		        claimed_by   = NULL
		  WHERE id = $1`,
		id,
	)
	return err
}

func (w *Worker) handleFailure(ctx context.Context, j outboxJob, err error) {
	nextAttempts := j.attempts + 1
	if nextAttempts >= w.cfg.MaxAttempts {
		w.toDLQ(ctx, j.id, nextAttempts, err.Error())
		return
	}
	delay := w.backoff(nextAttempts)
	w.markFailed(ctx, j.id, nextAttempts, err.Error(), delay)
}

// backoff returns the delay until the next retry attempt for a row that has
// just failed for the Nth time. The first failure waits BackoffStart and
// each subsequent failure doubles up to BackoffMax. This keeps transient
// sink outages out of DLQ until at least
// BackoffStart * (2^(MaxAttempts-1)) of cumulative wall-clock time.
func (w *Worker) backoff(attempts int) time.Duration {
	d := w.cfg.BackoffStart
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= w.cfg.BackoffMax {
			return w.cfg.BackoffMax
		}
	}
	return d
}

func (w *Worker) markFailed(ctx context.Context, id int64, attempts int, msg string, delay time.Duration) {
	if _, err := w.cfg.Pool.Exec(ctx,
		`UPDATE public.audit_outbox
		    SET attempts      = $1,
		        last_error    = $2,
		        next_retry_at = now() + make_interval(secs => $3::numeric),
		        claimed_at    = NULL,
		        claimed_by    = NULL
		  WHERE id = $4`,
		attempts,
		trimError(msg),
		delay.Seconds(),
		id,
	); err != nil {
		// Failure to record the failure: log and let the lease expire so
		// the row is retried (correct behaviour: another attempt is
		// preferable to a silent retry-counter desync).
		slog.Warn("auditworker mark-failed failed", "err", err, "outbox_id", id)
	}
}

func (w *Worker) toDLQ(ctx context.Context, id int64, attempts int, msg string) {
	if _, err := w.cfg.Pool.Exec(ctx, `
		WITH del AS (
			DELETE FROM public.audit_outbox
			 WHERE id=$1
		 RETURNING audit_id, audit_ts, sink, delivered_at, created_at
		)
		INSERT INTO public.audit_outbox_dlq
			(audit_id, audit_ts, sink, attempts, last_error, delivered_at, created_at)
		SELECT audit_id, audit_ts, sink, $2, $3, delivered_at, created_at
		  FROM del`,
		id,
		attempts,
		trimError(msg),
	); err != nil {
		slog.Warn("auditworker DLQ insert failed", "err", err, "outbox_id", id)
	}
}

func trimError(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > 2048 {
		return msg[:2048]
	}
	return msg
}
