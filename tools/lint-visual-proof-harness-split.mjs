#!/usr/bin/env node
// Structural guard on the harness/application split in the visual-proof
// workflows (issue #1758).
//
// Both proof workflows read their YAML from one ref and check out a different
// tree to run it against: the target pull request's. The scripts the steps
// invoke by name are therefore coupled to command lines that live in the YAML,
// and taking them from the target meant a pull request branched before a
// harness change could not be proven at all. Run 33690589909 died nine seconds
// in on `unknown argument: --oauth-server`, an argument main's YAML passed to a
// script only main carried.
//
// Each workflow now re-takes a named list of paths from its own ref. The list
// is the boundary, and it rots in two ways. Someone adds a step that calls a
// new script and does not list it. Or, one hop down and harder to see, a listed
// script reads a file from the tree that nobody listed: the review of PR #1764
// found main's install-agent-engine-host.sh installing the target's
// agent-engine-health-probe.sh and rendering the target's systemd templates,
// which is the same defect wearing a different hat. Both reintroduce it
// silently, on workflows that do not run on most pull requests. So this lint
// fails instead.
//
// Three checks per workflow:
//
//   A. Every `scripts/...` path the workflow invokes is on that workflow's
//      harness list or in FROM_TARGET below.
//   B. Every path on the harness list exists, so a rename on main is a lint
//      failure here rather than a `git checkout` failure inside a proof run.
//   C. Every repo path a listed harness script reads through its own repo-root
//      variable is on the harness list or in FROM_TARGET. This is the
//      transitive half, and FROM_TARGET is what separates a deliberate
//      target-side read from a missed one.
//
// Comment lines and the `paths:` trigger list are not invocations and are
// skipped. Follows the pattern of tools/lint-no-token-in-proof-captures.mjs: a
// small scanner with a MUST_CATCH / MUST_ALLOW self-test that runs as a
// preflight on every invocation.

