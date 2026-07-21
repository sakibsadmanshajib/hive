package rag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRagEmbeddingConfigMigrationExists guards against the exact demo-blocking
// regression found 2026-07-21: the rag_embedding_config migration shipped in
// the repo but was never applied to a live Supabase project, and both
// provision.go (here) and edge-api/internal/rag/config.go query the table by
// this literal name. If a future migration renames or drops the table
// without updating both consumers, this test catches the drift statically
// (no live DB needed), the same way repository_schema_test.go guards
// provider_capabilities.
func TestRagEmbeddingConfigMigrationExists(t *testing.T) {
	migration := findRagEmbeddingConfigMigration(t)

	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS public.rag_embedding_config",
		"id             SMALLINT    PRIMARY KEY DEFAULT 1",
		"ON CONFLICT (id) DO NOTHING",
	} {
		if !strings.Contains(migration, want) {
			t.Fatalf("rag_embedding_config migration must contain %q (idempotent singleton config table)", want)
		}
	}
}

// TestProvisionQueriesRagEmbeddingConfigByLiteralName pins the table name
// both independent consumers of the singleton row query by, to the same
// literal the migration creates. edge-api/internal/rag/config.go queries this
// table independently of control-plane's provision.go (control-plane writes,
// edge-api only reads) -- a rename on one side without the other fails fast
// in CI here instead of only surfacing as a silent "relation does not exist"
// warning at boot.
func TestProvisionQueriesRagEmbeddingConfigByLiteralName(t *testing.T) {
	for _, path := range []string{
		"apps/control-plane/internal/rag/provision.go",
		"apps/edge-api/internal/rag/config.go",
	} {
		source := readRagRepoFile(t, path)
		if !strings.Contains(source, "FROM public.rag_embedding_config WHERE id = 1") {
			t.Fatalf("%s must read the singleton row from public.rag_embedding_config WHERE id = 1", path)
		}
	}
}

func findRagEmbeddingConfigMigration(t *testing.T) string {
	t.Helper()

	root := ragRepoRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "supabase/migrations/*.sql"))
	if err != nil {
		t.Fatalf("glob supabase migrations: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one Supabase migration")
	}

	for _, path := range matches {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		text := string(body)
		if strings.Contains(text, "CREATE TABLE IF NOT EXISTS public.rag_embedding_config") {
			return text
		}
	}

	t.Fatal("expected a migration creating public.rag_embedding_config")
	return ""
}

func readRagRepoFile(t *testing.T, relativePath string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(ragRepoRoot(t), filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatalf("read %s: %v", relativePath, err)
	}
	return string(body)
}

func ragRepoRoot(t *testing.T) string {
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
