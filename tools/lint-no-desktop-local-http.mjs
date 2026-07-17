#!/usr/bin/env node
// Structural guard for issue #342 item 5 (decision D8).
//
// apps/desktop/src-tauri/src/local_tasks.rs's module doc claims "this module
// has no HTTP client dependency at all" as the ACTUAL mechanism behind the
// guarantee that a desktop-local task never appears in the cloud task list:
// there is no code path connecting the two, not a runtime check that happens
// not to fire. That guarantee was enforced only by code review. This lint
// makes it structural: if the module ever gains an HTTP client dependency,
// CI fails instead of the guarantee silently breaking.
//
// Mirrors the pattern of tools/lint-no-direct-*.mjs (a small, dependency-free
// source scanner wired into the repo-policy-lints CI job).

import { readFileSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..");
const TARGET = join(REPO_ROOT, "apps", "desktop", "src-tauri", "src", "local_tasks.rs");

// HTTP client crates that would give this module a path to the network.
const FORBIDDEN_CRATES = [
  "reqwest",
  "hyper",
  "ureq",
  "isahc",
  "awc",
  "surf",
  "curl",
  "attohttpc",
  "http_req",
];

export function findOffenders(src) {
  const offenders = [];
  src.split("\n").forEach((line, i) => {
    // Strip a line comment before matching: the module doc names `reqwest`
    // precisely to state its absence, and that mention must not trip the lint.
    const code = line.split("//")[0];
    for (const crate of FORBIDDEN_CRATES) {
      // Match the crate as a Rust path segment: `use reqwest`, `reqwest::...`,
      // `extern crate reqwest;`, `reqwest {` -- not an arbitrary substring.
      const re = new RegExp(`\\b${crate}\\b\\s*(::|;|\\{)`);
      if (re.test(code)) {
        offenders.push({ line: i + 1, crate, text: line.trim() });
      }
    }
  });
  return offenders;
}

function main() {
  if (!existsSync(TARGET)) {
    console.error(`lint-no-desktop-local-http: target not found: ${TARGET}`);
    process.exit(2);
  }
  const offenders = findOffenders(readFileSync(TARGET, "utf8"));
  if (offenders.length === 0) {
    console.log(
      "lint-no-desktop-local-http: ok (desktop-local task store has no HTTP client dependency)",
    );
    process.exit(0);
  }
  for (const o of offenders) {
    console.error(`${TARGET}:${o.line}: forbidden HTTP client '${o.crate}' — ${o.text}`);
  }
  console.error(
    "lint-no-desktop-local-http: the desktop-local task store must have no HTTP client dependency " +
      "(issue #342 item 5, decision D8: a desktop-local task must never reach the cloud task list).",
  );
  process.exit(1);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
