#!/usr/bin/env node
// Phase 18 RBAC lint primitive.
//
// Scans Go source files under apps/control-plane/internal/ for bare role/
// email-verified checks that must be routed through the authz package instead.
//
// Forbidden patterns (outside the allowlist):
//   .Role == "owner"    — use Policy.Can(actor, perm)
//   .Role == "member"   — use Policy.Can(actor, perm)
//   chosen.Role         — use Actor.Role derived by ActorResolver
//   .EmailVerified &&   — use Policy.Can which gates on Verified internally
//   && .*\.EmailVerified — same
//
// Allowlist (these paths MAY contain the patterns):
//   apps/control-plane/internal/authz/           — the matrix itself
//   apps/control-plane/internal/platform/role.go — MembershipRole declarations
//   apps/control-plane/internal/platform/role_pgx.go — SQL queries returning role strings
//   *_test.go files — test stubs and fixtures
//
// Usage:
//   node lint-no-bare-role-check.mjs                        # scan all internal/
//   node lint-no-bare-role-check.mjs path/to/file.go        # scan specific file
//   node lint-no-bare-role-check.mjs path/to/dir/           # scan specific dir
//   node lint-no-bare-role-check.mjs --help

import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const PACKAGE_ROOT = resolve(HERE, "..");
// Default target: apps/control-plane/internal/ (two levels up from packages/openai-contract)
const REPO_ROOT = resolve(PACKAGE_ROOT, "../..");
const DEFAULT_TARGET = resolve(REPO_ROOT, "apps/control-plane/internal");

// Forbidden patterns — each entry is a regex applied line-by-line.
const FORBIDDEN_PATTERNS = [
  { re: /\.Role\s*==\s*"owner"/, label: '.Role == "owner"' },
  { re: /\.Role\s*==\s*"member"/, label: '.Role == "member"' },
  { re: /chosen\.Role\b/, label: "chosen.Role" },
  { re: /\.EmailVerified\s*&&/, label: ".EmailVerified &&" },
  { re: /&&\s*.*\.EmailVerified\b/, label: "&& .EmailVerified" },
];

// Allowlist — files whose absolute path starts with one of these prefixes
// OR matches *_test.go are skipped entirely.
const ALLOWLIST_DIRS = [
  resolve(REPO_ROOT, "apps/control-plane/internal/authz"),
  resolve(REPO_ROOT, "apps/control-plane/internal/platform/role.go"),
  resolve(REPO_ROOT, "apps/control-plane/internal/platform/role_pgx.go"),
];

function isAllowlisted(absPath) {
  if (absPath.endsWith("_test.go")) return true;
  return ALLOWLIST_DIRS.some(
    (allowed) => absPath === allowed || absPath.startsWith(allowed + "/")
  );
}

function collectGoFiles(target) {
  const files = [];
  function walk(p) {
    if (!existsSync(p)) return;
    const st = statSync(p);
    if (st.isFile()) {
      if (p.endsWith(".go")) files.push(p);
      return;
    }
    if (st.isDirectory()) {
      for (const entry of readdirSync(p)) {
        walk(join(p, entry));
      }
    }
  }
  walk(target);
  return files;
}

function lintFile(absPath) {
  if (isAllowlisted(absPath)) return [];
  if (!existsSync(absPath) || !statSync(absPath).isFile()) {
    return [{ file: absPath, line: 0, pattern: "(missing)", text: "file not found" }];
  }
  const src = readFileSync(absPath, "utf8");
  const lines = src.split("\n");
  const offenders = [];
  for (let i = 0; i < lines.length; i++) {
    const lineText = lines[i];
    for (const { re, label } of FORBIDDEN_PATTERNS) {
      if (re.test(lineText)) {
        offenders.push({
          file: absPath,
          line: i + 1,
          pattern: label,
          text: lineText.trim(),
        });
      }
    }
  }
  return offenders;
}

export function lint(targets) {
  const allFiles = [];
  for (const t of targets) {
    const abs = resolve(t);
    const st = existsSync(abs) ? statSync(abs) : null;
    if (st && st.isDirectory()) {
      allFiles.push(...collectGoFiles(abs));
    } else {
      allFiles.push(abs);
    }
  }

  const results = allFiles.map((f) => ({ file: f, errors: lintFile(f) }));
  const failed = results.filter((r) => r.errors.length > 0);
  return { results, failed };
}

function parseArgs(argv) {
  const args = argv.slice(2);
  const explicit = [];
  for (const a of args) {
    if (a === "--help" || a === "-h") {
      console.log(
        "usage: lint-no-bare-role-check.mjs [path ...]\n" +
          "  Default: scan apps/control-plane/internal/\n" +
          "  Allowlist: internal/authz/, platform/role.go, platform/role_pgx.go, *_test.go\n" +
          "  Forbidden: .Role==\"owner\", .Role==\"member\", chosen.Role, .EmailVerified &&"
      );
      process.exit(0);
    } else {
      explicit.push(a);
    }
  }
  if (explicit.length > 0) return explicit;
  return [DEFAULT_TARGET];
}

function main() {
  const targets = parseArgs(process.argv);
  const { failed } = lint(targets);
  if (failed.length === 0) {
    const total = targets.map((t) => collectGoFiles(resolve(t))).flat().length;
    console.log(`lint-no-bare-role-check: ok (${total} files clean)`);
    process.exit(0);
  }
  for (const r of failed) {
    for (const e of r.errors) {
      console.error(
        `${e.file}:${e.line}: pattern '${e.pattern}' found in: ${e.text}\n` +
          "  -> Route authz through authz.Policy.Can or authz.RequirePermission\n" +
          `  -> Allowlist: internal/authz/, platform/role.go, platform/role_pgx.go, *_test.go`
      );
    }
  }
  process.exit(1);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
