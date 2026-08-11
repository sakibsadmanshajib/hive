#!/usr/bin/env node
// Guard for issue #813, camouflage shape 14 in docs/TESTING-STANDARD.md.
//
// A Playwright spec that no workflow runs emits no signal at all. It does not
// skip, there is no variable to blame, and `npx playwright test` locally runs
// it, so the hole is invisible to anyone working on the specs.
//
// This guard answers one question per spec file: which workflow invocations
// select it, and can any of those run on an ordinary pull request. It reports
// three states, and it reports them separately, because collapsing them is how
// a coverage number becomes gameable:
//
//   pr     some invocation that a pull request can trigger selects the spec
//   other  only invocations that a pull request cannot trigger select it,
//          meaning a nightly schedule, a manual dispatch, or a labelled run
//   dark   no invocation selects it at all
//
// Anything that is not `pr` is debt, and every piece of it is declared in
// apps/web-console/tests/dark-spec-allowlist.json with the state it is in. A
// declared state that disagrees with the measured one fails, in both
// directions. That is what stops the obvious cheat: adding a
// workflow_dispatch-only workflow moves a spec from `dark` to `other`, which
// is a failure until somebody edits the ledger, and it never moves the
// headline pull-request number at all.
//
// Two deliberate design choices, both of them corrections of an earlier
// version of this file that is written up as shape 15 in the standard.
//
// 1. It executes nothing. An earlier version ran `playwright test --list` for
//    every invocation it parsed out of a workflow, including a `--config`
//    path taken from that same workflow text. Playwright IMPORTS a config
//    file, so that turned editing a `run:` line into arbitrary code execution
//    inside a required check. Wiring is resolved instead from
//    apps/web-console/playwright-spec-manifest.json, which pins every spec to
//    the projects that collect it and is itself verified against a live
//    `--list` by apps/web-console/scripts/verify-spec-collection.mjs in the
//    same required job. Ask the runner, but ask it through the artifact it
//    already certifies.
//
// 2. It parses workflows with a YAML parser and refuses arguments it does not
//    model. An invocation carrying `--grep`, a positional path filter, or any
//    flag not in the table below is a hard failure, because crediting it with
//    everything its projects contain would over-count exactly the way the
//    filename matching this guard replaced did.
//
// Run: node tools/verify-spec-wiring.mjs

import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { parse } from "yaml";

const WORKFLOW_DIR = ".github/workflows";
const WEB_CONSOLE = "apps/web-console";
const MANIFEST_PATH = `${WEB_CONSOLE}/playwright-spec-manifest.json`;
const ALLOWLIST_PATH = `${WEB_CONSOLE}/tests/dark-spec-allowlist.json`;
const PACKAGE_PATH = `${WEB_CONSOLE}/package.json`;
const DEFAULT_CONFIG = "playwright.config.ts";

const STATES = ["pr", "other", "dark"];
const DECLARABLE_STATES = ["other", "dark"];

// Flags taking a separate value when not written as --flag=value.
const VALUE_FLAGS = new Set([
  "--config",
  "-c",
  "--project",
  "--grep",
  "-g",
  "--grep-invert",
  "--reporter",
  "--workers",
  "-j",
  "--retries",
  "--timeout",
  "--global-timeout",
  "--output",
  "--shard",
  "--repeat-each",
  "--max-failures",
  "--trace",
  "--tsconfig",
]);

// Flags that narrow a run to fewer tests than its projects contain. The guard
// models `--config` and `--project` and nothing else, so it refuses these
// rather than crediting the invocation with files it may never touch.
const NARROWING_FLAGS = new Set([
  "--grep",
  "-g",
  "--grep-invert",
  "--last-failed",
  "--only-changed",
  "--no-deps",
  "--max-failures",
  "-x",
]);

// Flags that cannot change which spec files are collected. `--shard` is here
// on the assumption that a sharded workflow runs every shard; a matrix that
// runs some of them is a different hole, and not one this guard models.
const NEUTRAL_FLAGS = new Set([
  "--reporter",
  "--workers",
  "-j",
  "--retries",
  "--timeout",
  "--global-timeout",
  "--output",
  "--shard",
  "--repeat-each",
  "--trace",
  "--tsconfig",
  "--headed",
  "--debug",
  "--ui",
  "--list",
  "--quiet",
  "--forbid-only",
  "--fully-parallel",
  "--ignore-snapshots",
  "--update-snapshots",
  "-u",
  "--pass-with-no-tests",
]);

