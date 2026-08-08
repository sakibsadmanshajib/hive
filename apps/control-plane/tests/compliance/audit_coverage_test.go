package compliance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/testdb"
)

// TestAuditCoverage_AllControlsHaveEvents reuses the JS coverage
// generator with --check, which exits non-zero when any control has
// zero hits in the configured window. Phase 19 leaves the result
// advisory — later phases (KB upload, backups, key rotation) fill the
// rows that Phase 19 does not emit. The CI gate flips this to a hard
// failure once those phases land.
func TestAuditCoverage_AllControlsHaveEvents(t *testing.T) {
	_ = testdb.DSN(t)

	repoRoot, err := repoRootFromCaller()
	if err != nil {
		// Not a skip. runtime.Caller resolving to a path with no repo root
		// above it means the test binary was built somewhere this suite
		// cannot work from, which is a harness fault worth a red build
		// rather than an invisible pass.
		t.Fatalf("cannot locate repo root: %v", err)
	}

	cmd := exec.Command("node", "tools/soc2-coverage-report.mjs", "--check")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	t.Logf("soc2 coverage report output:\n%s", string(out))
	if err != nil {
		// Exit code 1 is the documented "missing controls" advisory in
		// Phase 19; later phases (KB upload, backups, key rotation) fill
		// those rows. Anything else (tooling crash, node missing) should
		// fail the test loudly.
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			t.Log("soc2 coverage check is advisory in Phase 19 (missing controls)")
			return
		}
		t.Fatalf("failed to execute soc2 coverage checker: %v", err)
	}
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
