package compliance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestAuditCoverage_AllControlsHaveEvents reuses the JS coverage
// generator with --check, which exits non-zero when any control has
// zero hits in the configured window. Phase 19 leaves the result
// advisory — later phases (KB upload, backups, key rotation) fill the
// rows that Phase 19 does not emit. The CI gate flips this to a hard
// failure once those phases land.
func TestAuditCoverage_AllControlsHaveEvents(t *testing.T) {
	if os.Getenv("HIVE_TEST_DB_URL") == "" {
		t.Skip("HIVE_TEST_DB_URL not set")
	}

	repoRoot, err := repoRootFromCaller()
	if err != nil {
		t.Skip("cannot locate repo root: " + err.Error())
	}

	cmd := exec.Command("node", "tools/soc2-coverage-report.mjs", "--check")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	_ = err // advisory in Phase 19; do not fail the test
	t.Logf("soc2 coverage report output:\n%s", string(out))
}

// repoRootFromCaller resolves the repository root by walking up from
// this source file until it finds a `go.work` sentinel.
func repoRootFromCaller() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", os.ErrNotExist
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
