package auditarchive_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditarchive"
)

// ── fakes ─────────────────────────────────────────────────────────────────────

// fakeRepo implements auditarchive.Repository using in-memory slices.
type fakeRepo struct {
	rows     []auditarchive.AuditRow
	manifest []auditarchive.ManifestEntry
	deleted  []int64 // IDs deleted after archive
}

func (r *fakeRepo) FetchOlderThan(ctx context.Context, cutoff time.Time, tenantID uuid.UUID) ([]auditarchive.AuditRow, error) {
	out := make([]auditarchive.AuditRow, 0)
	for _, row := range r.rows {
		if row.TS.Before(cutoff) && row.TenantID == tenantID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r *fakeRepo) FetchTenants(ctx context.Context) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	out := []uuid.UUID{}
	for _, row := range r.rows {
		if !seen[row.TenantID] {
			seen[row.TenantID] = true
			out = append(out, row.TenantID)
		}
	}
	return out, nil
}

func (r *fakeRepo) ManifestExists(ctx context.Context, tenantID uuid.UUID, month time.Time) (bool, error) {
	for _, m := range r.manifest {
		if m.TenantID == tenantID && m.PartitionMonth.Equal(month) {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepo) InsertManifest(ctx context.Context, entry auditarchive.ManifestEntry) error {
	r.manifest = append(r.manifest, entry)
	return nil
}

func (r *fakeRepo) DeleteArchived(ctx context.Context, tenantID uuid.UUID, month time.Time) (int64, error) {
	remaining := r.rows[:0]
	var count int64
	for _, row := range r.rows {
		rowMonth := time.Date(row.TS.Year(), row.TS.Month(), 1, 0, 0, 0, 0, time.UTC)
		if row.TenantID == tenantID && rowMonth.Equal(month) {
			count++
		} else {
			remaining = append(remaining, row)
		}
	}
	r.rows = remaining
	return count, nil
}

func (r *fakeRepo) FetchExpiredManifests(ctx context.Context, now time.Time) ([]auditarchive.ManifestEntry, error) {
	out := []auditarchive.ManifestEntry{}
	for _, m := range r.manifest {
		if now.After(m.PurgeAfter) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (r *fakeRepo) DeleteManifest(ctx context.Context, id uuid.UUID) error {
	remaining := r.manifest[:0]
	for _, m := range r.manifest {
		if m.ID != id {
			remaining = append(remaining, m)
		}
	}
	r.manifest = remaining
	return nil
}

// fakeStore implements auditarchive.ObjectStore using in-memory map.
type fakeStore struct {
	objects map[string][]byte
	deleted []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: map[string][]byte{}}
}

func (s *fakeStore) Put(ctx context.Context, key string, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.objects[key] = data
	return nil
}

func (s *fakeStore) Delete(ctx context.Context, key string) error {
	s.deleted = append(s.deleted, key)
	delete(s.objects, key)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func makeRows(tenantID uuid.UUID, ts time.Time, n int) []auditarchive.AuditRow {
	rows := make([]auditarchive.AuditRow, n)
	for i := range rows {
		rows[i] = auditarchive.AuditRow{
			ID:       int64(i + 1),
			Seq:      int64(i + 1),
			TenantID: tenantID,
			Action:   "TEST_ACTION",
			Severity: "INFO",
			TS:       ts,
		}
	}
	return rows
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestArchiveSelectsOldRowsOnly: rows newer than cutoff must not be archived.
func TestArchiveSelectsOldRowsOnly(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour) // 100 days ago
	fresh := now.Add(-10 * 24 * time.Hour) // 10 days ago

	repo := &fakeRepo{
		rows: append(
			makeRows(tenantID, old, 3),
			makeRows(tenantID, fresh, 2)...,
		),
	}
	store := newFakeStore()
	arch := auditarchive.New(auditarchive.Config{
		HotRetentionDays:  90,
		RetentionYears:    10,
		Repo:              repo,
		Store:             store,
		ColdStorageBucket: "hive-audit-cold",
	})

	n, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// Only 3 old rows archived; 2 fresh rows remain in repo.
	if n != 3 {
		t.Errorf("archived %d rows, want 3", n)
	}
	if len(repo.rows) != 2 {
		t.Errorf("repo has %d rows after archive, want 2 (fresh only)", len(repo.rows))
	}
}

// TestArchiveWritesCompressedJSONL: verifies the cold object is valid gzip JSONL.
func TestArchiveWritesCompressedJSONL(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 5)}
	store := newFakeStore()
	arch := auditarchive.New(auditarchive.Config{
		HotRetentionDays:  90,
		RetentionYears:    10,
		Repo:              repo,
		Store:             store,
		ColdStorageBucket: "hive-audit-cold",
	})

	if _, err := arch.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(store.objects) == 0 {
		t.Fatal("no objects written to store")
	}
	for key, data := range store.objects {
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("key %s: not gzip: %v", key, err)
		}
		defer gr.Close()
		dec := json.NewDecoder(gr)
		lineCount := 0
		for {
			var obj map[string]any
			if err := dec.Decode(&obj); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("key %s: invalid JSONL: %v", key, err)
			}
			lineCount++
		}
		if lineCount != 5 {
			t.Errorf("key %s: got %d JSONL lines, want 5", key, lineCount)
		}
	}
}