const PR_EVENTS = ["pull_request", "pull_request_target"];
// GitHub's defaults when `on.pull_request.types` is omitted, plus the one a
// draft marked ready for review fires. A `pull_request` trigger listing none
// of these, `types: [labeled]` being the case in this repository, does not
// react to an ordinary pull request push.
const ORDINARY_PR_TYPES = ["opened", "synchronize", "reopened", "ready_for_review"];

const failures = [];
const fail = (message) => failures.push(message);

// ---- workflow reading ---------------------------------------------------

// `on:` parses to the boolean true under YAML 1.1, so read the raw key set
// rather than trusting the parsed key name. Same trick as
// .github/ci/lint-workflow-check-names.mjs.
function onBlock(doc) {
  return doc?.on ?? doc?.[true] ?? doc?.True;
}

function reactsToOrdinaryPullRequest(doc) {
  const on = onBlock(doc);
  if (!on) return false;
  if (typeof on === "string") return PR_EVENTS.includes(on);
  if (Array.isArray(on)) return on.some((event) => PR_EVENTS.includes(event));
  if (typeof on !== "object") return false;

  return PR_EVENTS.some((event) => {
    if (!(event in on)) return false;
    const config = on[event];
    const types =
      config && typeof config === "object" && !Array.isArray(config) && Array.isArray(config.types)
        ? config.types
        : ORDINARY_PR_TYPES;
    return types.some((type) => ORDINARY_PR_TYPES.includes(type));
  });
}

