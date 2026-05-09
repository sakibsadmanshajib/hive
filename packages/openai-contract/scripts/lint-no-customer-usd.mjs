#!/usr/bin/env node
// Customer-USD / FX zero-leak lint primitive.
//
// Phase 14: scans OpenAPI YAML path documents for property keys that would
// leak USD or FX language onto a customer-facing surface.
//
// Phase 17 (FX-17-06): extends `--all` mode to scan repo-wide source classes:
//   - Go struct field tags in apps/control-plane + apps/edge-api
//   - TS/TSX interface fields in apps/web-console + apps/chat-app/client
//   - OpenAPI YAML in packages/openai-contract/spec/paths
//
// BD regulatory rule: BDT-only on every wire shape consumed by a Bangladesh
// customer. The lint is the standing repo-wide guard against regression.
//
// Whitelist: a line bearing the adjacent comment `// PHASE-17-INTERNAL-ONLY`
// is intentionally exempt (server→Stripe USD payload edge case). Use with
// extreme care — the comment must justify why the field never reaches a
// customer wire.
//
// Default targets (no flags): the four Phase 14 path files (backwards
// compatible with the original CI wiring).
// Pass --all to chain YAML + Go + TS scanners across the repo.
// Pass explicit paths to override the default list (test harness uses this).

import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { dirname, resolve, join, basename } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const PACKAGE_ROOT = resolve(HERE, "..");
const PATHS_DIR = join(PACKAGE_ROOT, "spec", "paths");
const REPO_ROOT = resolve(PACKAGE_ROOT, "..", "..");

const PHASE14_DEFAULTS = [
  join(PATHS_DIR, "budgets.yaml"),
  join(PATHS_DIR, "spend-alerts.yaml"),
  join(PATHS_DIR, "invoices.yaml"),
  join(PATHS_DIR, "grants.yaml"),
];

// ─── Regex sources ────────────────────────────────────────────────────────────

// Banned key fragment shared across scanners.
const BANNED_KEY = "(amount_usd|usd_[A-Za-z0-9_]*|fx_[A-Za-z0-9_]*|price_per_credit_usd|exchange_rate)";

// YAML: anchored on YAML key positions so prose in description: blocks does
// not trip the lint.
const YAML_KEY_PATTERN = new RegExp(`^(\\s*-?\\s*)${BANNED_KEY}\\s*:`, "gm");

// Go: struct field tag `json:"<key>..."` — ignore lines tagged json:"-".
const GO_TAG_PATTERN = new RegExp(`\`json:"${BANNED_KEY}[^"]*"`, "g");

// TS/TSX: interface / type field declaration at start-of-line.
//   amount_usd: number
//   amount_usd?: number
//   "amount_usd": number   (covered by trimming the optional quote)
const TS_FIELD_PATTERN = new RegExp(`^\\s*"?${BANNED_KEY}"?\\s*\\??\\s*:`, "gm");

const WHITELIST_MARKER = "PHASE-17-INTERNAL-ONLY";

// ─── Walkers ──────────────────────────────────────────────────────────────────

function listAllPathFiles() {
  if (!existsSync(PATHS_DIR)) return [];
  return readdirSync(PATHS_DIR)
    .filter((f) => f.endsWith(".yaml") || f.endsWith(".yml"))
    .map((f) => join(PATHS_DIR, f));
}

const TS_SKIP_DIRS = new Set(["node_modules", ".next", "dist", "build", "out", ".turbo", "coverage"]);
const GO_SKIP_DIRS = new Set(["testdata", "fixtures", "vendor", "node_modules"]);

function isTsTestFile(name) {
  return /\.(test|spec)\.(ts|tsx|jsx|js)$/.test(name);
}

function isInTsTestDir(relPath) {
  return relPath.split(/[\\/]/).some((seg) => seg === "__tests__" || seg === "e2e");
}

function walkDir(root, accept, skipDirs) {
  const out = [];
  if (!existsSync(root)) return out;
  const stack = [root];
  while (stack.length > 0) {
    const cur = stack.pop();
    let entries;
    try {
      entries = readdirSync(cur, { withFileTypes: true });
    } catch {
      continue;
    }
    for (const ent of entries) {
      const full = join(cur, ent.name);
      if (ent.isDirectory()) {
        if (skipDirs.has(ent.name)) continue;
        stack.push(full);
      } else if (ent.isFile()) {
        if (accept(full, ent.name)) out.push(full);
      }
    }
  }
  return out;
}

function listGoFiles() {
  const roots = [
    join(REPO_ROOT, "apps", "control-plane"),
    join(REPO_ROOT, "apps", "edge-api"),
  ];
  const accept = (full, name) => {
    if (!name.endsWith(".go")) return false;
    if (name.endsWith("_test.go")) return false;
    return true;
  };
  return roots.flatMap((r) => walkDir(r, accept, GO_SKIP_DIRS));
}

