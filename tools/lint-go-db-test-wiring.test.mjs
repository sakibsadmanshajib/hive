// tools/lint-go-db-test-wiring.test.mjs
// Fixture test for the cross-leg attribution hole a second CodeRabbit pass
// found on this PR (tools/lint-go-db-test-wiring.mjs review thread, line 165):
// `resolveDirectories` stores every matrix leg's directory on every `go test`
// invocation found in a step, with no regard for a shell `if`/`else` inside
// that step's `run:` text that actually restricts one invocation to one leg.
// Two RLS branches that both name the same package pattern (the real ci.yml
// shape for `./internal/rag/...`) can therefore credit a leg whose branch
// never runs that command at all.
//
// This builds the smallest fixture that reproduces it: two Go modules, one
// workflow step with `if [ "${{ matrix.module }}" = "mod-a" ]` wrapping a
// single `go test` call that only mod-a's branch reaches, and no `else`. The
// real lint script runs as a subprocess against the fixture directory
// (dependency-free unit testing of the module is not practical here: unlike
// verify-spec-wiring.mjs, this script's whole body is the measurement, so a
// subprocess against a throwaway fixture tree is the boundary that does not
// require rewriting the parser to be pure).
//
// Run: node tools/lint-go-db-test-wiring.test.mjs

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(HERE, "..");
const LINT_SCRIPT = join(REPO_ROOT, "tools", "lint-go-db-test-wiring.mjs");

function write(root, relPath, content) {
  const full = join(root, relPath);
  mkdirSync(dirname(full), { recursive: true });
  writeFileSync(full, content);
}

function buildFixture() {
  const root = mkdtempSync(join(tmpdir(), "lint-go-db-test-wiring-fixture-"));

  write(root, "apps/mod-a/go.mod", "module mod-a\n\ngo 1.24\n");
  write(
    root,
    "apps/mod-a/internal/rag/x_test.go",
    `package rag

import (
	"os"
	"testing"
)

func TestNeedsDSN(t *testing.T) {
	if os.Getenv("HIVE_TEST_DB_URL") == "" {
		t.Skip("no db")
	}
}
`,
  );

  write(root, "apps/mod-b/go.mod", "module mod-b\n\ngo 1.24\n");
  write(
    root,
    "apps/mod-b/internal/rag/y_test.go",
    `package rag

import (
	"os"
	"testing"
)

func TestNeedsDSN(t *testing.T) {
	if os.Getenv("HIVE_TEST_DB_URL") == "" {
		t.Skip("no db")
	}
}
`,
  );

  // One step, one shell `if` with NO `else`: only mod-a's branch ever calls
  // `go test`. mod-b's ./internal/rag reads the same DSN variable and sits
  // under a matrix leg the working-directory template resolves for, but no
  // `go test` command anywhere ever names it. It must be reported as dark.
  write(
    root,
    ".github/workflows/fixture.yml",
    `on:
  pull_request:
jobs:
  go-tests:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - module: mod-a
            path: ./apps/mod-a
          - module: mod-b
            path: ./apps/mod-b
    steps:
      - name: bootstrap
        run: |
          echo "HIVE_TEST_DB_URL=postgresql://fixture" >> "$GITHUB_ENV"
      - name: rls
        working-directory: \${{ matrix.path }}
        run: |
          if [ "\${{ matrix.module }}" = "mod-a" ]; then
            go test -tags integration -count=1 ./internal/rag/...
          fi
`,
  );

  return root;
}

function run(root) {
  return spawnSync("node", [LINT_SCRIPT], { cwd: root, encoding: "utf8" });
}

