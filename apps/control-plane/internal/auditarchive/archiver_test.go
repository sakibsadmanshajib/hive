package auditarchive_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
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
	// onInsert, if set, runs synchronously right after a successful (non-duplicate)
	// InsertManifest. Tests use this instead of a fixed sleep to learn deterministically
	// when a background RunCron pass has produced its first manifest entry.
	onInsert func()
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

func (r *fakeRepo) InsertManifest(ctx context.Context, entry auditarchive.ManifestEntry) (bool, error) {
	for _, m := range r.manifest {
		if m.TenantID == entry.TenantID && m.PartitionMonth.Equal(entry.PartitionMonth) {
			return false, nil // duplicate; matches ON CONFLICT DO NOTHING
		}
	}
	r.manifest = append(r.manifest, entry)
	if r.onInsert != nil {
		r.onInsert()
	}
	return true, nil
}

// DeleteArchived removes only the rows whose IDs match the archived set.
func (r *fakeRepo) DeleteArchived(ctx context.Context, ids []int64) (int64, error) {
	idSet := make(map[int64]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	remaining := make([]auditarchive.AuditRow, 0, len(r.rows))
	var count int64
	for _, row := range r.rows {
		if idSet[row.ID] {
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

// fakeStore implements auditarchive.ObjectStore using an in-memory map.
// Put returns the exact byte count written (matching the updated interface).
type fakeStore struct {
	objects map[string][]byte
	deleted []string
	// failPut causes Put to return an error and write fewer bytes than given,
	// simulating a truncated/failed upload for P1 verification tests.
	failPut bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: map[string][]byte{}}
}

func (s *fakeStore) Put(ctx context.Context, key string, r io.Reader, size int64) (int64, error) {
	if s.failPut {
		// Simulate a partial write: drain the reader but report 0 bytes written.
		_, _ = io.ReadAll(r)
		return 0, errors.New("fakeStore: simulated upload failure")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	if int64(len(data)) != size {
		return 0, fmt.Errorf("fakeStore: caller-supplied size %d does not match actual bytes read %d", size, len(data))
	}
	s.objects[key] = data
	return int64(len(data)), nil
}

func (s *fakeStore) Delete(ctx context.Context, key string) error {
	s.deleted = append(s.deleted, key)
	delete(s.objects, key)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// makeRows builds n rows starting at ID/Seq = startID.
// Use distinct startIDs when combining rows from multiple calls so ID-based
// delete in the fake repository does not collide across groups.
func makeRows(tenantID uuid.UUID, ts time.Time, n int, startID int64) []auditarchive.AuditRow {
	rows := make([]auditarchive.AuditRow, n)
	for i := range rows {
		rows[i] = auditarchive.AuditRow{
			ID:       startID + int64(i),
			Seq:      startID + int64(i),
			TenantID: tenantID,
			Action:   "TEST_ACTION",
			Severity: "INFO",
			TS:       ts,
		}
	}
	return rows
}

func newArchiver(repo auditarchive.Repository, store auditarchive.ObjectStore) *auditarchive.Archiver {
	return auditarchive.New(auditarchive.Config{
		HotRetentionDays:  90,
		RetentionYears:    10,
		Repo:              repo,
		Store:             store,
		ColdStorageBucket: "hive-audit-cold",
	})
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestArchiveSelectsOldRowsOnly: rows newer than cutoff must not be archived.
func TestArchiveSelectsOldRowsOnly(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)
	fresh := now.AddDate(0, 0, -10)

	repo := &fakeRepo{
		rows: append(makeRows(tenantID, old, 3, 1), makeRows(tenantID, fresh, 2, 100)...),
	}
	store := newFakeStore()
	arch := newArchiver(repo, store)

	n, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 3 {
		t.Errorf("archived %d rows, want 3", n)
	}
	if len(repo.rows) != 2 {
		t.Errorf("repo has %d rows after archive, want 2 (fresh only)", len(repo.rows))
	}
}

// TestBoundaryMonthNoUnarchivedDelete is the P0 regression test.
//
// Setup: two rows share the same calendar month.
//
//	oldRow.TS = cutoff - 10 days  (fetched and archived)
//	newRow.TS = cutoff + 1 day    (never fetched; must survive the delete pass)
//
// now is a fixed, synthetic instant (not time.Now()) chosen so cutoff-10d and
// cutoff+1d always land in the same calendar month regardless of which day
// the suite runs on: a wall-clock now could push the two dates across a
// month boundary depending on where in the month the test happened to run,
// silently changing what the test exercises without a skip window or retry.
func TestBoundaryMonthNoUnarchivedDelete(t *testing.T) {
	tenantID := uuid.New()
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -90)

	oldTS := cutoff.AddDate(0, 0, -10)
	newTS := cutoff.AddDate(0, 0, 1)
	if oldTS.Month() != newTS.Month() || oldTS.Year() != newTS.Year() {
		t.Fatalf("test fixture invariant broken: oldTS %v and newTS %v must share a calendar month", oldTS, newTS)
	}

	oldRow := auditarchive.AuditRow{ID: 1, Seq: 1, TenantID: tenantID, Action: "A", Severity: "INFO", TS: oldTS}
	newRow := auditarchive.AuditRow{ID: 2, Seq: 2, TenantID: tenantID, Action: "B", Severity: "INFO", TS: newTS}

	repo := &fakeRepo{rows: []auditarchive.AuditRow{oldRow, newRow}}
	store := newFakeStore()
	arch := newArchiver(repo, store)

	_, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// newRow must still be in the hot table: it was not fetched (ts >= cutoff)
	// and must not have been deleted by the cutoff-bounded DELETE.
	found := false
	for _, r := range repo.rows {
		if r.ID == newRow.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("P0: boundary-month row with ts >= cutoff was deleted without being archived")
	}
	// oldRow must have been archived and deleted.
	for _, r := range repo.rows {
		if r.ID == oldRow.ID {
			t.Error("P0: old row that was archived is still present in hot table")
		}
	}
}

// TestArchiveWritesCompressedJSONL: verifies the cold object is valid gzip JSONL.
func TestArchiveWritesCompressedJSONL(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 5, 1)}
	store := newFakeStore()
	arch := newArchiver(repo, store)

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
	old := now.AddDate(0, 0, -100)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 4, 1)}
	store := newFakeStore()
	arch := newArchiver(repo, store)

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
	expectedPurge := m.ArchivedAt.AddDate(10, 0, 0)
	diff := m.PurgeAfter.Sub(expectedPurge)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("purge_after %v, want ~%v", m.PurgeAfter, expectedPurge)
	}

	// The manifest's digest must match the actual bytes archived to storage,
	// not just be present and 32 bytes long -- a wrong hash would silently
	// break chain re-verification without ever failing this test otherwise.
	archived, ok := store.objects[m.ObjectKey]
	if !ok {
		t.Fatalf("no stored object found for manifest object_key %q", m.ObjectKey)
	}
	wantSum := sha256.Sum256(archived)
	if !bytes.Equal(m.SHA256Hash, wantSum[:]) {
		t.Errorf("manifest SHA256Hash %x does not match sha256 of archived bytes %x", m.SHA256Hash, wantSum)
	}
}

