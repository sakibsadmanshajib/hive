package filestore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilestoreSchemaLivesInSupabaseMigration(t *testing.T) {
	source := readRepoFile(t, "apps/control-plane/internal/filestore/repository.go")

	if strings.Contains(source, "ensureSchema") {
		t.Fatal("NewRepository must not call ensureSchema; filestore schema belongs in Supabase migrations")
	}
	if strings.Contains(source, "CREATE TABLE") {
		t.Fatal("repository.go must not create tables at runtime; filestore schema belongs in Supabase migrations")
	}

	migration := filestoreSchemaMigration(t)
	for _, table := range []string{
		"create table public.files",
		"create table public.uploads",
		"create table public.upload_parts",
		"create table public.batches",
	} {
		if !strings.Contains(strings.ToLower(migration), table) {
			t.Fatalf("filestore schema migration must contain %s", table)
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
		"create table public.files",
		"create table public.uploads",
		"create table public.upload_parts",
		"create table public.batches",
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