// A second, three-leg fixture for the `elif` misattribution ecc:code-review
// found on this same PR. Each leg's branch names a DIFFERENT package
// (mod-a/internal/rag, mod-b/internal/other, mod-c/internal/rag again) so a
// misattribution changes the pass/fail outcome rather than hiding behind an
// identical pattern every branch happens to share. Before the fix,
// legsForLine's backward scan recognised `if` and `else` but not `elif`, so
// scanning back from mod-b's `elif` line walked straight past its own
// `elif [ "${{ matrix.module }}" = "mod-b" ]` header and latched onto the
// outer `if [ ... = "mod-a" ]` instead, crediting that line with mod-a's
// directory, not mod-b's. mod-b/internal/other then matched no invocation at
// all (the one line that names it was attributed to the wrong module), and
// the guard failed for the wrong reason: "no go test step ... names it",
// when a real one does, misattributed. Confirmed RED against the pre-fix
// legsForLine, extracted verbatim and run in isolation on this exact chain:
// it returned `[{module:"mod-a"}]` for the elif line where
// `[{module:"mod-b"}]` was correct.
function buildElifFixture() {
  const root = mkdtempSync(join(tmpdir(), "lint-go-db-test-wiring-elif-fixture-"));

  const dsnTest = (pkg) => `package ${pkg}

import (
	"os"
	"testing"
)

func TestNeedsDSN(t *testing.T) {
	if os.Getenv("HIVE_TEST_DB_URL") == "" {
		t.Skip("no db")
	}
}
`;

  write(root, "apps/mod-a/go.mod", "module mod-a\n\ngo 1.24\n");
  write(root, "apps/mod-a/internal/rag/x_test.go", dsnTest("rag"));
  write(root, "apps/mod-b/go.mod", "module mod-b\n\ngo 1.24\n");
  write(root, "apps/mod-b/internal/other/x_test.go", dsnTest("other"));
  write(root, "apps/mod-c/go.mod", "module mod-c\n\ngo 1.24\n");
  write(root, "apps/mod-c/internal/rag/x_test.go", dsnTest("rag"));

  write(
    root,
    ".github/workflows/fixture.yml",
    `on:
  pull_request:
jobs:
  go-tests:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        include:
          - module: mod-a
            path: ./apps/mod-a
          - module: mod-b
            path: ./apps/mod-b
          - module: mod-c
            path: ./apps/mod-c
    steps:
      - name: bootstrap
        run: |
          echo "HIVE_TEST_DB_URL=postgresql://fixture" >> "$GITHUB_ENV"
      - name: rls
        working-directory: \${{ matrix.path }}
        run: |
          if [ "\${{ matrix.module }}" = "mod-a" ]; then
            go test -tags integration -count=1 ./internal/rag/...
          elif [ "\${{ matrix.module }}" = "mod-b" ]; then
            go test -tags integration -count=1 ./internal/other/...
          else
            go test -tags integration -count=1 ./internal/rag/...
          fi
`,
  );

  return root;
}

const elifRoot = buildElifFixture();
try {
  const result = run(elifRoot);
  const output = `${result.stdout}${result.stderr}`;

  // mod-b's own elif line genuinely reaches ./internal/other/..., so a
  // correctly leg-aware guard must pair it, not report it dark.
  assert.equal(
    result.status,
    0,
    `expected mod-b/internal/other to pair via its own elif line. Output:\n${output}`,
  );
  assert.doesNotMatch(
    output,
    /mod-b\/internal\/other/,
    `mod-b/internal/other must not be named as a problem when its elif line is attributed correctly. Output:\n${output}`,
  );
} finally {
  rmSync(elifRoot, { recursive: true, force: true });
}

const root = buildFixture();
try {
  const result = run(root);
  const output = `${result.stdout}${result.stderr}`;

  // THE HOLE: mod-b/internal/rag is never reached by any go test command
  // (mod-a's if-branch has no else), so the guard must fail and name it.
  // Before the leg-aware fix, resolveDirectories credited mod-a's single
  // invocation with BOTH matrix legs' directories regardless of the shell
  // `if` wrapping it, so the pairing lookup for mod-b/internal/rag found
  // that invocation, saw HIVE_TEST_DB_URL in scope and a pattern that
  // covers ./internal/rag, and reported it paired: exit 0, no mention of
  // mod-b at all.
  assert.equal(
    result.status,
    1,
    `expected the guard to fail on an untested mod-b/internal/rag, got exit ${result.status}. Output:\n${output}`,
  );
  assert.match(
    output,
    /mod-b\/internal\/rag/,
    `expected the failure to name mod-b/internal/rag as unpaired. Output:\n${output}`,
  );
} finally {
  rmSync(root, { recursive: true, force: true });
}

console.log("lint-go-db-test-wiring.test: PASS");