// TestArchiveIdempotent: re-running on already-archived month is a no-op.
func TestArchiveIdempotent(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 3, 1)}
	store := newFakeStore()
	arch := newArchiver(repo, store)

	n1, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("first RunOnce: %v", err)
	}

	// Restore rows to simulate they still exist; manifest already present.
	repo.rows = makeRows(tenantID, old, 3, 1)

	n2, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second run archived %d rows, want 0 (idempotent)", n2)
	}
	_ = n1
}

// TestArchiveChainIntegrityPreserved: manifest written before hot rows deleted.
func TestArchiveChainIntegrityPreserved(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 6, 1)}
	store := newFakeStore()
	arch := newArchiver(repo, store)

	if _, err := arch.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(repo.manifest) == 0 {
		t.Fatal("manifest not written before hot rows deleted")
	}
	archMonth := time.Date(old.Year(), old.Month(), 1, 0, 0, 0, 0, time.UTC)
	for _, row := range repo.rows {
		rowMonth := time.Date(row.TS.Year(), row.TS.Month(), 1, 0, 0, 0, 0, time.UTC)
		if row.TenantID == tenantID && rowMonth.Equal(archMonth) {
			t.Errorf("hot row %d still present after archive", row.ID)
		}
	}
}

// TestFailedWriteBlocksDelete is the P1 regression test.
// If Put returns an error, DeleteArchived must not be called.
func TestFailedWriteBlocksDelete(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 3, 1)}
	store := newFakeStore()
	store.failPut = true // simulate upload failure
	arch := newArchiver(repo, store)

	_, err := arch.RunOnce(context.Background(), now)
	if err == nil {
		t.Fatal("P1: expected RunOnce to return error on failed Put, got nil")
	}

	// Hot rows must still be present; nothing deleted.
	if len(repo.rows) != 3 {
		t.Errorf("P1: %d rows remain, want 3 (delete must not run after failed write)", len(repo.rows))
	}
	// No manifest entry must have been written.
	if len(repo.manifest) != 0 {
		t.Errorf("P1: manifest has %d entries, want 0 after failed write", len(repo.manifest))
	}
}

