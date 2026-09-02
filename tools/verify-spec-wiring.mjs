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
// 1. It never executes a `--config` path taken from workflow text. An
//    earlier version ran `playwright test --list` for every invocation it
//    parsed out of a workflow, including that `--config` argument verbatim.
//    Playwright IMPORTS a config file, so that turned editing a `run:` line
//    into arbitrary code execution inside a required check. A workflow's
//    `--config` is checked against KNOWN_CONFIGS, a small fixed list this
//    file maintains, and only a config on that list is ever handed to a
//    live Playwright process (see projectsOf). Which specs a config's
//    projects collect still comes from
//    apps/web-console/playwright-spec-manifest.json, verified against a
//    live `--list` by apps/web-console/scripts/verify-spec-collection.mjs in
//    the "Web console" job; which PROJECTS a config declares no longer does
//    (a hand-maintained copy of that went stale under PR #799 and produced
//    a false positive here), and is asked of Playwright directly instead,
//    in the same job, now that this guard runs there rather than in the
//    dependency-light "Repo policy lints" job.
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
import { spawnSync } from "node:child_process";
import { parse } from "yaml";

const WORKFLOW_DIR = ".github/workflows";
const WEB_CONSOLE = "apps/web-console";
const MANIFEST_PATH = `${WEB_CONSOLE}/playwright-spec-manifest.json`;
const ALLOWLIST_PATH = `${WEB_CONSOLE}/tests/dark-spec-allowlist.json`;
const PACKAGE_PATH = `${WEB_CONSOLE}/package.json`;
const DEFAULT_CONFIG = "playwright.config.ts";

// The config files this guard knows how to ask Playwright about. Fixed and
// developer-maintained, the same trust boundary CONFIGS in
// verify-spec-collection.mjs already accepts: a workflow's own `--config`
// text is never passed to a live Playwright process, only checked against
// this list, because Playwright imports whatever config path it is given,
// and running arbitrary workflow-authored text through that import is
// exactly the code-execution hole named at the top of this file.
const KNOWN_CONFIGS = [
  DEFAULT_CONFIG,
  "e2e/phase-19/owui/playwright.owui.config.ts",
  "e2e/chat-coverage/playwright.chat-coverage.config.ts",
];

// A project name Playwright will never have, so --project=<this> always
// fails and always names every real project in its own error, regardless of
// how any project's testMatch is gated.
const NONEXISTENT_PROJECT_PROBE = "__verify-spec-wiring-nonexistent-probe__";

// Asks Playwright itself which projects a config declares, instead of
// trusting a hand-maintained copy. playwright-spec-manifest.json used to pin
// this in a `configs` object; PR #799 added a project to
// playwright.config.ts while a sibling branch's copy of that object was
// mid-flight, and this guard blamed the workflow that correctly selected the
// new project instead of its own stale copy. No live credentials are
// needed: project declaration does not depend on the env-gated testMatch
// some projects carry.
//
// Method note: the default reporters (html, and this repository's own
// flake-reporter) swallow this output; --reporter=list must be explicit, or
// the probe silently sees nothing instead of failing loudly. Requires
// apps/web-console's own node_modules (Playwright installed), which is why
// this guard now runs in the "Web console" CI job rather than the
// dependency-light "Repo policy lints" job.
function projectsOf(configPath) {
  const args = configPath === DEFAULT_CONFIG ? [] : [`--config=${configPath}`];
  const run = spawnSync(
    "npx",
    ["playwright", "test", `--project=${NONEXISTENT_PROJECT_PROBE}`, "--list", "--reporter=list", ...args],
    { cwd: WEB_CONSOLE, encoding: "utf8" },
  );
  const match = /Available projects:\s*(.+)/.exec(`${run.stdout ?? ""}${run.stderr ?? ""}`);
  if (!match) return null;
  return match[1]
    .split(",")
    .map((entry) => entry.trim().replace(/^"|"$/g, ""))
    .filter(Boolean);
}

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

// The command heads this guard knows how to read. A container's command is
// only read when its FIRST token is one of these; see commandInsideContainer.
const COMMAND_HEADS = new Set(["npm", "npx", "playwright", "@playwright/test"]);

