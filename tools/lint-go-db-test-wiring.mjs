#!/usr/bin/env node
// Guard for camouflage shape 1 in docs/TESTING-STANDARD.md, on the Go side.
// Issues #659, #701, #708, #797.
//
// A Go test gated on a database DSN skips when the variable is unset, and a
// skip is not a failure, so the run is green. The variable being exported
// somewhere in the workflow is not enough: it has to be available to the step
// that runs the package. Both halves of that have already failed here.
//
//   * The plain `go test ./...` step runs every package, and runs BEFORE the
//     bootstrap step that writes HIVE_TEST_DB_URL to $GITHUB_ENV. Everything
//     gated on it skips there.
//   * The later step that does have the variable names its packages in an
//     explicit list. A package missing from that list is never invoked at all.
//
// Between the two, a package can look covered twice over and execute nothing.
// That is exactly what `./internal/platform/...` did until #803: it skipped in
// the first step and was absent from the second.
//
// This lint pairs every Go test file that reads a `*_TEST_DB_URL` variable
// with a workflow step that both names its package and has that variable in
// scope. Deleting the package from the list fails here, in a required check
// that does not depend on the Go job it is protecting.
//
// Run: node tools/lint-go-db-test-wiring.mjs

import { readFileSync, readdirSync, existsSync } from "node:fs";
import { join, relative, dirname } from "node:path";
import { parse } from "yaml";

const WORKFLOW_DIR = ".github/workflows";
const SOURCE_ROOTS = ["apps", "packages"];
const DSN_VAR = /os\.Getenv\("([A-Z0-9_]*TEST_DB_URL)"\)/g;
const SKIP_DIRS = new Set(["node_modules", "vendor", ".git", "target", "dist", ".next"]);

// The debt this repository is carrying today, as `module/package: reason`.
// Every one of these is a package whose database-gated tests have never
// executed in CI. They are #797's backlog, and turning them on is a change of
// its own, because a suite that has never run is not known to pass. Listing
// them here is the point: the count is visible, the guard fails when a package
// leaves the workflow's list, and it fails again if a package is exempted here
// after it starts running.
const KNOWN_DARK = new Map([
  // audit, auditarchive, auditverifier, auditworker, licensing and
  // tests/compliance were wired into the go-tests job's live-Postgres step by
  // the 2026-08-26 un-skip pass: removed here because the lint fails if a
  // package both runs and is still declared as debt.
  // marketplace, tenant/settings and usage wired into the go-tests job's
  // live-Postgres step by issue #708: removed here because the lint fails
  // if a package both runs and is still declared as debt.
]);

const errors = [];

function walk(dir, out = []) {
  if (!existsSync(dir)) return out;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (SKIP_DIRS.has(entry.name)) continue;
    const full = join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else out.push(full);
  }
  return out;
}

function moduleRootOf(file) {
  let dir = dirname(file);
  while (dir && dir !== ".") {
    if (existsSync(join(dir, "go.mod"))) return dir;
    dir = dirname(dir);
  }
  return null;
}

// ---- what needs a DSN ---------------------------------------------------

// module directory -> package pattern -> the DSN variables its tests read.
const needed = new Map();
for (const root of SOURCE_ROOTS) {
  for (const file of walk(root)) {
    if (!file.endsWith("_test.go")) continue;
    const source = readFileSync(file, "utf8");
    const vars = [...source.matchAll(DSN_VAR)].map((match) => match[1]);
    if (vars.length === 0) continue;

    const module = moduleRootOf(file);
    if (!module) {
      errors.push(`${file} reads ${vars.join(", ")} but sits under no Go module`);
      continue;
    }
    const pkg = `./${relative(module, dirname(file))}`.split("\\").join("/");
    if (!needed.has(module)) needed.set(module, new Map());
    const packages = needed.get(module);
    if (!packages.has(pkg)) packages.set(pkg, new Map());
    for (const name of vars) {
      if (!packages.get(pkg).has(name)) packages.get(pkg).set(name, []);
      packages.get(pkg).get(name).push(file);
    }
  }
}

// ---- what the workflows run --------------------------------------------

// The concrete matrix legs a job runs, so `working-directory: ${{ matrix.path }}`
// resolves to real directories. Only the `include:`-only shape is handled,
// which is the one this repository uses; a bare-axis matrix would need the
// cartesian product from .github/ci/lint-workflow-check-names.mjs.
function matrixLegs(job) {
  const include = job?.strategy?.matrix?.include;
  return Array.isArray(include) && include.length > 0 ? include : [{}];
}

