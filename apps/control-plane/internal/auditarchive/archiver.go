// Package auditarchive implements the nightly audit retention cron.
//
// Lifecycle (PHIPA 10-year / Quebec Law 25):
//   - Hot retention: rows older than HotRetentionDays stay in audit_log for fast queries.
//   - Cold archive: rows past the hot window are exported as JSONL, gzip-compressed,
//     written to local object storage (zero external egress), and recorded in
//     audit_cold_archive_manifest. The manifest entry is written BEFORE hot rows
//     are deleted to preserve chain integrity.
//   - Purge: cold objects past purge_after (archived_at + RetentionYears) are deleted
//     from storage and the manifest.
package auditarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// AuditRow is a minimal projection of public.audit_log sufficient for JSONL export.
// All columns that matter for chain integrity and regulatory content are included.
type AuditRow struct {
	ID              int64           `json:"id"`
	Seq             int64           `json:"seq"`
	TenantID        uuid.UUID       `json:"tenant_id"`
	ActorID         string          `json:"actor_id,omitempty"`
	ActorType       string          `json:"actor_type,omitempty"`
	Action          string          `json:"action"`
	ResourceType    string          `json:"resource_type,omitempty"`
	ResourceID      string          `json:"resource_id,omitempty"`
	Severity        string          `json:"severity"`
	BeforeJSON      json.RawMessage `json:"before_json,omitempty"`
	AfterJSON       json.RawMessage `json:"after_json,omitempty"`
	RequestID       string          `json:"request_id,omitempty"`
	SourceIP        string          `json:"source_ip,omitempty"`
	UserAgent       string          `json:"user_agent,omitempty"`
	JWTClaimsDigest string          `json:"jwt_claims_digest,omitempty"`
	DeploySHA       string          `json:"deploy_sha,omitempty"`
	Env             string          `json:"env,omitempty"`
	PrevHash        []byte          `json:"prev_hash"`
	RowHash         []byte          `json:"row_hash"`
	TS              time.Time       `json:"ts"`
}

// ManifestEntry mirrors public.audit_cold_archive_manifest.
type ManifestEntry struct {
	ID             uuid.UUID `json:"id"`
	TenantID       uuid.UUID `json:"tenant_id"`
	PartitionMonth time.Time `json:"partition_month"`
	ObjectKey      string    `json:"object_key"`
	SHA256Hash     []byte    `json:"sha256_hash"`
	RowCount       int64     `json:"row_count"`
	FirstSeq       int64     `json:"first_seq"`
	LastSeq        int64     `json:"last_seq"`
	ArchivedAt     time.Time `json:"archived_at"`
	PurgeAfter     time.Time `json:"purge_after"`
}

// Repository abstracts all DB access so the archiver is testable without Postgres.
type Repository interface {
	FetchTenants(ctx context.Context) ([]uuid.UUID, error)
	FetchOlderThan(ctx context.Context, cutoff time.Time, tenantID uuid.UUID) ([]AuditRow, error)
	ManifestExists(ctx context.Context, tenantID uuid.UUID, month time.Time) (bool, error)
	InsertManifest(ctx context.Context, entry ManifestEntry) error
	DeleteArchived(ctx context.Context, tenantID uuid.UUID, month time.Time) (int64, error)
	FetchExpiredManifests(ctx context.Context, now time.Time) ([]ManifestEntry, error)
	DeleteManifest(ctx context.Context, id uuid.UUID) error
}

// ObjectStore abstracts local cold storage (Supabase Storage / local filesystem).
// Zero external egress: the backing store must be the box's own filesystem.
type ObjectStore interface {
	Put(ctx context.Context, key string, r io.Reader) error
	Delete(ctx context.Context, key string) error
}

// Config holds tunable parameters; all have sane defaults applied by New.
type Config struct {
	// HotRetentionDays: rows older than this are eligible for cold archive (default 90).
	HotRetentionDays int
	// RetentionYears: cold objects are kept for this many years (PHIPA default 10).
	RetentionYears int
	// ColdStorageBucket: bucket name (or path prefix) in the ObjectStore.
	ColdStorageBucket string
	Repo              Repository
	Store             ObjectStore
}

// Archiver runs the cold-archive lifecycle.
type Archiver struct {
	cfg Config
}

// New returns an Archiver with defaults applied.
func New(cfg Config) *Archiver {
	if cfg.HotRetentionDays <= 0 {
		cfg.HotRetentionDays = 90
	}
	if cfg.RetentionYears <= 0 {
		cfg.RetentionYears = 10
	}
	if cfg.ColdStorageBucket == "" {
		cfg.ColdStorageBucket = "hive-audit-cold"
	}
	return &Archiver{cfg: cfg}
}