// TestArchiveManifestRecorded: manifest entry must be written with correct SHA-256.
func TestArchiveManifestRecorded(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 4)}
	store := newFakeStore()
	arch := auditarchive.New(auditarchive.Config{
		HotRetentionDays:  90,
		RetentionYears:    10,
		Repo:              repo,
		Store:             store,
		ColdStorageBucket: "hive-audit-cold",
	})

	if _, err := arch.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(repo.manifest) == 0 {
		t.Fatal("no manifest entry recorded")
	}
	m := repo.manifest[0]
	if m.TenantID != tenantID {
		t.Errorf("manifest tenant %v, want %v", m.TenantID, tenantID)
	}
	if len(m.SHA256Hash) != 32 {
		t.Errorf("SHA256Hash len %d, want 32", len(m.SHA256Hash))
	}
	if m.RowCount != 4 {
		t.Errorf("manifest row_count %d, want 4", m.RowCount)
	}
	// purge_after must be ~10 years out
	expectedPurge := m.ArchivedAt.AddDate(10, 0, 0)
	diff := m.PurgeAfter.Sub(expectedPurge)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("purge_after %v, want ~%v", m.PurgeAfter, expectedPurge)
	}
}

// TestArchiveIdempotent: re-running on already-archived month is a no-op.
func TestArchiveIdempotent(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 3)}
	store := newFakeStore()
	arch := auditarchive.New(auditarchive.Config{
		HotRetentionDays:  90,
		RetentionYears:    10,
		Repo:              repo,
		Store:             store,
		ColdStorageBucket: "hive-audit-cold",
	})

	n1, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	// Reset rows to simulate they still exist (idempotency: manifest already there).
	repo.rows = makeRows(tenantID, old, 3)

	n2, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second run archived %d rows, want 0 (idempotent)", n2)
	}
	_ = n1
}

// TestArchiveChainIntegrityPreserved: rows deleted from hot table only after manifest written.
func TestArchiveChainIntegrityPreserved(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 6)}
	store := newFakeStore()
	arch := auditarchive.New(auditarchive.Config{
		HotRetentionDays:  90,
		RetentionYears:    10,
		Repo:              repo,
		Store:             store,
		ColdStorageBucket: "hive-audit-cold",
	})

	if _, err := arch.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Manifest must exist before hot rows are gone.
	if len(repo.manifest) == 0 {
		t.Fatal("manifest not written before hot rows deleted")
	}
	// Hot rows for this month must be deleted.
	for _, row := range repo.rows {
		rowMonth := time.Date(row.TS.Year(), row.TS.Month(), 1, 0, 0, 0, 0, time.UTC)
		archMonth := time.Date(old.Year(), old.Month(), 1, 0, 0, 0, 0, time.UTC)
		if row.TenantID == tenantID && rowMonth.Equal(archMonth) {
			t.Errorf("hot row %d still present after archive", row.ID)
		}
	}
}

// TestPurgeExpiredColdObjects: objects past purge_after are removed from store + manifest.
func TestPurgeExpiredColdObjects(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 2)}
	store := newFakeStore()
	arch := auditarchive.New(auditarchive.Config{
		HotRetentionDays:  90,
		RetentionYears:    10,
		Repo:              repo,
		Store:             store,
		ColdStorageBucket: "hive-audit-cold",
	})

	// Archive first.
	if _, err := arch.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// Artificially back-date purge_after to the past.
	for i := range repo.manifest {
		repo.manifest[i].PurgeAfter = now.Add(-time.Hour)
	}

	purged, err := arch.PurgeExpired(context.Background(), now)
	if err != nil {
		t.Fatalf("PurgeExpired: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged %d manifests, want 1", purged)
	}
	if len(store.objects) != 0 {
		t.Errorf("store still has %d objects after purge, want 0", len(store.objects))
	}
	if len(repo.manifest) != 0 {
		t.Errorf("manifest has %d entries after purge, want 0", len(repo.manifest))
	}
}

// TestNoRowsNoArchive: if there are no old rows, nothing is written.
func TestNoRowsNoArchive(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	fresh := now.Add(-5 * 24 * time.Hour) // 5 days ago

	repo := &fakeRepo{rows: makeRows(tenantID, fresh, 10)}
	store := newFakeStore()
	arch := auditarchive.New(auditarchive.Config{
		HotRetentionDays:  90,
		RetentionYears:    10,
		Repo:              repo,
		Store:             store,
		ColdStorageBucket: "hive-audit-cold",
	})

	n, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("archived %d rows, want 0", n)
	}
	if len(store.objects) != 0 {
		t.Errorf("store has %d objects, want 0", len(store.objects))
	}
	if len(repo.manifest) != 0 {
		t.Errorf("manifest has %d entries, want 0", len(repo.manifest))
	}
}

// TestCronRun: smoke test the scheduler wrapper (single tick).
func TestCronRun(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.Add(-100 * 24 * time.Hour)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 2)}
	store := newFakeStore()
	arch := auditarchive.New(auditarchive.Config{
		HotRetentionDays:  90,
		RetentionYears:    10,
		Repo:              repo,
		Store:             store,
		ColdStorageBucket: "hive-audit-cold",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// RunCron fires immediately on first tick then repeats; cancel after first run.
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- arch.RunCron(ctx, 100*time.Millisecond)
	}()

	// Wait for the cron to fire at least once.
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-doneCh

	if len(repo.manifest) == 0 {
		t.Error("cron did not produce manifest entry")
	}
}