function resolveDirectories(template, legs) {
  if (!template) return [""];
  if (!template.includes("${{")) return [normalizeDir(template)];
  return legs
    .map((leg) =>
      template.replace(/\$\{\{\s*matrix\.([A-Za-z0-9_]+)\s*\}\}/g, (whole, key) =>
        key in leg ? String(leg[key]) : whole,
      ),
    )
    .filter((value) => !value.includes("${{"))
    .map(normalizeDir);
}

const normalizeDir = (value) => value.replace(/^\.\//, "").replace(/\/$/, "");

// A `go test` line inside a shell `if [ "${{ matrix.KEY }}" = "VALUE" ]` /
// `else` block only ever runs for the matrix leg(s) that branch selects.
// Without this, two branches naming the same package pattern (the real shape
// of the control-plane/edge-api RLS step below) can credit a leg whose branch
// never reaches that command at all, because every invocation in the step
// otherwise carries every leg's directory regardless of which branch wraps it.
//
// Unrecognised shapes (a different comparison operator, no enclosing `if`,
// something this has not been taught) return every leg unchanged, which is
// the pre-existing, permissive behaviour: narrowing wrongly is worse than not
// narrowing, and this guard already fails closed elsewhere by declaring debt
// rather than inventing coverage, not by rejecting shapes it cannot read.
// Also handles an `elif` chain, and a nested, unrelated `if ... fi` (e.g.
// gating on a plain shell variable, nothing to do with the matrix) that
// closes before the `go test` line: the backward scan tracks how many `fi`s
// it still owes a matching `if` before treating one as its own enclosing
// header, so a completed nested block does not read as "outside every block".
//
// Known ceiling: the trailing `else` of a chain is modelled as "every leg no
// `if`/`elif` in this chain named", not as the specific leg(s) the workflow
// author meant. A leg the surrounding step's own YAML `if:` already excludes
// (this repository's RLS step runs only for `matrix.module in
// (control-plane, edge-api)`, two of its four legs) is not modelled here, so
// an `else` line is still credited with every OTHER leg this shell chain
// never names, including ones the step never reaches at all. Harmless today
// because no source file under those extra legs reads a DSN variable this
// pairs on; would under-narrow the day one does. Modelling the step's own
// `if:` against the matrix is the rest of the "heavy lift" this guard's
// review named; an `elif` chain within one step, the shape reported and
// fixed here, is not the remaining gap.
function legsForLine(lines, index, legs) {
  const CONDITION =
    /^(if|elif)\s+\[\s*"?\$\{\{\s*matrix\.([A-Za-z0-9_]+)\s*\}\}"?\s*==?\s*"([^"]+)"\s*\]/;
  let inElse = false;
  let depth = 0; // unmatched `fi`s seen so far scanning backward, each still owed a matching `if`
  const excluded = []; // [key, value] pairs a trailing `else` must exclude
  for (let i = index; i >= 0; i--) {
    const line = lines[i].trim();
    if (i !== index && /^fi\s*;?$/.test(line)) {
      depth += 1;
      continue;
    }
    if (depth > 0) {
      // Inside a nested, unrelated shell block that already closed before
      // our line (an `if [ "$SOME_OTHER_VAR" = ... ]; then ... fi` with
      // nothing to do with the matrix). Only an `if` here pays down the debt
      // a `fi` created; an `elif`/`else` at this depth belongs to that same
      // nested block, not to ours, and a plain command is not a header at
      // all. Bailing out on the first `fi` regardless of nesting was the
      // original bug: it credited every leg the moment ANY block closed
      // before our line, even one with no matrix condition in it at all.
      if (/^if\b/.test(line)) depth -= 1;
      continue;
    }
    if (!inElse && /^else\b/.test(line)) {
      inElse = true;
      continue;
    }
    const match = CONDITION.exec(line);
    if (!match) continue;
    const [, kind, key, value] = match;
    if (!inElse) {
      // Our own branch's header: the `if` or `elif` this line lives inside.
      return legs.filter((leg) => String(leg[key]) === value);
    }
    // Inside a trailing `else`: this if/elif is a sibling branch it excludes.
    // Keep walking to collect every sibling in the chain, stopping once the
    // chain's own opening `if` is reached (an `elif` cannot start a chain).
    excluded.push([key, value]);
    if (kind === "if") {
      return legs.filter((leg) => excluded.every(([k, v]) => String(leg[k]) !== v));
    }
  }
  return legs;
}