// `docker run` options, split by whether they take the token after them. The
// list is what this repository's workflows plausibly pass, not all of Docker:
// anything outside it is refused rather than guessed at, the same posture
// selectionOf takes with Playwright's own flags.
const DOCKER_VALUE_OPTIONS = new Set([
  "--network", "--user", "-u", "-e", "--env", "--env-file", "-v", "--volume",
  "--mount", "-w", "--workdir", "--name", "--shm-size", "--ipc", "--platform",
  "--pull", "--add-host", "--label", "-l", "--memory", "-m", "--cpus",
  "--tmpfs", "--dns", "--security-opt", "--cap-add", "--cap-drop", "-p",
  "--publish", "--entrypoint", "--restart", "--hostname", "-h",
]);
const DOCKER_BOOLEAN_OPTIONS = new Set([
  "--rm", "-i", "-t", "-it", "-ti", "--init", "--privileged", "--quiet", "-d",
  "--detach", "--read-only", "--interactive", "--tty",
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

// Can this `if:` be true on an ordinary pull request?
//
// THE CEILING, STATED PLAINLY: this function proves survival only for a
// small, closed set of atoms this repository's own workflows actually use,
// composed with `&&` and `||`:
//
//   true                                          a literal, unconditional
//   always()                                      a required job's own
//     job-level condition; imposes no restriction of its own, see the
//     comment on isKnownSurvivingAtom
//   needs.<job>.outputs.<x> != 'false'             a path gate, either
//   needs.<job>.outputs.<x> == 'true'              polarity ci.yml uses
//   github.event_name == 'pull_request(_target)'   the event itself
//   github.event.pull_request.head.repo.full_name  same-repo (non-fork) PR
//     == github.repository
//
// Everything else is refused, meaning the condition is treated as NOT
// surviving: every negated form (`!=` the other direction, `!contains(...)`,
// `!startsWith(...)`, `!endsWith(...)`, a grouped `!(...)` of any kind),
// `github.event.action`, a label check, a bare `false`, or any expression
// this has not been taught. `!` is never credited, at any position: proving
// a negation survives requires knowing what it negates is a confirmed hard
// exclusion, and telling "confirmed excluded" apart from "merely
// unrecognised" is exactly the unbounded semantics problem this rewrite
// stops chasing. A negation is therefore always not-surviving, even one a
// human could work out is actually safe.
//
// WHY REFUSING IS THE SAFE DIRECTION: four separate review passes each found
// a distinct negated or composed shape reaching the old presence-only
// fallback below and being credited as coverage: `!=`, then a literal
// `false` plus negated contains/startsWith, then a caller-side
// boolean-coercion bug (see conditionOf), then grouped `!(...)`. Each fix
// closed one shape and left the category open, because GitHub Actions
// expressions nest, negate, group, and compose with `&&`, `||`, `contains`,
// `startsWith`, `endsWith` and `!` without a finite list of shapes a pattern
// match can enumerate; the next author who writes a condition slightly
// differently reopens it. Failing closed by default inverts which mistake
// is silent: an unrecognised condition now reports a spec as debt for a
// human to clear, rather than silently crediting pull-request coverage this
// narrow a guard was never able to prove.
function isKnownSurvivingAtom(trimmed) {
  if (/^true$/i.test(trimmed)) return true;
  // A required job's own job-level `if:` (see .github/ci/lint-workflow-check-
  // names.mjs Check 3) is the literal string `always()`, on every required
  // job in ci.yml (go-tests, repo-policy-lints, web-unit, agent-console-unit,
  // live-integration, web-e2e). It exists so the job still runs, and still
  // reports its own conclusion, when a job it `needs:` failed or was
  // cancelled — GitHub's default job-level gating would otherwise skip it
  // outright. It imposes no restriction of its own: real gating for a
  // required job lives entirely in its step-level `if:`, which this function
  // (via evaluate's AND) still has to clear on its own merits. Crediting the
  // literal `always()` atom here does not widen what counts as surviving; it
  // only stops a required job's mandatory job-level condition from masking
  // whatever its step conditions actually say.
  if (/^always\(\)$/.test(trimmed)) return true;
  if (/^needs\.[\w-]+\.outputs\.[\w-]+\s*!=\s*['"]false['"]$/.test(trimmed)) return true;
  if (/^needs\.[\w-]+\.outputs\.[\w-]+\s*==\s*['"]true['"]$/.test(trimmed)) return true;
  if (/^github\.event_name\s*==\s*['"]pull_request(_target)?['"]$/.test(trimmed)) return true;
  if (/^github\.event\.pull_request\.head\.repo\.full_name\s*==\s*github\.repository$/.test(trimmed)) return true;
  return false;
}

// Removes one matching, whole-string-enclosing pair of parentheses:
// "(A && B)" -> "A && B". Leaves "(A) && (B)" alone, since those parens do
// not enclose the whole expression (the depth count returns to zero before
// the string ends).
function stripOuterParens(expr) {
  if (!(expr.startsWith("(") && expr.endsWith(")"))) return expr;
  let depth = 0;
  for (let i = 0; i < expr.length; i++) {
    if (expr[i] === "(") depth += 1;
    else if (expr[i] === ")") {
      depth -= 1;
      if (depth === 0 && i < expr.length - 1) return expr;
    }
  }
  return expr.slice(1, -1).trim();
}

// Splits on every top-level occurrence of `&&` or `||`, ignoring any that sit
// inside parentheses. Returns [expr] unchanged if the operator never appears
// at depth zero, which is how the caller detects "there is nothing to split".
function splitTopLevel(expr, op) {
  const parts = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < expr.length; i++) {
    const ch = expr[i];
    if (ch === "(") depth += 1;
    else if (ch === ")") depth -= 1;
    else if (depth === 0 && expr.slice(i, i + op.length) === op) {
      parts.push(expr.slice(start, i).trim());
      i += op.length - 1;
      start = i + 1;
    }
  }
  parts.push(expr.slice(start).trim());
  return parts;
}

// `||` binds loosest, so it is split first; each side only has to survive on
// its own for the whole to. `&&` binds tighter: every side has to survive.
// `!` and a bare atom are the leaves once no top-level combinator remains.
function evaluate(expr) {
  const trimmed = stripOuterParens(expr.trim());

  const orParts = splitTopLevel(trimmed, "||");
  if (orParts.length > 1) return orParts.some((part) => evaluate(part));

  const andParts = splitTopLevel(trimmed, "&&");
  if (andParts.length > 1) return andParts.every((part) => evaluate(part));

  if (trimmed.startsWith("!")) return false; // see the ceiling comment above

  return isKnownSurvivingAtom(trimmed);
}

export function survivesOrdinaryPullRequest(condition) {
  if (!condition) return true;
  return evaluate(String(condition).trim());
}

// A job or step `if:` normalized to the string this guard reasons about. YAML
// parses an unquoted `false`/`true` to a JS boolean, not a string, so a naive
// `typeof x === "string" ? x : ""` silently turns a real `if: false` (a
// disabled step) into "" (no condition, i.e. always runs) and credits it as
// pull-request coverage. Booleans round-trip through their string form;
// anything else that is not already a string normalizes to "", same as
// before.
export function conditionOf(raw) {
  if (typeof raw === "boolean") return String(raw);
  return typeof raw === "string" ? raw : "";
}

function workflowFiles() {
  return readdirSync(WORKFLOW_DIR)
    .filter((name) => name.endsWith(".yml") || name.endsWith(".yaml"))
    .sort()
    .map((name) => join(WORKFLOW_DIR, name));
}

function* runSteps(file, doc) {
  for (const [jobId, job] of Object.entries(doc?.jobs ?? {})) {
    const jobCondition = conditionOf(job?.if);
    const jobWorkingDirectory = job?.defaults?.run?.["working-directory"] ?? "";
    for (const step of job?.steps ?? []) {
      if (typeof step?.run !== "string") continue;
      const stepCondition = conditionOf(step?.if);
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

/**
 * The command a `docker run` starts, or null when the command is not one, or
 * does not start anything this guard can read.
 *
 * A containerised run is still a run. deploy-demo-box.yml's
 * agent-workspace-coverage job runs its probe inside a container attached to
 * the stack's compose network, because the deployment's admin API is refused
 * at the public origin and reachable only from in there (issue #1531). Without
 * this, that invocation parses as nothing, its spec measures as dark, and the
 * guard reports the exact opposite of the truth about a suite that does run.
 *
 * The container's own options are skipped rather than modelled, which is a
 * deliberately weaker claim than the one selectionOf makes about Playwright's
 * flags: a bind mount, a network, an environment variable or a user cannot
 * change which spec files Playwright collects. One option can, by replacing
 * the command outright, so `--entrypoint` is refused instead of skipped.
 *
 * The image boundary is parsed rather than guessed at, because an option VALUE
 * can be any word at all. An earlier version of this function scanned for the
 * first `npm`/`npx`/`playwright` token instead, and `docker run -e npm
 * image:tag npx playwright test` then returned `npm image:tag npx playwright
 * test`, which matches no invocation pattern and is dropped in silence: a real
 * run measured as no run, which is this guard's whole failure mode. Found by
 * CodeRabbit on this PR.
 *
 * @param {string} command
 * @returns {string | { unmodelled: string } | null}
 */
// Fail closed, but only where it could be hiding something. An option this
// function cannot read leaves the image boundary unknown, so a Playwright
// command after it would be credited or dropped by guesswork and has to be
// reported. A container running something else entirely is not this guard's
// business: ci.yml runs `docker run --entrypoint caddy ... validate`, and
// refusing that would be a false positive on every future container step too.
function refuseIfItCouldBeAPlaywrightRun(rest, flag) {
  return rest.some((token) => COMMAND_HEADS.has(token)) ? { unmodelled: flag } : null;
}

export function commandInsideContainer(command) {
  const match = /^docker\s+run\s+(.*)$/.exec(command.trim());
  if (!match) return null;
  const tokens = tokenize(match[1]);

  let i = 0;
  for (; i < tokens.length; i++) {
    const token = tokens[i];
    if (!token.startsWith("-")) break; // the image, so the command follows it
    const flag = token.includes("=") ? token.slice(0, token.indexOf("=")) : token;
    if (flag === "--entrypoint") return refuseIfItCouldBeAPlaywrightRun(tokens.slice(i), "--entrypoint");
    if (token.includes("=")) continue; // --flag=value carries its own operand
    if (DOCKER_VALUE_OPTIONS.has(flag)) {
      i += 1;
      continue;
    }
    if (DOCKER_BOOLEAN_OPTIONS.has(flag)) continue;
    return refuseIfItCouldBeAPlaywrightRun(tokens.slice(i), flag);
  }

  const inner = tokens.slice(i + 1);
  if (inner.length === 0 || !COMMAND_HEADS.has(inner[0])) return null;
  return inner.join(" ");
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
        "KNOWN_CONFIGS in this file. Add it there so this guard can ask Playwright about its " +
        "projects, then it can be measured here.",
    );
    return null;
  }

  for (const project of named) {
    if (!available.includes(project)) {
      fail(
        `${where}: \`${invocation.shown}\` selects the project \`${project}\`, which config ` +
          `\`${config}\` does not declare (Playwright has: ${available.join(", ")}). Playwright ` +
          "exits non-zero on an unknown project name, so this invocation would fail loudly rather " +
          "than run no specs quietly; it is still measured as unwired here, because a workflow step " +
          "that always fails is not coverage either.",
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

function main() {
const manifest = JSON.parse(readFileSync(MANIFEST_PATH, "utf8"));
const specProjects = manifest.specs ?? {};
const configs = Object.fromEntries(
  KNOWN_CONFIGS.map((path) => [path, projectsOf(path)]).filter(([, projects]) => projects),
);
const scripts = JSON.parse(readFileSync(PACKAGE_PATH, "utf8")).scripts ?? {};

if (Object.keys(configs).length === 0) {
  console.error(
    "spec wiring guard FAILED: asked Playwright about every config in KNOWN_CONFIGS and got no " +
      "project list back from any of them, so no invocation can be resolved to a project set. " +
      "Run one by hand: npx playwright test --project=x --list --reporter=list (cwd " +
      `${WEB_CONSOLE}) and check its output.`,
  );
  process.exit(1);
}
if (Object.keys(configs).length < KNOWN_CONFIGS.length) {
  const missing = KNOWN_CONFIGS.filter((path) => !(path in configs));
  console.error(
    `spec wiring guard FAILED: Playwright would not name its projects for: ${missing.join(", ")}. ` +
      "Run the probe in KNOWN_CONFIGS by hand against each to see why.",
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
        `project \`${project}\` is declared by both \`${configOf.get(project)}\` and \`${label}\`, ` +
          "per Playwright's own listing. Project names have to be unique across configs, otherwise " +
          "selecting one credits the specs of the other.",
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
    for (const rawCommand of shellCommands(step.run)) {
      // A container's command is read as though the step ran it directly. The
      // step's own working-directory still has to be the web console, checked
      // below, which is where the npm scripts it may name resolve from.
      const contained = commandInsideContainer(rawCommand);
      if (contained !== null && typeof contained === "object") {
        fail(
          `${step.where}: \`${rawCommand}\` runs a container with \`${contained.unmodelled}\`, ` +
            "which replaces the command it runs, so this guard cannot say which specs it reaches.",
        );
        continue;
      }
      const command = typeof contained === "string" ? contained : rawCommand;
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
}

// Guarded so tools/verify-spec-wiring.test.mjs can import the pure helpers
// above (survivesOrdinaryPullRequest) without running the real measurement
// against this repository's own workflow tree and exiting the test process.
if (import.meta.url === `file://${process.argv[1]}`) main();