import { readFileSync, existsSync, statSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..");

const WORKFLOWS = [
  ".github/workflows/chat-visual-proof.yml",
  ".github/workflows/agent-visual-proof.yml",
];

// Paths a proof workflow, or a harness script, reads from the target pull
// request's own tree on purpose. Every entry carries the reason, because
// "it is on the list" and "it is a deliberate exception" have to be told apart
// by the next person, and a bare allowlist cannot do that.
const FROM_TARGET = {
  "scripts/apply-migrations.sh":
    "reached only from ci-throwaway-db.sh with no arguments, so it has no interface with the workflow YAML, and it validates scripts/migration-baseline.conf against supabase/migrations, both of which are the target's",
  "supabase/migrations":
    "the schema under proof. A pull request that adds a migration has to be captured with that migration applied",
  "scripts/migration-baseline.conf":
    "indexes supabase/migrations and has to agree with it, so it moves with the target's migrations",
  "deploy/supabase/init/00-extensions.sql":
    "the extension set the application's own schema depends on, not something the stack script's arguments are coupled to",
  ".github/ci/test-db-bootstrap.sql":
    "stubs the roles the target's migrations expect, so it belongs with them",
  "apps/agent-engine/packs":
    "the sandbox tool packs under proof. Pinning them to main would photograph main's agent rather than the target's",
};

const SCRIPT_REF = /scripts\/[A-Za-z0-9][A-Za-z0-9._-]*/g;

// A repo path a harness script reads through its own repo-root variable.
// ponytail: three variable names rather than a general dataflow pass. A wide
// scan for any repo-shaped substring was tried and is unusable: it matches
// container image names (supabase/gotrue), URL paths (supabase/auth/v1) and
// file references in Python docstrings, which would bury the real entries in an
// allowlist nobody maintains. Add a name here when a harness script introduces
// a fourth; check C going quiet is the symptom.
const ROOTED_REF = /\$\{?(?:repo_root|REPO_DIR|TEMPLATE_DIR)\}?\/([A-Za-z0-9._][A-Za-z0-9._/-]*)/g;

// The array literal the workflow's harness step declares. Returns null when the
// step is gone, which is itself a failure: a workflow with no harness list has
// silently gone back to running the target's copies.
function harnessList(text) {
  const open = text.indexOf("harness=(");
  if (open === -1) return null;
  const close = text.indexOf(")", open);
  if (close === -1) return null;
  return text
    .slice(open + "harness=(".length, close)
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
}

// Everything that is not a comment, not a `paths:` trigger entry, and not the
// harness list itself. What is left is what the job actually runs.
function invocationText(text) {
  const open = text.indexOf("harness=(");
  const close = open === -1 ? -1 : text.indexOf(")", open);
  const withoutList =
    open === -1 ? text : text.slice(0, open) + text.slice(close + 1);
  return withoutList
    .split("\n")
    .filter((line) => !/^\s*#/.test(line))
    .filter((line) => !/^\s*-\s*'[^']*'\s*$/.test(line))
    .join("\n");
}

function uncommented(text) {
  return text
    .split("\n")
    .filter((line) => !/^\s*#/.test(line))
    .join("\n");
}

function unknownRefs(refs, known, where) {
  return [...new Set(refs)].sort().flatMap((ref) =>
    known.has(ref)
      ? []
      : [
          `${where}${ref} is on neither the harness list nor FROM_TARGET. List it as harness, or add it to FROM_TARGET with the reason it must come from the target tree.`,
        ],
  );
}

// Check A, plus the list itself.
function scanWorkflow(text) {
  const declared = harnessList(text);
  if (declared === null) {
    return {
      declared: [],
      problems: [
        "no `harness=( ... )` list found, so nothing pins the harness to this workflow's own ref (issue #1758)",
      ],
    };
  }
  const problems = declared.length === 0 ? ["the harness list is empty"] : [];
  const known = new Set([...declared, ...Object.keys(FROM_TARGET)]);
  problems.push(
    ...unknownRefs(invocationText(text).match(SCRIPT_REF) ?? [], known, "invokes "),
  );
  return { declared, problems };
}

// Check C.
function scanScript(text, known, name) {
  const refs = [...uncommented(text).matchAll(ROOTED_REF)].map((m) =>
    m[1].replace(/[./]+$/, ""),
  );
  return unknownRefs(refs, known, `${name} reads `);
}

// --- self-test preflight -----------------------------------------------------

const listed = (...paths) => ["          harness=(", ...paths.map((p) => `            ${p}`), "          )"].join("\n");

const MUST_CATCH = [
  ["a new invocation missing from the list", [listed("scripts/a.sh"), "          scripts/b.sh --go"].join("\n")],
  ["the harness step deleted outright", "          scripts/a.sh --go\n"],
  ["an empty harness list", listed()],
];

const MUST_ALLOW = [
  [
    "a comment and a paths: entry naming an unlisted script",
    ["      - 'scripts/b.sh'", "          # see scripts/c.sh for why", listed("scripts/a.sh"), "          scripts/a.sh --go"].join("\n"),
  ],
  [
    "a listed script reached by a relative path and inside a string",
    [listed("scripts/a.py"), "          cat x | python3 ../../scripts/a.py", '          echo "::error::see scripts/a.py\'s main()"'].join("\n"),
  ],
  ["a FROM_TARGET entry invoked directly", [listed("scripts/a.sh"), "          scripts/apply-migrations.sh"].join("\n")],
];

const SCRIPT_MUST_CATCH = [
  ["a transitive call to an unlisted script", '"$repo_root/scripts/unlisted.sh" --gotrue'],
  ["a transitive read of an unlisted directory", 'TEMPLATE_DIR="$REPO_DIR/deploy/unlisted"'],
];

const SCRIPT_MUST_ALLOW = [
  ["a transitive call to a listed script", 'python3 "$repo_root/scripts/a.py"'],
  ["a transitive read declared in FROM_TARGET", 'migrations_dir="$repo_root/supabase/migrations"'],
  ["a commented reference to an unlisted script", '# calls $repo_root/scripts/unlisted.sh eventually'],
  ["an image name that merely looks like a repo path", 'image=supabase/gotrue:v2'],
];

function selfTest() {
  for (const [name, fixture] of MUST_CATCH) {
    assert.ok(scanWorkflow(fixture).problems.length > 0, `self-test: should have been caught: ${name}`);
  }
  for (const [name, fixture] of MUST_ALLOW) {
    assert.deepEqual(scanWorkflow(fixture).problems, [], `self-test: should have been allowed: ${name}`);
  }
  const known = new Set(["scripts/a.py", ...Object.keys(FROM_TARGET)]);
  for (const [name, fixture] of SCRIPT_MUST_CATCH) {
    assert.ok(scanScript(fixture, known, "x").length > 0, `self-test: should have been caught: ${name}`);
  }
  for (const [name, fixture] of SCRIPT_MUST_ALLOW) {
    assert.deepEqual(scanScript(fixture, known, "x"), [], `self-test: should have been allowed: ${name}`);
  }
}

// --- main --------------------------------------------------------------------

selfTest();

let failed = false;
for (const rel of WORKFLOWS) {
  const abs = join(REPO_ROOT, rel);
  if (!existsSync(abs)) {
    console.error(`${rel}: missing. Update WORKFLOWS in tools/lint-visual-proof-harness-split.mjs if it was renamed.`);
    failed = true;
    continue;
  }
  const { problems, declared } = scanWorkflow(readFileSync(abs, "utf8"));
  const known = new Set([...declared, ...Object.keys(FROM_TARGET)]);
  for (const path of declared) {
    const target = join(REPO_ROOT, path);
    if (!existsSync(target)) {
      problems.push(`${path} is on the harness list but does not exist`);
      continue;
    }
    if (statSync(target).isFile()) {
      problems.push(...scanScript(readFileSync(target, "utf8"), known, path));
    }
  }
  if (problems.length > 0) {
    failed = true;
    for (const problem of problems) console.error(`${rel}: ${problem}`);
  } else {
    console.log(`${rel}: harness split OK (${declared.length} pinned paths)`);
  }
}

if (failed) {
  console.error(
    "\nThe visual-proof workflows take these paths from their own ref so a pull request branched before a harness change can still be proven. See issue #1758 and the comment above the harness step.",
  );
  process.exit(1);
}
