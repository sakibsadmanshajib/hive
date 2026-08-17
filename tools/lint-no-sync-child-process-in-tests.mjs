#!/usr/bin/env node
// Structural guard against a synchronous child process inside the Playwright
// test tree.
//
// `execFileSync`, `execSync` and `spawnSync` block the worker's event loop for
// as long as the child runs. Playwright's test timeout is a timer on that same
// loop, so while the child is alive the timeout physically cannot fire. The
// result is not a slow test, it is a test that reports the wrong verdict: PR
// #838 found one recorded as passed at 30194 ms under a 30000 ms timeout. Every
// timeout in the file is disarmed for the duration, including the ones
// protecting assertions that have nothing to do with the child.
//
// The async forms (`execFile` + `promisify`, `spawn` with an awaited `close`)
// leave the loop free, so the timer fires and the test fails honestly. That is
// the fix in every case but one, and the one exception is in ALLOWED below with
// its reason.
//
// Deliberately matches call shape (`name(`) and import clauses rather than bare
// mentions, and deliberately does NOT strip comments first. Stripping `//` to
// end of line would also eat anything after a URL, which is a way to hide a real
// call from this lint. A prose comment that spells a banned call with its
// parenthesis will trip the guard: that is a loud, harmless false positive, and
// rewording the comment is cheaper than a scanner that can be fooled.

