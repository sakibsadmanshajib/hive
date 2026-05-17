// Command gen-permissions emits a TypeScript permissions file from the Go
// authz registry. Run via `make gen-permissions` or:
//
//	go run ./apps/control-plane/cmd/gen-permissions [output-path]
//
// Default output: apps/web-console/lib/control-plane/permissions.generated.ts
// Idempotent: running twice produces zero diff.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/hivegpt/hive/apps/control-plane/internal/authz"
)

const header = "// AUTO-GENERATED — do not edit. Run `make gen-permissions` to regenerate.\n// Source: apps/control-plane/internal/authz/permissions.go\n\n"

func main() {
	out := "apps/web-console/lib/control-plane/permissions.generated.ts"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("export const PERMISSIONS = [\n")
	for _, p := range authz.AllPermissions() {
		fmt.Fprintf(&b, "  %q,\n", string(p))
	}
	b.WriteString("] as const;\n\nexport type Permission = typeof PERMISSIONS[number];\n")

	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