function listTsFiles() {
  const roots = [
    join(REPO_ROOT, "apps", "web-console", "app"),
    join(REPO_ROOT, "apps", "web-console", "components"),
    join(REPO_ROOT, "apps", "web-console", "lib"),
    join(REPO_ROOT, "apps", "chat-app", "client", "src"),
  ];
  const accept = (full, name) => {
    if (!/\.(ts|tsx|jsx)$/.test(name)) return false;
    if (isTsTestFile(name)) return false;
    const rel = full.slice(REPO_ROOT.length);
    if (isInTsTestDir(rel)) return false;
    return true;
  };
  return roots.flatMap((r) => walkDir(r, accept, TS_SKIP_DIRS));
}

// ─── Per-file linters ────────────────────────────────────────────────────────

function readFileSafe(file) {
  if (!existsSync(file) || !statSync(file).isFile()) {
    return null;
  }
  return readFileSync(file, "utf8");
}

function lineNumberAt(src, offset) {
  return src.slice(0, offset).split("\n").length;
}

function lineContentAt(src, offset) {
  const before = src.lastIndexOf("\n", offset - 1);
  const after = src.indexOf("\n", offset);
  return src.slice(before + 1, after === -1 ? src.length : after);
}

function isWhitelisted(lineContent) {
  return lineContent.includes(WHITELIST_MARKER);
}

function lintWithPattern(file, pattern, keyGroupIndex) {
  const src = readFileSafe(file);
  if (src === null) {
    return { file, errors: [{ line: 0, key: "(missing)", message: "file not found" }] };
  }
  const offenders = [];
  const re = new RegExp(pattern.source, pattern.flags);
  let match;
  while ((match = re.exec(src)) !== null) {
    const lineContent = lineContentAt(src, match.index);
    if (isWhitelisted(lineContent)) continue;
    offenders.push({
      line: lineNumberAt(src, match.index),
      key: match[keyGroupIndex],
    });
  }
  return { file, errors: offenders };
}

export function lintYamlFile(file) {
  return lintWithPattern(file, YAML_KEY_PATTERN, 2);
}

export function lintGoFile(file) {
  return lintWithPattern(file, GO_TAG_PATTERN, 1);
}

export function lintTsFile(file) {
  return lintWithPattern(file, TS_FIELD_PATTERN, 1);
}

// ─── Top-level orchestration ─────────────────────────────────────────────────

function classify(file) {
  if (file.endsWith(".go")) return "go";
  if (/\.(ts|tsx|jsx)$/.test(file)) return "ts";
  if (/\.(yaml|yml)$/.test(file)) return "yaml";
  return "yaml"; // default fallback for explicit paths
}

function lintOne(file) {
  switch (classify(file)) {
    case "go":
      return lintGoFile(file);
    case "ts":
      return lintTsFile(file);
    case "yaml":
    default:
      return lintYamlFile(file);
  }
}

export function lint(targets) {
  const results = targets.map((t) => lintOne(t));
  const failed = results.filter((r) => r.errors.length > 0);
  return { results, failed };
}

function parseArgs(argv) {
  const args = argv.slice(2);
  let all = false;
  const explicit = [];
  for (const a of args) {
    if (a === "--all") {
      all = true;
    } else if (a === "--help" || a === "-h") {
      console.log("usage: lint-no-customer-usd.mjs [--all] [path ...]");
      console.log("  --all   scan YAML paths + Go + TS sources repo-wide");
      console.log("  default: Phase 14 four OpenAPI YAML path files");
      console.log(`  whitelist: lines containing '// ${WHITELIST_MARKER}' are exempt`);
      process.exit(0);
    } else {
      explicit.push(resolve(a));
    }
  }
  if (explicit.length > 0) return { mode: "explicit", targets: explicit };
  if (all) {
    const targets = [
      ...listAllPathFiles(),
      ...listGoFiles(),
      ...listTsFiles(),
    ];
    return { mode: "all", targets };
  }
  return { mode: "default", targets: PHASE14_DEFAULTS };
}

function main() {
  const { mode, targets } = parseArgs(process.argv);
  if (targets.length === 0) {
    console.error("lint-no-customer-usd: no target files");
    process.exit(2);
  }
  const { failed } = lint(targets);
  if (failed.length === 0) {
    const banner = mode === "all"
      ? `lint-no-customer-usd: ok (${targets.length} files clean — YAML+Go+TS, whitelist '${WHITELIST_MARKER}')`
      : `lint-no-customer-usd: ok (${targets.length} files clean)`;
    console.log(banner);
    process.exit(0);
  }
  for (const r of failed) {
    for (const e of r.errors) {
      console.error(`${r.file}:${e.line}: customer-USD key '${e.key}'${e.message ? " — " + e.message : ""}`);
    }
  }
  console.error(`lint-no-customer-usd: ${failed.length} file(s) flagged (whitelist marker: '// ${WHITELIST_MARKER}')`);
  process.exit(1);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
