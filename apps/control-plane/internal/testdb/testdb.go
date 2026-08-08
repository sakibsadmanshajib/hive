// Package testdb resolves the live-Postgres DSN that the database-backed
// suites in this module run against, and decides what happens when that DSN
// is absent.
//
// It exists because "absent" used to mean t.Skip everywhere, and a skipped
// test is indistinguishable from a passing one inside a green check. Twelve
// packages in this module were gated on HIVE_TEST_DB_URL while no workflow
// step that ran them ever exported it (issues #659 and #708), so their
// database assertions had never executed once. Centralising the gate means
// the rule "a missing DSN in CI is a red build, not a quiet skip" is stated
// once and cannot drift back package by package.
package testdb

import (
	"os"
	"strings"
	"testing"
)

// EnvVar is the environment variable every database-backed suite in this
// module reads.
const EnvVar = "HIVE_TEST_DB_URL"

// DSN returns the live-Postgres DSN for a database-backed test.
//
// Outside CI an unset DSN skips, so the suite stays runnable on a developer
// box with no Postgres. Inside CI an unset DSN is fatal: the workflow is
// supposed to export it, and a silent skip there is the exact failure mode
// that let these suites sit dark.
//
// The testing.Short() carve-out is load-bearing. The go-tests job runs
// `go test -short ./...` first, before the bootstrap step that creates the
// database and exports the DSN, and that step compiles these packages too.
// Without the carve-out every database-backed package would fail that
// earlier step. With it, "the RLS step forgot the DSN" (a real bug) stays
// distinguishable from "this is the -short step, which never has it"
// (expected).
func DSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(EnvVar)
	if dsn == "" {
		if os.Getenv("CI") != "" && !testing.Short() {
			t.Fatalf("%s not set in CI; this suite must not silently skip (issues #659, #708)", EnvVar)
		}
		t.Skipf("%s not set", EnvVar)
	}
	return dsn
}

// RequireTestDSN is DSN plus the "this really is a scratch database" guard
// used by every suite that seeds or truncates shared tables. A DSN with no
// "test" marker is refused outright rather than skipped: pointing a
// destructive suite at a real database is a mistake worth failing on, in CI
// and on a developer box alike.
func RequireTestDSN(t *testing.T) string {
	t.Helper()
	dsn := DSN(t)
	if !strings.Contains(strings.ToLower(dsn), "test") {
		t.Fatalf("refusing to run: %s must point at a test database (DSN missing 'test' marker)", EnvVar)
	}
	return dsn
}
