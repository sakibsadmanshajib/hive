package sandbox_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestVendoredApptainerWorkspace_PatchMarkersPresent guards against a future
// re-vendor of vendor/openhands silently dropping the HIVE PATCH applied to
// upstream's ApptainerWorkspace._start_container() (security spike #307):
// unconditional --pid/--containall and the Docker-socket bind-mount guard.
// apps/agent-engine's own Go launcher (BuildArgv) never calls this Python
// class directly and does not depend on this patch to be safe itself — this
// test exists because the vendored copy is defense in depth and an upstream
// sync is exactly the kind of change that could remove it unnoticed.
func TestVendoredApptainerWorkspace_PatchMarkersPresent(t *testing.T) {
	path := vendoredApptainerWorkspacePath(t)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vendored workspace.py: %v", err)
	}
	src := string(content)

	required := []string{
		`container_opts: list[str] = ["--pid", "--containall"]`,
		"docker.sock",
		"docker_host",
	}
	for _, marker := range required {
		if !strings.Contains(src, marker) {
			t.Fatalf("vendored ApptainerWorkspace is missing expected HIVE PATCH marker %q — re-apply the patch documented in vendor/openhands/VENDORING.md", marker)
		}
	}
}

// vendoredApptainerWorkspacePath resolves the path to the patched vendor
// file relative to this test file's own location, so it works regardless of
// the working directory `go test` is invoked from.
func vendoredApptainerWorkspacePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve test file path via runtime.Caller")
	}
	// this file: apps/agent-engine/internal/sandbox/vendor_patch_test.go
	// target:    vendor/openhands/openhands-workspace/openhands/workspace/apptainer/workspace.py
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	return filepath.Join(repoRoot, "vendor", "openhands", "openhands-workspace", "openhands", "workspace", "apptainer", "workspace.py")
}
