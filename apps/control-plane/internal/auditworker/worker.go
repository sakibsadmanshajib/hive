package auditworker

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/hivegpt/hive/apps/control-plane/internal/auditworker/sinks"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Sink = sinks.Sink

type Config struct {
	Pool         *pgxpool.Pool
	Sinks        []Sink
	MaxAttempts  int
	BackoffStart time.Duration
	BackoffMax   time.Duration
	PollInterval time.Duration
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

func (w *Worker) drainOnce(ctx context.Context) error {
	rows, err := w.cfg.Pool.Query(ctx, `
		SELECT id, audit_id, audit_ts, sink, attempts
		  FROM public.audit_outbox
		 WHERE delivered_at IS NULL
		 ORDER BY created_at
		 LIMIT 50`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type job struct {
		id       int64
		auditID  int64
		auditTS  time.Time
		sink     string
		attempts int
	}
	jobs := make([]job, 0, 50)
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.id, &j.auditID, &j.auditTS, &j.sink, &j.attempts); err != nil {
			return err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, j := range jobs {
		sink, ok := w.bySink[j.sink]
		if !ok {
			continue
		}

		payload, err := w.loadPayload(ctx, j.auditID, j.auditTS)
		if err != nil {
			w.handleFailure(ctx, j, err)
			continue
		}
		if err := sink.Send(ctx, payload); err != nil {
			w.handleFailure(ctx, j, err)
			continue
		}
		_, _ = w.cfg.Pool.Exec(ctx,
			`UPDATE public.audit_outbox SET delivered_at=now() WHERE id=$1`,
			j.id,
		)
	}
	return nil
}

func (w *Worker) loadPayload(ctx context.Context, auditID int64, auditTS time.Time) (map[string]any, error) {
	var raw []byte
	err := w.cfg.Pool.QueryRow(ctx, `
		SELECT row_to_json(a)
		  FROM public.audit_log a
		 WHERE id=$1 AND ts=$2`,
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

func (w *Worker) handleFailure(ctx context.Context, j struct {
	id       int64
	auditID  int64
	auditTS  time.Time
	sink     string
	attempts int
}, err error) {
	nextAttempts := j.attempts + 1
	if nextAttempts >= w.cfg.MaxAttempts {
		w.toDLQ(ctx, j.id, nextAttempts, err.Error())
		return
	}
	w.markFailed(ctx, j.id, nextAttempts, err.Error())
}

func (w *Worker) markFailed(ctx context.Context, id int64, attempts int, msg string) {
	_, _ = w.cfg.Pool.Exec(ctx,
		`UPDATE public.audit_outbox SET attempts=$1, last_error=$2 WHERE id=$3`,
		attempts,
		trimError(msg),
		id,
	)
}

func (w *Worker) toDLQ(ctx context.Context, id int64, attempts int, msg string) {
	_, _ = w.cfg.Pool.Exec(ctx, `
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
	)
}

func trimError(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > 2048 {
		return msg[:2048]
	}
	return msg
}
