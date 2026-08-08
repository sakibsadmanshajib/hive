#!/usr/bin/env node
// Turns one Playwright JSON reporter run into the machine-readable coverage
// ledger for the agent-workspace surface: proven/total, with every unproven
// control named and why.
//
// Usage: node scripts/build-agent-workspace-coverage.mjs <playwright-report.json> [out.json]
//
// ponytail: string-matching [C#] tags out of test titles rather than a custom
// Playwright reporter/fixture -- this is a report-shape problem, not a
// runtime one, and the JSON reporter already gives us everything needed.
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const CONTROLS_PATH = join(SCRIPT_DIR, "..", "tests", "e2e", "_probe", "agent-workspace-controls.json");

const [, , reportPathArg, outPathArg] = process.argv;
if (!reportPathArg) {
  console.error("usage: build-agent-workspace-coverage.mjs <playwright-report.json> [out.json]");
  process.exit(1);
}
const outPath = outPathArg ?? join(SCRIPT_DIR, "..", "tests", "e2e", "_probe", "agent-workspace-coverage.json");

const controls = JSON.parse(readFileSync(CONTROLS_PATH, "utf8"));
const report = JSON.parse(readFileSync(reportPathArg, "utf8"));

const TAG_RE = /\[C(\d+)\]/g;

/** @type {Map<string, { status: string, reason: string | null, title: string }>} */
const byControlId = new Map();

function walkSuites(suites) {
  for (const suite of suites ?? []) {
    for (const spec of suite.specs ?? []) {
      const title = spec.title ?? "";
      const ids = [...title.matchAll(TAG_RE)].map((m) => `C${m[1]}`);
      if (ids.length === 0) {
        walkSuites(suite.suites);
        continue;
      }
      const test = spec.tests?.[0];
      const result = test?.results?.[0];
      // Playwright's per-test `status` field uses "expected"/"unexpected"/
      // "flaky"/"skipped", not "passed"/"failed" -- the actual outcome for a
      // control is `result.status` from the attempt itself.
      const status = result?.status ?? "unknown";
      const skipAnnotation = test?.annotations?.find((a) => a.type === "skip");
      const reason = skipAnnotation?.description ?? (status === "failed" ? (result?.error?.message ?? "failed") : null);
      for (const id of ids) {
        byControlId.set(id, { status, reason, title });
      }
    }
    walkSuites(suite.suites);
  }
}
walkSuites(report.suites);

const results = controls.controls.map((c) => {
  const run = byControlId.get(c.id);
  const proven = run?.status === "passed";
  return {
    id: c.id,
    description: c.description,
    requires_creds: c.requires_creds,
    proven,
    status: run?.status ?? "not_run",
    reason: proven ? null : run?.reason ?? "no matching test found in this run",
    test_title: run?.title ?? null,
  };
});

const proven = results.filter((r) => r.proven);
const unproven = results.filter((r) => !r.proven);

const coverage = {
  surface: controls.surface,
  generated_at: new Date().toISOString(),
  total: results.length,
  proven: proven.length,
  ratio: `${proven.length}/${results.length}`,
  not_present: controls.not_present,
  unproven,
  results,
};

writeFileSync(outPath, JSON.stringify(coverage, null, 2) + "\n");
console.log(`agent-workspace coverage: ${coverage.ratio}`);
for (const u of unproven) {
  console.log(`  UNPROVEN ${u.id}: ${u.description} -- ${u.reason}`);
}
