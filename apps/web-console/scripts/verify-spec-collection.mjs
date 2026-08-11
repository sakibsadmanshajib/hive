#!/usr/bin/env node
// Guard: a Playwright spec must never leave the run plan quietly.
//
// Replaces scripts/verify-phase19-collection.sh, which asked the same question
// for one project only (issue 708: a broken `testMatch` collected zero files
// while the run still went green). Three ways a spec goes dark that the old
// per-project count could not see:
//
//   1. A syntax error, a throwing top-level import, or a bad config block in a
//      spec file. `playwright test --list` exits non-zero for this, but only if
//      something actually reads the exit code, and only for the files the
//      command's own projects load.
//   2. A `testDir` or `testMatch` edit that stops matching a file. Nothing
//      errors: the file is simply never loaded and never runs again.
//   3. A spec added to the tree that no project collects. It cannot fail,
//      because it never executes.
//
// The check is identity, not cardinality: every spec and setup file is pinned
// to the exact set of Playwright projects that collect it, in
// playwright-spec-manifest.json. A count alone is satisfiable by accident (add
// one spec, drop another, still green). Pinning names also makes the two cases
// distinguishable, which is the point:
//
//   * DELIBERATE removal or rewiring -> the manifest disagrees, and the fix is
//     the one-line manifest edit this script prints, made in the same PR.
//   * ACCIDENTAL drop -> the manifest disagrees in exactly the same way, and
//     the PR author has to look at a line they did not mean to touch.
//
// Needs no live stack, no browsers and no credentials: `--list` only loads the
// spec modules.
//
// Usage: node scripts/verify-spec-collection.mjs   (run from apps/web-console)

