package apikeys

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Go default above is only half the surface. account_rate_policies and
// api_key_rate_policies both declare `requests_per_minute integer not null
// default 60` and `tokens_per_minute integer not null default 120000`
// (20260331_04_api_key_usage_and_limits.sql), so a row inserted without an
// explicit value is handed a limit by the schema itself, and the Go default
// is never consulted for it.
const noDefaultLimitsMigrationRelPath = "supabase/migrations/20260830_02_no_default_rate_limits.sql"

func noDefaultLimitsSQL(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRootForTest(t), filepath.FromSlash(noDefaultLimitsMigrationRelPath)))
	if err != nil {
		t.Fatalf("read %s: %v", noDefaultLimitsMigrationRelPath, err)
	}
	return strings.ToLower(string(body))
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.work above %s", wd)
		}
	}
}

func TestRatePolicyColumnDefaultsAreDropped(t *testing.T) {
	sql := noDefaultLimitsSQL(t)

	for _, table := range []string{"public.account_rate_policies", "public.api_key_rate_policies"} {
		for _, column := range []string{"requests_per_minute", "tokens_per_minute"} {
			want := "alter table " + table + " alter column " + column + " set default 0"
			if !strings.Contains(sql, want) {
				t.Errorf("%s does not contain %q; the schema default would keep imposing a limit nobody configured", noDefaultLimitsMigrationRelPath, want)
			}
		}
	}
}

// Existing rows are NOT touched, and that is a decision rather than an
// omission. UpdateLimits (repository.go) is the only writer of either table
// and always passes an explicit RPM and TPM from a console request; no
// migration inserts into them and account_rate_policies has no writer at all.
// So every row that exists was typed by an operator, and any backfill, even
// one fingerprinted on the old 60 / 120000 pair, erases an explicit limit.
//
// This test is the guard against a future "helpful" backfill being added back.
func TestNoBackfillErasesAnExplicitLimit(t *testing.T) {
	sql := noDefaultLimitsSQL(t)

	for _, table := range []string{"public.account_rate_policies", "public.api_key_rate_policies"} {
		if strings.Contains(sql, "update "+table) {
			t.Errorf("%s updates rows in %s; every row in that table was written explicitly by an operator through UpdateLimits, so a backfill erases a configured limit", noDefaultLimitsMigrationRelPath, table)
		}
		if strings.Contains(sql, "delete from "+table) {
			t.Errorf("%s deletes rows from %s", noDefaultLimitsMigrationRelPath, table)
		}
	}

	// The reasoning has to survive in the file, because the next reader's first
	// instinct will be that the migration forgot to backfill.
	if !strings.Contains(sql, "updatelimits") {
		t.Errorf("%s does not name UpdateLimits as the reason it backfills nothing; without that the omission reads as an oversight", noDefaultLimitsMigrationRelPath)
	}
}

// A migration that walked into the fraud windows or the free-token weight
// would be changing behaviour the directive did not ask for.
func TestNoDefaultLimitsMigrationTouchesNothingElse(t *testing.T) {
	sql := noDefaultLimitsSQL(t)

	for _, banned := range []string{
		"rolling_five_hour_limit",
		"weekly_limit",
		"free_token_weight_tenths",
		"drop table",
		"drop column",
	} {
		if strings.Contains(sql, banned) {
			t.Errorf("%s mentions %q; only the two per-minute defaults are in scope", noDefaultLimitsMigrationRelPath, banned)
		}
	}
}
