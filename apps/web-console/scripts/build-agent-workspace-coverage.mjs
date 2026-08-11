#!/usr/bin/env node
// Turns one Playwright JSON reporter run into the machine-readable coverage
// ledger for the agent-workspace surface: proven over total, with every
// unproven control named and why.
//
// Usage: node scripts/build-agent-workspace-coverage.mjs <playwright-report.json> [out.json]
//
// The output is GENERATED, never committed. tests/e2e/_probe/*-coverage.json is
// gitignored precisely so the number cannot be hand-edited or left frozen at
// whatever a past run produced: a committed snapshot reports the same ratio
// forever, including after coverage has changed underneath it. CI uploads this
// file as a run artifact and prints the ratio into the job summary
// (.github/workflows/deploy-demo-box.yml).
//
// ponytail: string-matching [C#] tags out of test titles rather than a custom
// Playwright reporter or fixture. This is a report-shape problem, not a
// runtime one, and the JSON reporter already carries everything needed.
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const PROBE_DIR = join(SCRIPT_DIR, "..", "tests", "e2e", "_probe");
const CONTROLS_PATH = join(PROBE_DIR, "agent-workspace-controls.json");

const [, , reportPathArg, outPathArg] = process.argv;
if (!reportPathArg) {
  console.error("usage: build-agent-workspace-coverage.mjs <playwright-report.json> [out.json]");
  process.exit(1);
}
const outPath = outPathArg ?? join(PROBE_DIR, "agent-workspace-coverage.json");

const controls = JSON.parse(readFileSync(CONTROLS_PATH, "utf8"));
const report = JSON.parse(readFileSync(reportPathArg, "utf8"));

const TAG_RE = /\[C(\d+)\]/g;

/** @type {Map<string, { status: string, reason: string | null, attempts: number, title: string }>} */
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
      /*
       * `test.status` is Playwright's own verdict over ALL attempts:
       * "expected", "unexpected", "flaky" or "skipped". It is what this reads,
       * because the previous version read results[0], the FIRST attempt, while
       * the config sets `retries: 2`. That reported a control from an attempt
       * that is not the outcome of the run, in both directions: a control that
       * failed twice and passed on the third was reported "failed", and a
       * control that passed first and failed on a retry was reported "passed".
       *
       * "flaky" is deliberately NOT proven. A control that only passes on a
       * retry has not demonstrated the behaviour, it has demonstrated that the
       * behaviour is not reliable, and the config's failOnFlakyTests fails the
       * run over it anyway.
       */
      const status = test?.status ?? "not_run";
      const attempts = test?.results?.length ?? 0;
      // The LAST attempt carries the outcome the verdict is drawn from.
      const result = attempts > 0 ? test.results[attempts - 1] : undefined;
      const skipAnnotation = test?.annotations?.find((a) => a.type === "skip");
      let reason = null;
      if (status === "skipped") {
        reason = skipAnnotation?.description ?? "skipped with no recorded reason";
      } else if (status === "flaky") {
        reason = `passed only on retry (${attempts} attempts), so the behaviour is not proven`;
      } else if (status !== "expected") {
        reason = result?.error?.message ?? status;
      }
      for (const id of ids) {
        byControlId.set(id, { status, reason, attempts, title });
      }
    }
    walkSuites(suite.suites);
  }
}
walkSuites(report.suites);

const results = controls.controls.map((c) => {
  const run = byControlId.get(c.id);
  const proven = run?.status === "expected";
  return {
    id: c.id,
    description: c.description,
    requires_creds: c.requires_creds,
    screen: c.screen,
    proven,
    status: run?.status ?? "not_run",
    attempts: run?.attempts ?? 0,
    reason: proven ? null : (run?.reason ?? "no matching test found in this run"),
    test_title: run?.title ?? null,
  };
});

const proven = results.filter((r) => r.proven);
const unproven = results.filter((r) => !r.proven);
const notRun = results.filter((r) => r.status === "not_run");

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
  console.log(`  UNPROVEN ${u.id}: ${u.description} (${u.status}) ${u.reason}`);
}

/*
 * A control with no test at all is the one failure this script has to be loud
 * about. A skip carries a reason and is visible in the ratio; a control that
 * no test mentions is invisible in exactly the way a spec that never runs is,
 * and the whole point of the ledger is that a control cannot go missing
 * quietly.
 */
if (notRun.length > 0) {
  console.error(
    `\nFAIL: ${notRun.length} control(s) have no test in this run: ` +
      `${notRun.map((r) => r.id).join(", ")}. Either add a test carrying that ` +
      "[C#] tag, or remove the control from agent-workspace-controls.json.",
  );
  process.exit(1);
}