// TestPurgeExpiredColdObjects: objects past purge_after are removed from store and manifest.
func TestPurgeExpiredColdObjects(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 2, 1)}
	store := newFakeStore()
	arch := newArchiver(repo, store)

	if _, err := arch.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
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
	fresh := now.AddDate(0, 0, -5)

	repo := &fakeRepo{rows: makeRows(tenantID, fresh, 10, 1)}
	store := newFakeStore()
	arch := newArchiver(repo, store)

	n, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != 0 {
		t.Errorf("archived %d rows, want 0", n)
	}
	if len(store.objects) != 0 || len(repo.manifest) != 0 {
		t.Errorf("store/manifest non-empty when no rows qualify")
	}
}

// TestCronRun: smoke test the scheduler (fires immediately at startup).
//
// Waits on a channel closed by fakeRepo.onInsert instead of a fixed sleep:
// a fixed sleep either wastes time when the pass is fast or flakes under
// slow/loaded CI when the pass is slow. The channel close happens inside the
// archiver goroutine and is only ever observed after being received here, so
// the wait is both deterministic and race-free.
func TestCronRun(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)

	repo := &fakeRepo{rows: makeRows(tenantID, old, 2, 1)}
	firstPass := make(chan struct{})
	var once sync.Once
	repo.onInsert = func() { once.Do(func() { close(firstPass) }) }
	store := newFakeStore()
	arch := newArchiver(repo, store)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	doneCh := make(chan error, 1)
	go func() {
		doneCh <- arch.RunCron(ctx, time.Hour) // long interval; first pass fires immediately
	}()

	select {
	case <-firstPass:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for cron's immediate first pass to write a manifest entry")
	}
	cancel()
	<-doneCh

	if len(repo.manifest) == 0 {
		t.Error("cron did not produce manifest entry on immediate first pass")
	}
}

// TestLateInsertNotDeleted is the exact-IDs regression test.
// A row inserted into the same (tenant, month) window AFTER the fetch snapshot
// must NOT be deleted, because it was never exported to cold storage.
func TestLateInsertNotDeleted(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)

	// Start with 2 rows that will be fetched and archived.
	fetchedRows := makeRows(tenantID, old, 2, 1)
	repo := &fakeRepo{rows: fetchedRows}
	store := newFakeStore()

	// lateInsertID is the ID of a row inserted into the repo AFTER the
	// archiver's fetch but before the delete. We simulate this by injecting
	// it directly into the fakeRepo after the fetch is captured.
	// The cleanest way: use a custom store whose Put injects the late row.
	const lateInsertID int64 = 999
	lateRow := auditarchive.AuditRow{
		ID: lateInsertID, Seq: 999, TenantID: tenantID,
		Action: "LATE_INSERT", Severity: "INFO", TS: old,
	}

	injectOnce := false
	injector := &injectStore{
		inner: store,
		onPut: func() {
			if !injectOnce {
				injectOnce = true
				repo.rows = append(repo.rows, lateRow)
			}
		},
	}

	arch := newArchiver(repo, injector)
	_, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// lateRow was NOT in the fetched set, so it must still be in the hot table.
	found := false
	for _, r := range repo.rows {
		if r.ID == lateInsertID {
			found = true
			break
		}
	}
	if !found {
		t.Error("late-insert row was deleted even though it was never archived (exact-ID delete regression)")
	}
}

