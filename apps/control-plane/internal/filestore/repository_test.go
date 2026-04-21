package filestore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilestoreSchemaLivesInSupabaseMigration(t *testing.T) {
	migration := filestoreSchemaMigration(t)
	for _, table := range []string{
		"create table if not exists public.files",
		"create table if not exists public.uploads",
		"create table if not exists public.upload_parts",
		"create table if not exists public.batches",
	} {
		if !strings.Contains(strings.ToLower(migration), table) {
			t.Fatalf("filestore schema migration must contain %s", table)
		}
	}
}

func TestFilestoreRepositoryDoesNotRunSchemaDDL(t *testing.T) {
	source := readRepoFile(t, "apps/control-plane/internal/filestore/repository.go")

	if strings.Contains(source, "ensureSchema") {
		t.Fatal("NewRepository must not call ensureSchema; filestore schema belongs in Supabase migrations")
	}
	if strings.Contains(source, "CREATE TABLE") {
		t.Fatal("repository.go must not create tables at runtime; filestore schema belongs in Supabase migrations")
	}
}

func TestBatchAttributionMigrationAddsColumns(t *testing.T) {
	migration := batchAttributionMigration(t)

	for _, snippet := range []string{
		"add column if not exists api_key_id text",
		"add column if not exists model_alias text not null default ''",
		"add column if not exists estimated_credits bigint not null default 0",
		"add column if not exists actual_credits bigint not null default 0",
		"create index if not exists idx_batches_api_key_id",
		"create index if not exists idx_batches_model_alias",
	} {
		if !strings.Contains(strings.ToLower(migration), snippet) {
			t.Fatalf("batch attribution migration must contain %q", snippet)
		}
	}
}

func TestCreateBatchPersistsAttributionFields(t *testing.T) {
	repositorySource := createBatchSource(t, "apps/control-plane/internal/filestore/repository.go")
	for _, field := range []string{
		"api_key_id",
		"model_alias",
		"estimated_credits",
		"actual_credits",
		"reservation_id",
	} {
		if !strings.Contains(repositorySource, field) {
			t.Fatalf("repository CreateBatch must persist %s", field)
		}
	}

	serviceSource := createBatchSource(t, "apps/control-plane/internal/filestore/service.go")
	for _, field := range []string{
		"apiKeyID",
		"modelAlias",
		"estimatedCredits",
		"reservationID",
		"RequestCountsTotal",
		"model_alias is required",
		"estimated_credits must be greater than zero",
	} {
		if !strings.Contains(serviceSource, field) {
			t.Fatalf("service CreateBatch must carry %s", field)
		}
	}
}

func TestUpdateBatchStatusPersistsAllowedFields(t *testing.T) {
	body := updateBatchStatusSource(t)

	for _, field := range []string{
		"upstream_batch_id",
		"reservation_id",
		"output_file_id",
		"error_file_id",
		"request_counts_total",
		"request_counts_completed",
		"request_counts_failed",
		"actual_credits",
		"in_progress_at",
		"completed_at",
		"failed_at",
		"cancelled_at",
	} {
		if !strings.Contains(body, field) {
			t.Fatalf("UpdateBatchStatus must persist allowed field %s", field)
		}
	}
}

func TestNormalizeBatchUpdateValueConvertsTimestampAndIntegerFields(t *testing.T) {
	timestamp, err := normalizeBatchUpdateValue("completed_at", json.Number("1700000200"), batchUpdateTimestamp)
	if err != nil {
		t.Fatalf("normalize timestamp: %v", err)
	}
	if got, want := timestamp, time.Unix(1700000200, 0).UTC(); got != want {
		t.Fatalf("expected timestamp %v, got %v", want, got)
	}

	count, err := normalizeBatchUpdateValue("request_counts_completed", float64(2), batchUpdateInteger)
	if err != nil {
		t.Fatalf("normalize integer: %v", err)
	}
	if got, want := count, int64(2); got != want {
		t.Fatalf("expected integer %v, got %v", want, got)
	}
}

func TestNormalizeBatchUpdateValueRejectsInvalidTypes(t *testing.T) {
	if _, err := normalizeBatchUpdateValue("completed_at", "not-unix", batchUpdateTimestamp); err == nil || !strings.Contains(err.Error(), "invalid batch timestamp field") {
		t.Fatalf("expected invalid timestamp error, got %v", err)
	}

	if _, err := normalizeBatchUpdateValue("request_counts_failed", "one", batchUpdateInteger); err == nil || !strings.Contains(err.Error(), "invalid batch integer field") {
		t.Fatalf("expected invalid integer error, got %v", err)
	}
}

func filestoreSchemaMigration(t *testing.T) string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(repoRoot(t), "supabase/migrations/*.sql"))
	if err != nil {
		t.Fatalf("glob supabase migrations: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one Supabase migration")
	}

	requiredTables := []string{
		"create table if not exists public.files",
		"create table if not exists public.uploads",
		"create table if not exists public.upload_parts",
		"create table if not exists public.batches",
	}
	for _, path := range matches {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}

		text := string(body)
		lower := strings.ToLower(text)
		hasAllTables := true
		for _, table := range requiredTables {
			if !strings.Contains(lower, table) {
				hasAllTables = false
				break
			}
		}
		if hasAllTables {
			return text
		}
	}

	t.Fatal("expected a Supabase migration creating public.files, public.uploads, public.upload_parts, and public.batches; runtime ensureSchema must be removed")
	return ""
}

func updateBatchStatusSource(t *testing.T) string {
	t.Helper()

	source := readRepoFile(t, "apps/control-plane/internal/filestore/repository.go")
	start := strings.Index(source, "func (r *Repository) UpdateBatchStatus")
	if start == -1 {
		t.Fatal("UpdateBatchStatus function not found")
	}
	rest := source[start:]
	end := strings.Index(rest, "\n// --- Scanners ---")
	if end == -1 {
		t.Fatal("could not locate end of UpdateBatchStatus body")
	}
	return rest[:end]
}

func createBatchSource(t *testing.T, relativePath string) string {
	t.Helper()

	source := readRepoFile(t, relativePath)
	start := strings.Index(source, "CreateBatch")
	if start == -1 {
		t.Fatalf("CreateBatch not found in %s", relativePath)
	}

	return source[start:]
}

func batchAttributionMigration(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), "supabase/migrations/20260420_01_batch_accounting_attribution.sql"))
	if err != nil {
		t.Fatalf("read batch attribution migration: %v", err)
	}
	return string(body)
}

func readRepoFile(t *testing.T, relativePath string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatalf("read %s: %v", relativePath, err)
	}
	return string(body)
}

func repoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		if parent := filepath.Dir(dir); parent == dir {
			t.Fatalf("could not find repository root from %s", wd)
		}
	}
}
