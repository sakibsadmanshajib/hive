package genexport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Documented defaults, referenced by TestConfigDefaults.
const (
	DefaultBatchSize    = 100
	DefaultPollInterval = 5 * time.Second
	defaultBackoffMax   = 5 * time.Minute
	defaultHTTPTimeout  = 10 * time.Second

	ingestionPath = "/api/public/ingestion"
)

// Cursor is the exporter's position in public.usage_events, ordered by
// (created_at, id). Keyset rather than offset, so it is stable under
// concurrent inserts.
type Cursor struct {
	CreatedAt time.Time
	EventID   string
}

// Row is one settled usage event with the attempt it belongs to.
type Row struct {
	Attempt AttemptRow
	Event   UsageEventRow
}

// RowSource is the read side, kept as an interface so the loop is testable
// without a database. The production implementation is pgxSource.
type RowSource interface {
	// Fetch returns up to limit settled rows above the stored cursor, in
	// cursor order.
	Fetch(ctx context.Context, limit int) ([]Row, error)
	// Advance moves the stored cursor. It is called only after the batch was
	// accepted.
	Advance(ctx context.Context, to Cursor) error
}

type Config struct {
	Pool *pgxpool.Pool
	// Host is the Langfuse base URL. Empty disables the exporter entirely:
	// it then reads no row and opens no connection.
	Host         string
	PublicKey    string
	SecretKey    string
	BatchSize    int
	PollInterval time.Duration
	BackoffMax   time.Duration
	HTTP         *http.Client
	// Source overrides the Postgres reader. Tests set it; production leaves
	// it nil and gets a pgxSource over Pool.
	Source RowSource
}

type Exporter struct {
	cfg     Config
	source  RowSource
	client  *http.Client
	enabled bool
}

// New builds an Exporter. It never returns nil and never panics on an empty
// configuration: an exporter with no host, or with no key pair to authenticate
// with, is simply disabled, and every one of its operations is a no-op.
func New(cfg Config) *Exporter {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = DefaultPollInterval
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = defaultBackoffMax
	}
	cfg.Host = strings.TrimRight(strings.TrimSpace(cfg.Host), "/")
	cfg.PublicKey = strings.TrimSpace(cfg.PublicKey)
	cfg.SecretKey = strings.TrimSpace(cfg.SecretKey)

	client := cfg.HTTP
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}

	source := cfg.Source
	if source == nil && cfg.Pool != nil {
		source = &pgxSource{pool: cfg.Pool}
	}

	// A host with no key pair cannot authenticate. Treating that as enabled
	// would post an unauthenticated batch on every tick forever, which is
	// noise rather than export, so it counts as off.
	enabled := cfg.Host != "" && cfg.PublicKey != "" && cfg.SecretKey != "" && source != nil

	return &Exporter{cfg: cfg, source: source, client: client, enabled: enabled}
}

// Enabled reports whether this exporter will do anything at all.
func (e *Exporter) Enabled() bool { return e.enabled }

// BatchSize is the resolved batch size, defaults applied.
func (e *Exporter) BatchSize() int { return e.cfg.BatchSize }

// PollInterval is the resolved poll interval, defaults applied.
func (e *Exporter) PollInterval() time.Duration { return e.cfg.PollInterval }

// Run drains in a loop until ctx is done. A disabled exporter returns at once
// rather than parking a goroutine on a ticker that can never do anything.
//
// The retry shape follows auditworker.Worker: a failure backs the next attempt
// off exponentially up to BackoffMax, and a success resets it. There is no DLQ
// here and there does not need to be one, because a failure leaves the cursor
// where it was and the same rows are simply read again.
func (e *Exporter) Run(ctx context.Context) {
	if !e.enabled {
		return
	}

	wait := e.cfg.PollInterval
	timer := time.NewTimer(wait)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		if _, err := e.DrainOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			wait *= 2
			if wait > e.cfg.BackoffMax {
				wait = e.cfg.BackoffMax
			}
			slog.Warn("genexport drain error; cursor unchanged, batch will be retried",
				"err", err, "retry_in", wait)
		} else {
			wait = e.cfg.PollInterval
		}
		timer.Reset(wait)
	}
}