// Can this `if:` be true on an ordinary pull request? The guard does not
// evaluate the expression language, and it answers no whenever it cannot tell,
// so a job it misreads is reported as debt rather than as coverage.
//
// Known ceiling: a path gate such as `needs.changes.outputs.web_e2e == 'true'`
// counts as yes. web-e2e carries one, so a pull request touching no web path
// does not run those specs even though they are counted here as pull-request
// gated. That is a deliberate design in ci.yml rather than a hole, and
// modelling it would mean evaluating the `changes` job's path filters against
// a diff this guard does not have.
function survivesOrdinaryPullRequest(condition) {
  if (!condition) return true;
  if (/github\.event\.pull_request\.labels/.test(condition)) return false;
  if (/github\.event_name/.test(condition) && !/['"]pull_request['"]/.test(condition)) return false;
  return true;
}

function workflowFiles() {
  return readdirSync(WORKFLOW_DIR)
    .filter((name) => name.endsWith(".yml") || name.endsWith(".yaml"))
    .sort()
    .map((name) => join(WORKFLOW_DIR, name));
}

function* runSteps(file, doc) {
  for (const [jobId, job] of Object.entries(doc?.jobs ?? {})) {
    const jobCondition = typeof job?.if === "string" ? job.if : "";
    const jobWorkingDirectory = job?.defaults?.run?.["working-directory"] ?? "";
    for (const step of job?.steps ?? []) {
      if (typeof step?.run !== "string") continue;
      const stepCondition = typeof step?.if === "string" ? step.if : "";
      yield {
        where: `${file}#${jobId}`,
        workingDirectory: step["working-directory"] ?? jobWorkingDirectory,
        run: step.run,
        canRunOnPullRequest:
          reactsToOrdinaryPullRequest(doc) &&
          survivesOrdinaryPullRequest(jobCondition) &&
          survivesOrdinaryPullRequest(stepCondition),
      };
    }
  }
}

// One shell command per element. Line continuations are joined, comments
// dropped (a spec named in a comment runs nothing, which was the original
// false-positive half of this guard's first version), and chained commands
// split. Splitting on the operators ignores quoting, which can only ever break
// a command apart into pieces that match nothing.
function shellCommands(run) {
  return run
    .replace(/\\\n/g, " ")
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith("#"))
    .flatMap((line) => line.split(/&&|\|\||[;|]/))
    .map((command) => command.trim())
    .filter(Boolean);
}

function tokenize(text) {
  return text
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .map((token) => token.replace(/^["']|["']$/g, ""));
}

// The argv of every Playwright run a single shell command starts. An npm
// script is expanded into the argv it really runs, with any `-- <args>` the
// workflow appended kept, because dropping them would measure a narrowed run
// as if it were the whole suite.
function playwrightInvocations(command, scripts, seen = new Set()) {
  const direct = /^(?:npx\s+(?:--yes\s+|--no-install\s+)?)?(?:@playwright\/test|playwright)\s+test\b(.*)$/.exec(
    command,
  );
  if (direct) return [{ shown: command, argv: tokenize(direct[1]) }];

  const npm = /^npm\s+(?:run|run-script)\s+([A-Za-z0-9:._-]+)\s*(?:--\s+(.*))?$/.exec(command);
  if (!npm) return [];
  const [, name, extra] = npm;
  if (seen.has(name)) return []; // a script that runs itself, which npm would loop on anyway
  const body = scripts[name];
  if (typeof body !== "string") return [];

  const appended = extra ? tokenize(extra) : [];
  return shellCommands(body)
    .flatMap((inner) => playwrightInvocations(inner, scripts, new Set([...seen, name])))
    .map((invocation) => ({
      shown: `npm run ${name}${appended.length ? ` -- ${appended.join(" ")}` : ""}`,
      argv: [...invocation.argv, ...appended],
    }));
}

// ---- argv to a set of projects -----------------------------------------

function selectionOf(invocation, configs, where) {
  let config = DEFAULT_CONFIG;
  const named = [];
  const argv = invocation.argv;

  for (let i = 0; i < argv.length; i++) {
    const token = argv[i];
    if (!token.startsWith("-")) {
      fail(
        `${where}: \`${invocation.shown}\` filters by the path \`${token}\`. This guard models ` +
          "--config and --project only, so it cannot say which specs that run reaches. Select by " +
          "project instead.",
      );
      return null;
    }

    const equals = token.indexOf("=");
    const flag = equals < 0 ? token : token.slice(0, equals);
    let value = equals < 0 ? undefined : token.slice(equals + 1);
    if (value === undefined && VALUE_FLAGS.has(flag)) value = argv[++i];

    if (NARROWING_FLAGS.has(flag)) {
      fail(
        `${where}: \`${invocation.shown}\` narrows its run with \`${flag}\`, which this guard does ` +
          "not model. Crediting it with every spec in its projects would over-count. Drop the flag, " +
          "or teach this guard what it selects.",
      );
      return null;
    }
    if (flag === "--config" || flag === "-c") {
      config = value ?? "";
      continue;
    }
    if (flag === "--project") {
      named.push(value ?? "");
      continue;
    }
    if (!NEUTRAL_FLAGS.has(flag)) {
      fail(
        `${where}: \`${invocation.shown}\` passes \`${flag}\`, which this guard does not recognise. ` +
          "It fails closed rather than assume the flag cannot change which specs run.",
      );
      return null;
    }
  }

  const available = configs[config];
  if (!available) {
    fail(
      `${where}: \`${invocation.shown}\` uses the Playwright config \`${config}\`, which is not in ` +
        `${MANIFEST_PATH}. Add it to CONFIGS in ${WEB_CONSOLE}/scripts/verify-spec-collection.mjs so ` +
        "its projects are pinned, then it can be measured here.",
    );
    return null;
  }

  for (const project of named) {
    if (!available.includes(project)) {
      fail(
        `${where}: \`${invocation.shown}\` selects the project \`${project}\`, which config ` +
          `\`${config}\` does not declare. Playwright would collect nothing, so this invocation runs ` +
          `no specs at all. Config \`${config}\` has: ${available.join(", ")}.`,
      );
      return null;
    }
  }

  // No --project means every project in that config, which is what Playwright
  // does. Project dependencies (a setup project pulled in by the project that
  // needs it) are not modelled; setup files are not part of the count.
  return { config, projects: named.length > 0 ? named : available };
}

// ---- measurement --------------------------------------------------------

const manifest = JSON.parse(readFileSync(MANIFEST_PATH, "utf8"));
const specProjects = manifest.specs ?? {};
const configs = manifest.configs ?? {};
const scripts = JSON.parse(readFileSync(PACKAGE_PATH, "utf8")).scripts ?? {};

if (Object.keys(configs).length === 0) {
  console.error(
    `spec wiring guard FAILED: ${MANIFEST_PATH} declares no configs, so no invocation can be ` +
      "resolved to a project set.",
  );
  process.exit(1);
}

// Project names are matched globally below, so two configs sharing one would
// make an invocation credit specs it never touches.
const configOf = new Map();
for (const [label, projects] of Object.entries(configs)) {
  for (const project of projects) {
    if (configOf.has(project)) {
      fail(
        `project \`${project}\` is declared by both \`${configOf.get(project)}\` and \`${label}\` in ` +
          `${MANIFEST_PATH}. Project names have to be unique across configs, otherwise selecting one ` +
          "credits the specs of the other.",
      );
    }
    configOf.set(project, label);
  }
}

// Everything the manifest pins except the setup files, which exist to prepare
// state for a project rather than to assert anything. Filtering to `.spec.ts`
// instead would hardcode a filename pattern into the denominator, and the
// chromium project's testDir has no testMatch, so Playwright's default picks
// up `*.test.ts` there as well.
const specFiles = Object.keys(specProjects)
  .filter((file) => !file.endsWith(".setup.ts"))
  .sort();
if (specFiles.length === 0) {
  console.error(`spec wiring guard FAILED: ${MANIFEST_PATH} lists no spec files, so nothing is measured`);
  process.exit(1);
}

const selectedBy = new Map(specFiles.map((file) => [file, { pr: [], other: [] }]));
const invocationCount = { pr: 0, other: 0 };

for (const file of workflowFiles()) {
  const doc = parse(readFileSync(file, "utf8"));
  for (const step of runSteps(file, doc)) {
    // `npm run <script>` resolves against the package.json of the directory it
    // runs in, so only expand scripts for a step that runs in the web console.
    // A direct `npx playwright test` is still read from anywhere, and fails
    // below on its working directory rather than being quietly ignored.
    const visibleScripts = step.workingDirectory === WEB_CONSOLE ? scripts : {};
    for (const command of shellCommands(step.run)) {
      for (const invocation of playwrightInvocations(command, visibleScripts)) {
        if (step.workingDirectory !== WEB_CONSOLE) {
          fail(
            `${step.where}: \`${invocation.shown}\` runs with working-directory ` +
              `\`${step.workingDirectory || "(repository root)"}\`. Every spec this guard measures ` +
              `lives under ${WEB_CONSOLE}, so an invocation from anywhere else cannot be attributed.`,
          );
          continue;
        }
        const selection = selectionOf(invocation, configs, step.where);
        if (!selection) continue;

        const bucket = step.canRunOnPullRequest ? "pr" : "other";
        invocationCount[bucket] += 1;
        const label = `${invocation.shown} (${step.where})`;
        let matched = 0;
        for (const spec of specFiles) {
          if (!specProjects[spec].some((project) => selection.projects.includes(project))) continue;
          selectedBy.get(spec)[bucket].push(label);
          matched += 1;
        }
        // Zero collected is the phase-19 testMatch bug (#708). Silence here
        // would report the specs as dark rather than the selector as broken.
        if (matched === 0) {
          fail(
            `${step.where}: \`${invocation.shown}\` selects no spec file at all. Its selector is ` +
              "broken, which is not the same thing as there being nothing to run.",
          );
        }
      }
    }
  }
}

if (invocationCount.pr + invocationCount.other === 0) {
  console.error("spec wiring guard FAILED: found no Playwright invocation in any workflow");
  process.exit(1);
}

const stateOf = new Map(
  specFiles.map((spec) => {
    const found = selectedBy.get(spec);
    if (found.pr.length > 0) return [spec, "pr"];
    return [spec, found.other.length > 0 ? "other" : "dark"];
  }),
);

// ---- the debt ledger ----------------------------------------------------

const allowlist = JSON.parse(readFileSync(ALLOWLIST_PATH, "utf8"));
const groups = Array.isArray(allowlist.groups) ? allowlist.groups : null;
if (!groups) {
  console.error(`spec wiring guard FAILED: ${ALLOWLIST_PATH} has no \`groups\` array`);
  process.exit(1);
}

// One group per shared cause, rather than one entry per spec with a free text
// reason. An earlier version demanded a reason of at least 25 characters,
// which is a threshold set at whatever the current value happened to be:
// seven entries then shared one byte-identical sentence and passed. Stating a
// cause once, for the specs that share it, is the honest shape.
const declared = new Map();
for (const [index, group] of groups.entries()) {
  const where = `${ALLOWLIST_PATH} groups[${index}]`;
  if (!DECLARABLE_STATES.includes(group.state)) {
    fail(`${where} needs a state of ${DECLARABLE_STATES.join(" or ")}, not ${JSON.stringify(group.state)}`);
  }
  if (typeof group.reason !== "string" || group.reason.trim() === "") fail(`${where} needs a reason`);
  if (typeof group.owner !== "string" || group.owner.trim() === "") fail(`${where} needs an owner`);
  if (!Number.isInteger(group.issue)) fail(`${where} needs a tracking issue number`);
  if (!Array.isArray(group.specs) || group.specs.length === 0) fail(`${where} needs a non-empty specs list`);

  for (const spec of group.specs ?? []) {
    if (!stateOf.has(spec)) {
      fail(`${where} names \`${spec}\`, which is not a spec file in ${MANIFEST_PATH}`);
      continue;
    }
    if (declared.has(spec)) {
      fail(`${where} repeats \`${spec}\`, already declared by groups[${declared.get(spec)}]`);
      continue;
    }
    declared.set(spec, index);

    const measured = stateOf.get(spec);
    if (measured === group.state) continue;
    if (measured === "pr") {
      fail(
        `${where} carries \`${spec}\` as debt, but a pull request run selects it now. Remove it from ` +
          "the ledger in the change that wired it.",
      );
    } else {
      fail(
        `${where} declares \`${spec}\` as ${group.state}, and it measures as ${measured}. ` +
          (measured === "other"
            ? `It is selected by ${selectedBy.get(spec).other.join(", ")}, which no pull request can ` +
              "trigger. Moving a spec from dark to nightly is progress, and it is recorded here, not " +
              "silently dropped."
            : "Whatever used to select it does not any more."),
      );
    }
  }
}

for (const [spec, state] of stateOf) {
  if (state !== "pr" && !declared.has(spec)) {
    fail(
      `\`${spec}\` is ${state === "dark" ? "run by no workflow at all" : "run only outside pull requests"}` +
        `, and no group in ${ALLOWLIST_PATH} declares it.`,
    );
  }
}

// ---- report -------------------------------------------------------------

const counts = Object.fromEntries(STATES.map((state) => [state, 0]));
for (const state of stateOf.values()) counts[state] += 1;

if (failures.length > 0) {
  console.error("spec wiring guard FAILED");
  for (const failure of failures) console.error(`  ${failure}`);
  console.error("");
  console.error(
    `Measured: ${counts.pr} of ${specFiles.length} spec files run on a pull request, ` +
      `${counts.other} run only on other triggers, ${counts.dark} run nowhere.`,
  );
  console.error(`Ledger: ${ALLOWLIST_PATH}`);
  process.exit(1);
}

console.log(
  `spec wiring guard: OK, ${counts.pr}/${specFiles.length} spec files run on a pull request ` +
    `(${invocationCount.pr} invocation(s)); ${counts.other} run only on triggers a pull request ` +
    `cannot fire (${invocationCount.other} invocation(s)); ${counts.dark} run nowhere. ` +
    `The ${counts.other + counts.dark} that are not pull-request gated are declared in ${ALLOWLIST_PATH}.`,
);
console.log(
  "  Selection is not execution: a spec that skips every test on an unset variable is counted here " +
    "as run. That is camouflage shape 1, and it is measured separately.",
);
