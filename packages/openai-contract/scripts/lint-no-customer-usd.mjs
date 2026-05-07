#!/usr/bin/env node
// Phase 14 customer-USD lint primitive (Phase 17 will extend repo-wide via --all).
//
// Scans OpenAPI YAML path documents for property keys that would leak USD or
// FX language onto a customer-facing surface. BD regulatory rule: BDT-only on
// every wire shape consumed by a Bangladesh customer.
//
// Default targets: the four Phase 14 path files. Pass --all to scan every
// file under packages/openai-contract/spec/paths/. Pass explicit paths to
// override the default list (test harness uses this).

import { readFileSync, readdirSync, existsSync, statSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const PACKAGE_ROOT = resolve(HERE, "..");
const PATHS_DIR = join(PACKAGE_ROOT, "spec", "paths");

const PHASE14_DEFAULTS = [
  join(PATHS_DIR, "budgets.yaml"),
  join(PATHS_DIR, "spend-alerts.yaml"),
  join(PATHS_DIR, "invoices.yaml"),
  join(PATHS_DIR, "grants.yaml"),
];

// Customer-USD key matchers. Anchored on YAML key positions so prose in
// description: blocks does not trip the lint.
//
//   amount_usd
//   usd_<anything>
//   fx_<anything>
//   price_per_credit_usd
//   exchange_rate
const KEY_PATTERN = /^(\s*-?\s*)(amount_usd|usd_[A-Za-z0-9_]*|fx_[A-Za-z0-9_]*|price_per_credit_usd|exchange_rate)\s*:/m;
const KEY_PATTERN_GLOBAL = new RegExp(KEY_PATTERN.source, "gm");

function listAllPathFiles() {
  if (!existsSync(PATHS_DIR)) return [];
  return readdirSync(PATHS_DIR)
    .filter((f) => f.endsWith(".yaml") || f.endsWith(".yml"))
    .map((f) => join(PATHS_DIR, f));
}

function lintFile(file) {
  if (!existsSync(file) || !statSync(file).isFile()) {
    return { file, errors: [{ line: 0, key: "(missing)", message: "file not found" }] };
  }
  const src = readFileSync(file, "utf8");
  const offenders = [];
  let match;
  KEY_PATTERN_GLOBAL.lastIndex = 0;
  while ((match = KEY_PATTERN_GLOBAL.exec(src)) !== null) {
    const offset = match.index;
    const lineNumber = src.slice(0, offset).split("\n").length;
    offenders.push({ line: lineNumber, key: match[2] });
  }
  return { file, errors: offenders };
}

export function lint(targets) {
  const results = targets.map((t) => lintFile(t));
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
      process.exit(0);
    } else {
      explicit.push(resolve(a));
    }
  }
  if (explicit.length > 0) return explicit;
  if (all) return listAllPathFiles();
  return PHASE14_DEFAULTS;
}

function main() {
  const targets = parseArgs(process.argv);
  if (targets.length === 0) {
    console.error("lint-no-customer-usd: no target files");
    process.exit(2);
  }
  const { failed } = lint(targets);
  if (failed.length === 0) {
    console.log(`lint-no-customer-usd: ok (${targets.length} files clean)`);
    process.exit(0);
  }
  for (const r of failed) {
    for (const e of r.errors) {
      console.error(`${r.file}:${e.line}: customer-USD key '${e.key}'${e.message ? " — " + e.message : ""}`);
    }
  }
  process.exit(1);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