import { readFileSync, readdirSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..");

// Both Playwright configs in the repo (apps/web-console/playwright.config.ts
// and apps/web-console/e2e/phase-19/owui/playwright.owui.config.ts) root their
// projects inside these two trees. Support modules count: a spec importing a
// helper that blocks the loop is blocked just the same.
const TEST_ROOTS = ["apps/web-console/e2e", "apps/web-console/tests/e2e"];
const SOURCE_FILE = /\.(ts|tsx|mts|cts|mjs|cjs|js)$/;

const BANNED = ["execFileSync", "execSync", "spawnSync"];

// Narrow, documented exceptions. Path is repo-relative.
const ALLOWED = new Map([
  [
    "apps/web-console/e2e/phase-19/owui/owui.setup.ts",
    "Runs once inside the owui-setup project, not mid-test, so the only deadline " +
      "it can eat is its own, and stdio: \"inherit\" streams the installer's progress " +
      "into the job log live where a buffered async child would withhold it until exit. " +
      "PR #838 left this one in place on purpose. Convert it if it ever moves into a test.",
  ],
]);

const CALL_RE = new RegExp(`\\b(${BANNED.join("|")})\\s*\\(`);
// A banned name reached through a property, whether it is called there or only
// aliased: `cp.execSync(...)`, `require("node:child_process").execSync`,
// `cp["spawnSync"]`. Without this, binding the function to a local name and
// calling that name would slip past the call-shape match entirely.
const PROP_RE = new RegExp(
  `(?:\\.\\s*(${BANNED.join("|")})\\b|\\[\\s*["'](${BANNED.join("|")})["']\\s*\\])`,
);

export function findOffences(source) {
  const offences = [];
  const lines = source.split("\n");

  lines.forEach((line, index) => {
    const call = line.match(CALL_RE);
    if (call) offences.push({ line: index + 1, name: call[1], text: line.trim() });
    const prop = line.match(PROP_RE);
    if (prop) {
      offences.push({ line: index + 1, name: prop[1] ?? prop[2], text: line.trim() });
    }
  });

  // Import clauses can span lines, so scan the whole source for the module
  // specifier and check the binding list that precedes it.
  for (const match of source.matchAll(/import\s*\{([^}]*)\}\s*from\s*["'](?:node:)?child_process["']/g)) {
    for (const name of BANNED) {
      if (new RegExp(`\\b${name}\\b`).test(match[1])) {
        const line = source.slice(0, match.index).split("\n").length;
        offences.push({ line, name, text: match[0].replace(/\s+/g, " ").trim() });
      }
    }
  }
  for (const match of source.matchAll(/(?:const|let|var)\s*\{([^}]*)\}\s*=\s*require\(\s*["'](?:node:)?child_process["']\s*\)/g)) {
    for (const name of BANNED) {
      if (new RegExp(`\\b${name}\\b`).test(match[1])) {
        const line = source.slice(0, match.index).split("\n").length;
        offences.push({ line, name, text: match[0].replace(/\s+/g, " ").trim() });
      }
    }
  }

  // De-duplicate: an import line matched by both scans is one offence.
  const seen = new Set();
  return offences.filter((offence) => {
    const key = `${offence.line}:${offence.name}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function sourceFiles() {
  const found = [];
  const walk = (dir) => {
    for (const entry of readdirSync(join(REPO_ROOT, dir), { withFileTypes: true })) {
      if (entry.name.startsWith(".") || entry.name === "node_modules") continue;
      const rel = `${dir}/${entry.name}`;
      if (entry.isDirectory()) walk(rel);
      else if (SOURCE_FILE.test(entry.name)) found.push(rel);
    }
  };
  for (const root of TEST_ROOTS) walk(root);
  return found.sort();
}

const MUST_CATCH = [
  ["the #838 shape verbatim", 'execFileSync("node", [script], { stdio: "inherit" });'],
  ["exec flavour", "const out = execSync(`psql -c 'select 1'`);"],
  ["spawn flavour", "spawnSync('python3', [seeder], { encoding: 'utf8' });"],
  ["namespaced call", "cp.execFileSync(bin, args);"],
  ["awaited but still sync", "await Promise.resolve(execSync(cmd));"],
  ["spaced call", "execSync ('ls');"],
  ["aliased import", 'import { execSync as run } from "node:child_process";'],
  ["bare-specifier import", 'import { spawnSync } from "child_process";'],
  ["multi-line import", 'import {\n  execFileSync,\n} from "node:child_process";'],
  ["require destructure", 'const { execFileSync } = require("node:child_process");'],
  ["require renamed destructure", 'const { execSync: run } = require("child_process");'],
  // Bound to a local name and called through that, so no call site ever spells
  // a banned name. CodeRabbit found this bypass on PR #843.
  ["require property alias", 'const run = require("node:child_process").execSync;\nrun(cmd);'],
  ["namespace property alias", 'import cp from "node:child_process";\nconst run = cp.spawnSync;'],
  ["bracket property alias", 'const run = cp["execFileSync"];'],
];

const MUST_ALLOW = [
  ["the async replacement PR #838 shipped", 'const execFileAsync = promisify(execFile);'],
  ["awaited async child", "await execFileAsync('python3', [seeder]);"],
  ["async import", 'import { execFile, spawn } from "node:child_process";'],
  // The existing prose in tests/e2e/support/*.ts explains why the sync form is
  // banned. Explaining the ban must not trip the ban.
  ["prose naming the banned call", " * Awaited rather than synchronous. `execFileSync` freezes the worker's"],
  ["prose in a sentence", "// The reseed used to run through `execFileSync`, which freezes the loop."],
  ["an unrelated sync fs call", "const raw = readFileSync(path, 'utf8');"],
  ["a name that merely ends in Sync", "await waitForIdleSync(page);"],
];

function selfTest() {
  for (const [name, source] of MUST_CATCH) {
    assert.ok(findOffences(source).length > 0, `self-test: NOT caught -> ${name}: ${source}`);
  }
  for (const [name, source] of MUST_ALLOW) {
    const offences = findOffences(source);
    assert.equal(
      offences.length,
      0,
      `self-test: false positive -> ${name}: ${JSON.stringify(offences)}`,
    );
  }
  return MUST_CATCH.length + MUST_ALLOW.length;
}

function main() {
  let assertions;
  try {
    assertions = selfTest();
  } catch (err) {
    console.error(`lint-no-sync-child-process-in-tests: SELF-TEST FAILED\n${err.message}`);
    process.exit(2);
  }
  console.log(`lint-no-sync-child-process-in-tests: self-test ok (${assertions} assertions)`);
  if (process.argv.includes("--self-test")) process.exit(0);

  let failed = false;
  const fail = (msg) => {
    console.error(msg);
    failed = true;
  };

  // A stale exception is an exception nobody is looking at. Renaming or
  // deleting an allowlisted file has to come with the allowlist edit.
  for (const path of ALLOWED.keys()) {
    if (!existsSync(join(REPO_ROOT, path))) {
      fail(
        `tools/lint-no-sync-child-process-in-tests.mjs: allowlisted \`${path}\` no longer ` +
          "exists. Remove its entry from ALLOWED.",
      );
    }
  }

  const files = sourceFiles();
  let allowedHits = 0;
  for (const file of files) {
    const offences = findOffences(readFileSync(join(REPO_ROOT, file), "utf8"));
    if (offences.length === 0) continue;
    if (ALLOWED.has(file)) {
      allowedHits += offences.length;
      continue;
    }
    for (const offence of offences) {
      fail(
        `${file}:${offence.line}: \`${offence.name}\` blocks the Playwright worker's event ` +
          "loop, which stops every test timeout in this file from firing while the child " +
          `runs.\n    ${offence.text}\n    Use the async form (execFile + promisify, or spawn ` +
          "with an awaited close) and await it. If this genuinely cannot block a test " +
          "deadline, add the file to ALLOWED in " +
          "tools/lint-no-sync-child-process-in-tests.mjs with the reason.",
      );
    }
  }

  if (failed) {
    console.error(
      "\nlint-no-sync-child-process-in-tests: a synchronous child process in the Playwright " +
        "tree disarms the timeouts around it. PR #838 found a test recorded as passed at " +
        "30194 ms under a 30000 ms timeout for exactly this reason.",
    );
    process.exit(1);
  }

  console.log(
    `lint-no-sync-child-process-in-tests: ok (${files.length} files across ` +
      `${TEST_ROOTS.join(", ")}; ${allowedHits} allowlisted call(s) in ${ALLOWED.size} file(s))`,
  );
  process.exit(0);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
