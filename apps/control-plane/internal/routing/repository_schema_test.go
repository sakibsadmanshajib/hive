package routing

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var providerCapabilitiesMediaColumns = []string{
	"supports_image_generation",
	"supports_image_edit",
	"supports_tts",
	"supports_stt",
	"supports_batch",
}

func TestRoutingRepositoryDoesNotRunCapabilityDDL(t *testing.T) {
	source := readRepoFile(t, "apps/control-plane/internal/routing/repository.go")

	if strings.Contains(source, "ensureCapabilityColumns") {
		t.Fatal("repository.go must not call or define ensureCapabilityColumns; provider_capabilities schema belongs in migrations")
	}
	if strings.Contains(source, "ALTER TABLE") {
		t.Fatal("repository.go must not run ALTER TABLE at runtime; provider_capabilities schema belongs in migrations")
	}
}

func TestProviderCapabilitiesMigrationAddsMediaColumns(t *testing.T) {
	migration := providerCapabilitiesMediaMigration(t)

	for _, column := range providerCapabilitiesMediaColumns {
		if !strings.Contains(migration, column) {
			t.Fatalf("provider_capabilities migration must include %s", column)
		}
	}
	if strings.Contains(strings.ToLower(migration), "alter table route_capabilities") {
		t.Fatal("provider_capabilities media migration must not alter table route_capabilities")
	}
}

func TestProviderCapabilitiesMigrationBackfillsMediaAndBatchRoutes(t *testing.T) {
	migration := providerCapabilitiesMediaMigration(t)

	if !strings.Contains(strings.ToLower(migration), "where route_id = 'route-openrouter-auto'") {
		t.Fatal("provider_capabilities migration must backfill route-openrouter-auto")
	}

	assignments := []string{
		"supports_image_generation",
		"supports_tts",
		"supports_stt",
		"supports_batch",
	}
	for _, column := range assignments {
		pattern := regexp.MustCompile(`(?is)` + regexp.QuoteMeta(column) + `\s*=\s*true`)
		if !pattern.MatchString(migration) {
			t.Fatalf("provider_capabilities migration must backfill %s = true for route-openrouter-auto", column)
		}
	}
}

func TestListRouteCandidatesSelectsMediaColumns(t *testing.T) {
	source := readRepoFile(t, "apps/control-plane/internal/routing/repository.go")

	for _, column := range providerCapabilitiesMediaColumns {
		want := "c." + column
		if !strings.Contains(source, want) {
			t.Fatalf("ListRouteCandidates must select %s", want)
		}
	}
}

func providerCapabilitiesMediaMigration(t *testing.T) string {
	t.Helper()

	root := repoRoot(t)
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
		lower := strings.ToLower(text)
		if !strings.Contains(lower, "alter table public.provider_capabilities") {
			continue
		}

		hasAllColumns := true
		for _, column := range providerCapabilitiesMediaColumns {
			if !strings.Contains(text, column) {
				hasAllColumns = false
				break
			}
		}
		if hasAllColumns {
			return text
		}
	}

	t.Fatal("expected a migration altering public.provider_capabilities with media columns: supports_image_generation, supports_image_edit, supports_tts, supports_stt, supports_batch")
	return ""
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