import { spawnSync } from "node:child_process";
import { readFileSync, existsSync, readdirSync } from "node:fs";
import { dirname, resolve, relative, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const WEB_CONSOLE = resolve(HERE, "..");
const MANIFEST = join(WEB_CONSOLE, "playwright-spec-manifest.json");

// The owui config gates its projects' testMatch on credentials being present
// (see the comment at the top of playwright.owui.config.ts): without them every
// owui project matches zero files on purpose, so listing it in a CI job that has
// no OWUI secrets would find nothing and call it fine. Placeholders make the
// collection deterministic and identical everywhere, and listing never dials
// out, so no real credential is needed or wanted here.
const OWUI_LIST_ENV = {
  OWUI_E2E_EMAIL: "collection-guard@example.invalid",
  OWUI_E2E_PASSWORD: "unused-collection-guard-placeholder",
  SUPABASE_OAUTH_CLIENT_ID: "unused-collection-guard-placeholder",
  SUPABASE_OAUTH_CLIENT_SECRET: "unused-collection-guard-placeholder",
};

const CONFIGS = [
  { label: "playwright.config.ts", args: [], env: {} },
  {
    label: "e2e/phase-19/owui/playwright.owui.config.ts",
    args: ["--config=e2e/phase-19/owui/playwright.owui.config.ts"],
    env: OWUI_LIST_ENV,
  },
];

// Where Playwright test modules live. Anything matching TEST_FILE under these
// is expected to be collected by at least one project.
const TEST_ROOTS = ["e2e", "tests/e2e"];
const TEST_FILE = /\.(spec|setup)\.ts$/;

function listFilesOnDisk() {
  const found = [];
  const walk = (dir) => {
    for (const entry of readdirSync(join(WEB_CONSOLE, dir), { withFileTypes: true })) {
      if (entry.name.startsWith(".")) continue;
      const rel = `${dir}/${entry.name}`;
      if (entry.isDirectory()) walk(rel);
      else if (TEST_FILE.test(entry.name)) found.push(rel);
    }
  };
  for (const root of TEST_ROOTS) walk(root);
  return found.sort();
}

// file (relative to apps/web-console) -> sorted project names that collect it.
export function collectedFrom(report) {
  const byFile = new Map();
  const walk = (suite) => {
    for (const child of suite.suites ?? []) walk(child);
    for (const spec of suite.specs ?? []) {
      const file = relative(WEB_CONSOLE, resolve(report.config.rootDir, spec.file));
      const projects = byFile.get(file) ?? new Set();
      for (const test of spec.tests ?? []) projects.add(test.projectName);
      byFile.set(file, projects);
    }
  };
  for (const suite of report.suites ?? []) walk(suite);
  return byFile;
}

// Playwright still emits its JSON report on a load failure, with the real
// cause in `errors`. The rest of that document is the resolved config, which
// buries the one thing anybody reading a red CI log needs.
function reportedErrors(run) {
  try {
    const report = JSON.parse(run.stdout);
    const messages = (report.errors ?? []).map((e) => e.message ?? JSON.stringify(e));
    if (messages.length) return messages.join("\n");
  } catch {
    // Not JSON at all, fall through to the raw output.
  }
  return [(run.stderr ?? "").trim(), (run.stdout ?? "").slice(0, 2000)]
    .filter(Boolean)
    .join("\n");
}

function listOneConfig({ label, args, env }) {
  const run = spawnSync(
    "npx",
    ["playwright", "test", "--list", "--reporter=json", ...args],
    {
      cwd: WEB_CONSOLE,
      encoding: "utf8",
      maxBuffer: 64 * 1024 * 1024,
      env: { ...process.env, ...env },
    },
  );

  // A non-zero exit here is the whole point: a spec that fails to load takes
  // the listing down with it. Proceeding on a partial plan would trade a loud
  // failure for a quiet absence, which is the hole this guard closes.
  if (run.status !== 0) {
    console.error(
      `spec collection guard FAILED: \`playwright test --list\` exited ${run.status} for ` +
        `${label}. A spec file did not load (syntax error, throwing top-level import, or a ` +
        "bad config block), so the run plan is incomplete.\n",
    );
    console.error(reportedErrors(run));
    process.exit(1);
  }

  let report;
  try {
    report = JSON.parse(run.stdout);
  } catch (err) {
    console.error(
      `spec collection guard FAILED: could not parse the JSON listing for ${label}: ` +
        `${err.message}`,
    );
    process.exit(1);
  }

  if (report.errors?.length) {
    console.error(
      `spec collection guard FAILED: ${label} listed with ${report.errors.length} load ` +
        "error(s), so at least one spec is missing from the run plan:",
    );
    for (const error of report.errors) console.error(error.message ?? JSON.stringify(error));
    process.exit(1);
  }

  return collectedFrom(report);
}

function main() {
  const document = JSON.parse(readFileSync(MANIFEST, "utf8"));
  const manifest = document.specs;
  const declaredConfigs = document.configs ?? {};

  const collected = new Map();
  const problems = [];

  for (const config of CONFIGS) {
    const byFile = listOneConfig(config);
    const projects = new Set();
    for (const [file, fileProjects] of byFile) {
      const merged = collected.get(file) ?? new Set();
      for (const project of fileProjects) {
        merged.add(project);
        projects.add(project);
      }
      collected.set(file, merged);
    }

    // tools/verify-spec-wiring.mjs resolves a workflow's `--config` and
    // `--project` arguments through this map instead of importing the config
    // itself. It is pinned here, from the same listing, so it cannot drift
    // into crediting a workflow with projects its config does not declare.
    const actual = [...projects].sort();
    const expected = [...(declaredConfigs[config.label] ?? [])].sort();
    if (JSON.stringify(actual) !== JSON.stringify(expected)) {
      problems.push(
        `CONFIGS   ${config.label} declares projects ${JSON.stringify(actual)}, manifest \`configs\` ` +
          `says ${JSON.stringify(expected)}. Update:\n    ${JSON.stringify(config.label)}: ` +
          `${JSON.stringify(actual)}`,
      );
    }
  }

  for (const label of Object.keys(declaredConfigs)) {
    if (!CONFIGS.some((config) => config.label === label)) {
      problems.push(
        `CONFIGS   manifest \`configs\` names ${label}, which this guard never lists. Add it to ` +
          "CONFIGS here, or drop it from the manifest.",
      );
    }
  }

  const asLine = (file, projects) =>
    `    ${JSON.stringify(file)}: ${JSON.stringify([...projects].sort())}`;

  for (const [file, expected] of Object.entries(manifest)) {
    const actual = collected.get(file);
    if (!actual) {
      problems.push(
        existsSync(join(WEB_CONSOLE, file))
          ? `DROPPED  ${file} is still on disk but no Playwright project collects it any more ` +
            `(manifest says ${JSON.stringify(expected)}). A testDir/testMatch edit removed it ` +
            "from the run plan. Restore the wiring, or drop its manifest line if that was " +
            "deliberate."
          : `DELETED  ${file} is gone from disk. If that was deliberate, remove its line from ` +
            "playwright-spec-manifest.json in this same PR.",
      );
      continue;
    }
    const actualSorted = [...actual].sort();
    if (JSON.stringify(actualSorted) !== JSON.stringify([...expected].sort())) {
      problems.push(
        `REWIRED  ${file} is collected by ${JSON.stringify(actualSorted)}, manifest says ` +
          `${JSON.stringify(expected)}. Update the manifest line if the rewiring was ` +
          `deliberate:\n${asLine(file, actual)}`,
      );
    }
  }

  for (const [file, projects] of collected) {
    if (!(file in manifest)) {
      problems.push(
        `UNDECLARED  ${file} runs but is not in the manifest. Add:\n${asLine(file, projects)}`,
      );
    }
  }

  // Only for files the manifest does not already account for: a declared spec
  // that lost its project is reported once, as DROPPED, with the manifest line
  // to look at.
  for (const file of listFilesOnDisk()) {
    if (!collected.has(file) && !(file in manifest)) {
      problems.push(
        `DARK  ${file} exists but no Playwright project collects it, so it can never fail. ` +
          "Wire it into a project, or delete it.",
      );
    }
  }

  if (problems.length) {
    console.error("spec collection guard FAILED:\n");
    for (const problem of problems) console.error(`  ${problem}\n`);
    process.exit(1);
  }

  console.log(
    `spec collection guard: OK, ${collected.size} Playwright test files collected across ` +
      `${CONFIGS.length} configs, each into the projects the manifest pins it to.`,
  );
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