// A step exports a variable to every later step in its job by appending to
// $GITHUB_ENV. Anything else is scoped to the step or the job.
function exportedToGithubEnv(run) {
  const names = new Set();
  for (const line of run.split("\n")) {
    if (!/>>\s*"?\$\{?GITHUB_ENV\}?"?/.test(line)) continue;
    for (const match of line.matchAll(/([A-Z][A-Z0-9_]*)=/g)) names.add(match[1]);
  }
  return names;
}

// Every `go test` invocation, with the directories it can run in and the DSN
// variables that are actually in scope at that point in the job.
const invocations = [];
for (const file of readdirSync(WORKFLOW_DIR).filter((f) => f.endsWith(".yml") || f.endsWith(".yaml"))) {
  const path = join(WORKFLOW_DIR, file);
  const doc = parse(readFileSync(path, "utf8"));
  for (const [jobId, job] of Object.entries(doc?.jobs ?? {})) {
    const legs = matrixLegs(job);
    const jobDefault = job?.defaults?.run?.["working-directory"] ?? "";
    const inScope = new Set(Object.keys(job?.env ?? {}));

    for (const step of job?.steps ?? []) {
      if (typeof step?.run !== "string") continue;
      const available = new Set([...inScope, ...Object.keys(step?.env ?? {})]);
      const runLines = step.run.replace(/\\\n/g, " ").split("\n");

      for (const [index, line] of runLines.entries()) {
        const command = line.trim();
        if (command.startsWith("#") || !/\bgo test\b/.test(command)) continue;
        const scopedLegs = legsForLine(runLines, index, legs);
        invocations.push({
          where: `${path}#${jobId}`,
          directories: resolveDirectories(step["working-directory"] ?? jobDefault, scopedLegs),
          patterns: command.split(/\s+/).filter((token) => token.startsWith("./")),
          available,
        });
      }

      // Applies to later steps only, which is the whole point.
      for (const name of exportedToGithubEnv(step.run)) inScope.add(name);
    }
  }
}

if (invocations.length === 0) {
  console.error(`go db test wiring FAILED: found no \`go test\` command under ${WORKFLOW_DIR}`);
  process.exit(1);
}

// ---- the pairing --------------------------------------------------------

// `./...` covers everything, `./internal/x/...` covers that tree, and a bare
// `./internal/x` covers only itself.
function covers(pattern, pkg) {
  if (pattern === "./...") return true;
  if (pattern.endsWith("/...")) {
    const prefix = pattern.slice(0, -4);
    return pkg === prefix || pkg.startsWith(`${prefix}/`);
  }
  return pattern === pkg;
}

let paired = 0;
const stillDark = new Set();
for (const [module, packages] of needed) {
  for (const [pkg, vars] of packages) {
    for (const [name, files] of vars) {
      const key = `${module}/${pkg}`;
      const match = invocations.find(
        (invocation) =>
          invocation.directories.includes(module) &&
          invocation.available.has(name) &&
          invocation.patterns.some((pattern) => covers(pattern, pkg)),
      );
      if (match) {
        if (KNOWN_DARK.has(key)) {
          errors.push(
            `${key.replace("/./", "/")} is exempted in KNOWN_DARK, and it runs now. Remove the ` +
              "exemption in the change that wired it, otherwise the count of what is dark is wrong " +
              "in the flattering direction.",
          );
        }
        paired += 1;
        continue;
      }
      if (KNOWN_DARK.has(key)) {
        stillDark.add(key);
        continue;
      }

      const named = invocations.filter(
        (invocation) =>
          invocation.directories.includes(module) &&
          invocation.patterns.some((pattern) => covers(pattern, pkg)),
      );
      errors.push(
        `${module}/${pkg.slice(2)} reads ${name} in ${files.map((f) => relative(module, f)).join(", ")}, ` +
          (named.length > 0
            ? `and the only \`go test\` step that names it (${named[0].where}) does not have ${name} in ` +
              "scope. A step that runs before the one exporting the variable skips every test gated on it."
            : `and no \`go test\` step in ${WORKFLOW_DIR} both names that package and has ${name} in scope. ` +
              "Every test in it skips silently, which is indistinguishable from passing."),
      );
    }
  }
}

if (errors.length > 0) {
  console.error("go db test wiring FAILED:");
  for (const error of [...new Set(errors)]) console.error(`  - ${error}`);
  process.exit(1);
}

console.log(
  `go db test wiring OK: ${paired} package/variable pair(s) across ${needed.size} module(s), each named by a ` +
    "`go test` step that has the variable in scope. " +
    `${stillDark.size} more are carried as known debt in KNOWN_DARK and have never executed:`,
);
for (const key of [...stillDark].sort()) console.log(`  ${key.replace("/./", "/")}`);