// DrainOnce reads one batch of settled rows, posts it, and advances the cursor
// if and only if the endpoint accepted it. It returns the number of rows
// exported.
//
// The ordering is deliberate and is the whole durability argument: read, post,
// then advance. A crash or an outage anywhere before the advance leaves the
// cursor where it was, so the batch is read again and re-posted. Because the
// batch event ids are derived from the attempt id, that redelivery upserts the
// same rows rather than duplicating them.
func (e *Exporter) DrainOnce(ctx context.Context) (int, error) {
	if !e.enabled {
		return 0, nil
	}

	rows, err := e.source.Fetch(ctx, e.cfg.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("genexport: fetch: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	batch := make([]map[string]any, 0, len(rows)*2)
	for _, row := range rows {
		batch = append(batch, MapRow(row.Attempt, row.Event).IngestionBatch()...)
	}

	if err := e.post(ctx, batch); err != nil {
		return 0, err
	}

	last := rows[len(rows)-1].Event
	if err := e.source.Advance(ctx, Cursor{CreatedAt: last.CreatedAt, EventID: last.ID}); err != nil {
		// The batch is already accepted upstream. Not advancing means the
		// same rows are re-posted next tick, which upserts rather than
		// duplicates, so this is recoverable and is reported rather than
		// swallowed.
		return 0, fmt.Errorf("genexport: advance cursor: %w", err)
	}

	return len(rows), nil
}

func (e *Exporter) post(ctx context.Context, batch []map[string]any) error {
	payload, err := json.Marshal(map[string]any{"batch": batch})
	if err != nil {
		return fmt.Errorf("genexport: marshal batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.cfg.Host+ingestionPath, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("genexport: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(e.cfg.PublicKey, e.cfg.SecretKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("genexport: post batch: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Deliberately does not echo the response body into the error. A
		// Langfuse error can quote the batch back, and this error string ends
		// up in control-plane's logs.
		return fmt.Errorf("genexport: ingestion rejected the batch: status=%d", resp.StatusCode)
	}

	// Langfuse answers 207 for a partially accepted batch. The cursor still
	// advances, because a permanently rejected event would otherwise wedge the
	// whole export behind it forever, and a stall is a worse failure than a
	// dropped observability row. It is logged loudly instead, with ids only.
	if resp.StatusCode == http.StatusMultiStatus {
		logPartialRejection(body)
	}

	return nil
}

// logPartialRejection reports the ids Langfuse refused. Only ids and status
// codes are logged: the endpoint echoes submitted content back in its error
// payload, and none of that belongs in our logs.
func logPartialRejection(body []byte) {
	var parsed struct {
		Errors []struct {
			ID     string `json:"id"`
			Status int    `json:"status"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Errors) == 0 {
		return
	}
	ids := make([]string, 0, len(parsed.Errors))
	for _, e := range parsed.Errors {
		ids = append(ids, fmt.Sprintf("%s(status=%d)", e.ID, e.Status))
	}
	slog.Warn("genexport: ingestion rejected part of the batch; those generations are lost, the cursor still advanced",
		"rejected", len(ids), "ids", strings.Join(ids, ","))
}

// pgxSource is the production read side over the two accounting tables.
type pgxSource struct {
	pool *pgxpool.Pool
}

// ErrCursorMissing is returned when the single cursor row is absent, which
// means the migration has not been applied. It is an error rather than an
// implicit "start from the beginning" so the condition is loud: starting from
// the beginning would silently replay the entire usage history into Langfuse.
var ErrCursorMissing = errors.New("genexport: cursor row missing from public.generation_export_cursor (apply supabase/migrations/20260829_03_genexport_cursor.sql)")

// Every uuid column is cast to text in these queries so the scan targets are
// plain Go strings and no pgx uuid type registration is required.
func (s *pgxSource) Fetch(ctx context.Context, limit int) ([]Row, error) {
	var (
		cursorAt time.Time
		cursorID string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT last_created_at, last_event_id::text
		  FROM public.generation_export_cursor
		 WHERE id`).Scan(&cursorAt, &cursorID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrCursorMissing
	}
	if err != nil {
		return nil, fmt.Errorf("read cursor: %w", err)
	}

	// The projection is explicit and narrow on purpose. It selects no column
	// that could carry prompt text, completion text or a provider name, and
	// usage_events.provider_request_id is deliberately absent.
	rows, err := s.pool.Query(ctx, `
		SELECT ue.id::text, ue.event_type, ue.status, ue.endpoint, ue.model_alias,
		       ue.input_tokens, ue.output_tokens, ue.cache_read_tokens, ue.cache_write_tokens,
		       ue.hive_credit_delta, ue.error_code, ue.error_type, ue.created_at,
		       ra.id::text, ra.request_id, ra.attempt_number, ra.endpoint, ra.model_alias,
		       ra.user_id::text, ra.team_id::text, ra.api_key_id::text,
		       ra.started_at, ra.completed_at
		  FROM public.usage_events ue
		  JOIN public.request_attempts ra ON ra.id = ue.request_attempt_id
		 WHERE ue.event_type = ANY($1)
		   AND (ue.created_at, ue.id) > ($2, $3::uuid)
		 ORDER BY ue.created_at, ue.id
		 LIMIT $4`,
		TerminalEventTypes, cursorAt, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("select settled events: %w", err)
	}
	defer rows.Close()

	out := make([]Row, 0, limit)
	for rows.Next() {
		var r Row
		if err := rows.Scan(
			&r.Event.ID, &r.Event.EventType, &r.Event.Status, &r.Event.Endpoint, &r.Event.ModelAlias,
			&r.Event.InputTokens, &r.Event.OutputTokens, &r.Event.CacheReadTokens, &r.Event.CacheWriteTokens,
			&r.Event.HiveCreditDelta, &r.Event.ErrorCode, &r.Event.ErrorType, &r.Event.CreatedAt,
			&r.Attempt.ID, &r.Attempt.RequestID, &r.Attempt.AttemptNumber, &r.Attempt.Endpoint, &r.Attempt.ModelAlias,
			&r.Attempt.UserID, &r.Attempt.TeamID, &r.Attempt.APIKeyID,
			&r.Attempt.StartedAt, &r.Attempt.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan settled event: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate settled events: %w", err)
	}
	return out, nil
}

// Advance moves the cursor forward and only forward. The guard makes a
// concurrent second control-plane harmless: the later position wins and
// neither instance can drag the cursor backwards over rows already exported.
func (s *pgxSource) Advance(ctx context.Context, to Cursor) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE public.generation_export_cursor
		   SET last_created_at = $1,
		       last_event_id   = $2::uuid,
		       updated_at      = now()
		 WHERE id
		   AND (last_created_at, last_event_id) < ($1, $2::uuid)`,
		to.CreatedAt, to.EventID)
	if err != nil {
		return fmt.Errorf("update cursor: %w", err)
	}
	return nil
}