// RunOnce executes one archive pass as of now. It returns the total number of
// audit rows moved to cold storage across all tenants.
//
// Order of operations per (tenant, month) partition:
//  1. Check manifest: skip if already archived (idempotent).
//  2. Fetch rows older than cutoff for tenant.
//  3. Group by month, build JSONL, gzip, compute SHA-256.
//  4. Write compressed object to cold storage.
//  5. Insert manifest entry (write-once; fails loudly on duplicate).
//  6. Delete hot rows for this (tenant, month).
//
// Step 5 before step 6 ensures the chain is never broken: if the process
// crashes after writing the manifest but before deleting hot rows, the next
// run detects ManifestExists and skips, leaving hot rows as a safe duplicate.
func (a *Archiver) RunOnce(ctx context.Context, now time.Time) (int, error) {
	cutoff := now.Add(-time.Duration(a.cfg.HotRetentionDays) * 24 * time.Hour)

	tenants, err := a.cfg.Repo.FetchTenants(ctx)
	if err != nil {
		return 0, fmt.Errorf("auditarchive: fetch tenants: %w", err)
	}

	total := 0
	for _, tenantID := range tenants {
		rows, err := a.cfg.Repo.FetchOlderThan(ctx, cutoff, tenantID)
		if err != nil {
			return total, fmt.Errorf("auditarchive: fetch rows tenant %s: %w", tenantID, err)
		}
		if len(rows) == 0 {
			continue
		}

		// Group rows by partition month.
		byMonth := groupByMonth(rows)
		for month, monthRows := range byMonth {
			exists, err := a.cfg.Repo.ManifestExists(ctx, tenantID, month)
			if err != nil {
				return total, fmt.Errorf("auditarchive: manifest check: %w", err)
			}
			if exists {
				slog.Info("auditarchive: partition already archived, skipping",
					"tenant_id", tenantID, "month", month.Format("2006-01"))
				continue
			}

			n, err := a.archivePartition(ctx, now, tenantID, month, monthRows)
			if err != nil {
				return total, err
			}
			total += n
		}
	}
	return total, nil
}

// archivePartition handles one (tenant, month) batch.
func (a *Archiver) archivePartition(
	ctx context.Context,
	now time.Time,
	tenantID uuid.UUID,
	month time.Time,
	rows []AuditRow,
) (int, error) {
	// Build JSONL in memory.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	enc := json.NewEncoder(gz)
	var firstSeq, lastSeq int64
	for i, row := range rows {
		if err := enc.Encode(row); err != nil {
			return 0, fmt.Errorf("auditarchive: encode row %d: %w", row.ID, err)
		}
		if i == 0 {
			firstSeq = row.Seq
		}
		lastSeq = row.Seq
	}
	if err := gz.Close(); err != nil {
		return 0, fmt.Errorf("auditarchive: gzip close: %w", err)
	}

	compressed := buf.Bytes()
	sum := sha256.Sum256(compressed)

	objectKey := fmt.Sprintf("%s/%s/%s.jsonl.gz",
		a.cfg.ColdStorageBucket,
		tenantID.String(),
		month.Format("2006-01"),
	)

	if err := a.cfg.Store.Put(ctx, objectKey, bytes.NewReader(compressed)); err != nil {
		return 0, fmt.Errorf("auditarchive: store put %s: %w", objectKey, err)
	}

	entry := ManifestEntry{
		ID:             uuid.New(),
		TenantID:       tenantID,
		PartitionMonth: month,
		ObjectKey:      objectKey,
		SHA256Hash:     sum[:],
		RowCount:       int64(len(rows)),
		FirstSeq:       firstSeq,
		LastSeq:        lastSeq,
		ArchivedAt:     now,
		PurgeAfter:     now.AddDate(a.cfg.RetentionYears, 0, 0),
	}
	if err := a.cfg.Repo.InsertManifest(ctx, entry); err != nil {
		return 0, fmt.Errorf("auditarchive: insert manifest: %w", err)
	}

	// Delete hot rows AFTER manifest is safely written.
	deleted, err := a.cfg.Repo.DeleteArchived(ctx, tenantID, month)
	if err != nil {
		return 0, fmt.Errorf("auditarchive: delete archived: %w", err)
	}

	slog.Info("auditarchive: partition archived",
		"tenant_id", tenantID,
		"month", month.Format("2006-01"),
		"rows", deleted,
		"object_key", objectKey,
	)
	return int(deleted), nil
}

// PurgeExpired deletes cold objects and manifest entries whose purge_after has passed.
// Returns the number of manifest entries purged.
func (a *Archiver) PurgeExpired(ctx context.Context, now time.Time) (int, error) {
	entries, err := a.cfg.Repo.FetchExpiredManifests(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("auditarchive: fetch expired manifests: %w", err)
	}
	purged := 0
	for _, m := range entries {
		if err := a.cfg.Store.Delete(ctx, m.ObjectKey); err != nil {
			return purged, fmt.Errorf("auditarchive: delete cold object %s: %w", m.ObjectKey, err)
		}
		if err := a.cfg.Repo.DeleteManifest(ctx, m.ID); err != nil {
			return purged, fmt.Errorf("auditarchive: delete manifest %s: %w", m.ID, err)
		}
		slog.Info("auditarchive: expired cold object purged",
			"manifest_id", m.ID,
			"tenant_id", m.TenantID,
			"object_key", m.ObjectKey,
		)
		purged++
	}
	return purged, nil
}

// RunCron runs the archiver on a ticker until ctx is cancelled.
// Fires immediately on first tick. Intended to be called in a goroutine.
func (a *Archiver) RunCron(ctx context.Context, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case t := <-ticker.C:
			now := t.UTC()
			n, err := a.RunOnce(ctx, now)
			if err != nil {
				slog.Error("auditarchive: cron run error", "err", err)
				continue
			}
			purged, err := a.PurgeExpired(ctx, now)
			if err != nil {
				slog.Error("auditarchive: purge error", "err", err)
				continue
			}
			if n > 0 || purged > 0 {
				slog.Info("auditarchive: cron complete", "archived_rows", n, "purged_manifests", purged)
			}
		}
	}
}

// groupByMonth partitions rows by their UTC year-month (normalised to the 1st at midnight).
func groupByMonth(rows []AuditRow) map[time.Time][]AuditRow {
	out := map[time.Time][]AuditRow{}
	for _, r := range rows {
		m := time.Date(r.TS.Year(), r.TS.Month(), 1, 0, 0, 0, 0, time.UTC)
		out[m] = append(out[m], r)
	}
	return out
}