// TestGroupByMonthUsesUTCNotLocalTime is the deferred #261 regression test:
// a row whose TS carries a non-UTC Location must be bucketed by its UTC
// calendar month, not the month implied by the timestamp's own Location, so
// partitioning is deterministic on hosts not set to UTC.
func TestGroupByMonthUsesUTCNotLocalTime(t *testing.T) {
	tenantID := uuid.New()
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	// UTC-6: local wall-clock Jan 31 23:00 is Feb 1 05:00 in UTC. A grouping
	// bug that reads Year()/Month() off the row's own Location buckets this
	// as January; the UTC-correct bucket is February.
	tz := time.FixedZone("UTC-6", -6*60*60)
	localTS := time.Date(2026, 1, 31, 23, 0, 0, 0, tz)

	row := auditarchive.AuditRow{
		ID: 1, Seq: 1, TenantID: tenantID, Action: "A", Severity: "INFO", TS: localTS,
	}
	repo := &fakeRepo{rows: []auditarchive.AuditRow{row}}
	store := newFakeStore()
	arch := newArchiver(repo, store)

	if _, err := arch.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(repo.manifest) != 1 {
		t.Fatalf("got %d manifest entries, want 1", len(repo.manifest))
	}

	wantMonth := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	gotMonth := repo.manifest[0].PartitionMonth
	if !gotMonth.Equal(wantMonth) {
		t.Errorf("partition_month = %v, want %v (UTC month of %v)", gotMonth, wantMonth, localTS)
	}
}

// TestLargePartitionStillArchivesAllRows: exceeding MaxRowsPerPartition only
// logs an operator warning, it must not truncate or otherwise change what
// gets archived.
func TestLargePartitionStillArchivesAllRows(t *testing.T) {
	tenantID := uuid.New()
	now := time.Now().UTC()
	old := now.AddDate(0, 0, -100)

	const rowCount = 10
	repo := &fakeRepo{rows: makeRows(tenantID, old, rowCount, 1)}
	store := newFakeStore()
	arch := auditarchive.New(auditarchive.Config{
		HotRetentionDays:    90,
		RetentionYears:      10,
		Repo:                repo,
		Store:               store,
		ColdStorageBucket:   "hive-audit-cold",
		MaxRowsPerPartition: 1, // force every row over threshold
	})

	n, err := arch.RunOnce(context.Background(), now)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if n != rowCount {
		t.Errorf("archived %d rows, want %d (threshold must warn, not truncate)", n, rowCount)
	}
	if len(repo.manifest) != 1 || repo.manifest[0].RowCount != rowCount {
		t.Errorf("manifest row_count = %+v, want single entry with RowCount %d", repo.manifest, rowCount)
	}
}

// injectStore wraps fakeStore and calls onPut after each Put to simulate
// a concurrent insert between fetch and delete.
type injectStore struct {
	inner *fakeStore
	onPut func()
}

func (s *injectStore) Put(ctx context.Context, key string, r io.Reader, size int64) (int64, error) {
	n, err := s.inner.Put(ctx, key, r, size)
	if err == nil {
		s.onPut()
	}
	return n, err
}

func (s *injectStore) Delete(ctx context.Context, key string) error {
	return s.inner.Delete(ctx, key)
}
