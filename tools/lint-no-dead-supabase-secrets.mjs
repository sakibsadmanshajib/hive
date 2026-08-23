#!/usr/bin/env node
// Structural guard against a workflow configuring itself from the retired
// hosted Supabase project's repository secrets.
//
// What happened (issue #1059, issue #1055, issue #1050). The hosted Supabase
// project was deleted during the move to the self-hosted data plane. Ten
// repository secrets name that project. Six workflows read them, and none of
// them noticed:
//
//   * `agent-visual-proof` and `owui-nightly` failed loudly for four days,
//     with `socket.gaierror: Name or service not known` and Supavisor's
//     `tenant/user *** not found` in the logs. Loud, but only after the fact.
//   * `chat-coverage`'s live sweep and `demo-chat-settings-check` are gated to
//     manual or labelled runs, so they were configured against a project that
//     no longer exists and nothing said so.
//   * `deploy-web-console-workers` was GREEN, on every push touching
//     apps/web-console, while baking the deleted project's origin into the
//     Cloudflare Workers bundle it shipped. A green deploy over a dead backend
//     is the worst of the four shapes: nothing fails and nothing alerts.
//
// Why a lint rather than repointing the secrets. Both correct routes for a
// workflow are already in this repository and neither goes through a shared
// secret naming one project:
//
//   1. A job that needs a Supabase stands up its own, per run, with
//      scripts/ci-supabase-stack.sh (what PR #983 did for ci.yml and PR #1053
//      for the Cowork proof job). Self-contained, no shared mutable state.
//   2. A job that must run against the deployment reads the deployment's own
//      configuration: the public auth origin is public, and the deploy
//      workflow's own steps already read the box's .env on its self-hosted
//      runner rather than carrying a copy in a secret.
//
// Leaving the dead secrets readable is what lets the next workflow adopt them
// by copy-paste, which is how six of them got there. Once nothing reads them
// they can be deleted outright, which is the end state issue #1055 asks for.
//
// One check, deliberately narrow: no workflow file may reference any of the
// named dead secrets. The names are enumerated rather than pattern-matched on
// a `SUPABASE_` prefix, so a correctly provisioned, differently named secret
// for the self-hosted deployment passes without an exemption.
//
// Mirrors tools/lint-no-pgx-dsn-for-libpq.mjs and
// tools/lint-no-token-in-proof-captures.mjs: a small scanner with a
// MUST_CATCH / MUST_ALLOW self-test that runs as a preflight on every
// invocation, so the guard cannot rot into something that only ever passes.

import { readFileSync, readdirSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..");
const WORKFLOW_DIR = join(REPO_ROOT, ".github", "workflows");

// The repository secrets that name the deleted hosted project. Every one still
// carried an April 2026 timestamp at the time this guard was written; none was
// repointed after the cutover.
const DEAD_SECRETS = [
  "SUPABASE_URL",
  "SUPABASE_ANON_KEY",
  "SUPABASE_SERVICE_ROLE_KEY",
  "SUPABASE_DB_HOST",
  "SUPABASE_DB_PORT",
  "SUPABASE_DB_USER",
  "SUPABASE_DB_NAME",
  "SUPABASE_DB_PASSWORD",
  "NEXT_PUBLIC_SUPABASE_URL",
  "NEXT_PUBLIC_SUPABASE_ANON_KEY",
];

// Workflows still to be moved, each pinned to the issue that owns the move.
// This list must only ever shrink. An entry with no issue behind it is a
// permanent exemption wearing a temporary hat.
const PENDING = new Map([
  ["owui-nightly.yml", "#1055"],
]);

// `secrets.NAME`, `secrets['NAME']`, `secrets["NAME"]`. GitHub accepts all
// three inside an expression, and only matching the dotted form would let the
// bracket form back in.
function secretRefRe(name) {
  return new RegExp(
    String.raw`secrets\s*(?:\.\s*${name}\b|\[\s*['"]${name}['"]\s*\])`,
    "g",
  );
}

// Only a reference inside a `${{ }}` expression configures anything. Prose is
// left alone on purpose: the comment explaining why a workflow does NOT read
// `secrets.SUPABASE_URL` has to be able to name it, and a guard that forbids
// its own rationale gets deleted rather than obeyed. Expressions are matched
// across newlines because GitHub allows one to span lines inside a block
// scalar.
const EXPRESSION_RE = /\$\{\{([\s\S]*?)\}\}/g;

export function findOffenders(text) {
  const offenders = [];
  EXPRESSION_RE.lastIndex = 0;
  let match = EXPRESSION_RE.exec(text);
  while (match !== null) {
    const body = match[1];
    const line = text.slice(0, match.index).split("\n").length;
    for (const name of DEAD_SECRETS) {
      if (secretRefRe(name).test(body)) {
        offenders.push({
          line,
          secret: name,
          text: match[0].replace(/\s+/g, " ").trim(),
        });
      }
    }
    match = EXPRESSION_RE.exec(text);
  }
  return offenders;
}

const MUST_CATCH = [
  ["dotted reference", "      SUPABASE_URL: ${{ secrets.SUPABASE_URL }}"],
  [
    "service-role key",
    "          SUPABASE_SERVICE_ROLE_KEY: ${{ secrets.SUPABASE_SERVICE_ROLE_KEY }}",
  ],
  [
    "NEXT_PUBLIC mirror",
    "      NEXT_PUBLIC_SUPABASE_ANON_KEY: ${{ secrets.NEXT_PUBLIC_SUPABASE_ANON_KEY }}",
  ],
  [
    "database part secret",
    "          SUPABASE_DB_PASSWORD: ${{ secrets.SUPABASE_DB_PASSWORD }}",
  ],
  [
    "single-quoted bracket form",
    "      SUPABASE_URL: ${{ secrets['SUPABASE_URL'] }}",
  ],
  [
    "double-quoted bracket form",
    '      SUPABASE_URL: ${{ secrets["SUPABASE_URL"] }}',
  ],
  ["extra whitespace inside the expression", "      X: ${{ secrets . SUPABASE_ANON_KEY }}"],
  [
    "expression split across lines in a block scalar",
    "      X: >-\n        ${{ github.event_name == 'push'\n            && secrets.SUPABASE_URL\n            || '' }}",
  ],
  [
    "buried in a longer expression",
    "      X: ${{ vars.CONSOLE_URL || secrets.SUPABASE_URL }}",
  ],
];

const MUST_ALLOW = [
  // The variable name is not the problem. Setting it from a throwaway stack,
  // from the deployment's own configuration, or from $GITHUB_ENV is the fix.
  ["variable set from a throwaway stack", "      SUPABASE_URL: ${{ env.SUPABASE_URL_FROM_CONTAINER }}"],
  ["variable set from a repository variable", "      SUPABASE_URL: ${{ vars.DEMO_SUPABASE_URL }}"],
  ["literal placeholder", "          NEXT_PUBLIC_SUPABASE_URL: https://ci-placeholder.supabase.co"],
  ["shell read of the deployment's own env file", "          raw=$(grep -E '^SUPABASE_DB_URL=' \"$env_file\")"],
  ["prose naming the secret", "        # It used to run on ubuntu-latest and connect over the SUPABASE_DB_* secrets."],
  [
    "a comment explaining why the secret is NOT read",
    "      # Not secrets.SUPABASE_URL. That secret names the deleted project.",
  ],
  ["a differently named, live secret", "      SUPABASE_ANON_KEY: ${{ secrets.DEMO_SUPABASE_ANON_KEY }}"],
  ["an unrelated secret", "      LITELLM_MASTER_KEY: ${{ secrets.LITELLM_MASTER_KEY }}"],
];

function selfTest() {
  for (const [label, sample] of MUST_CATCH) {
    assert.ok(
      findOffenders(sample).length > 0,
      `self-test: expected to catch ${label}: ${sample}`,
    );
  }
  for (const [label, sample] of MUST_ALLOW) {
    const found = findOffenders(sample);
    assert.equal(
      found.length,
      0,
      `self-test: expected to allow ${label}, got ${JSON.stringify(found)}`,
    );
  }
}

function main() {
  selfTest();

  if (!existsSync(WORKFLOW_DIR)) {
    console.error(`no workflow directory at ${WORKFLOW_DIR}`);
    process.exit(1);
  }

  const files = readdirSync(WORKFLOW_DIR)
    .filter((f) => /\.ya?ml$/.test(f))
    .sort();

  let failures = 0;
  let pendingSeen = 0;

  for (const file of files) {
    const offenders = findOffenders(readFileSync(join(WORKFLOW_DIR, file), "utf8"));
    if (offenders.length === 0) continue;

    const issue = PENDING.get(file);
    if (issue) {
      pendingSeen += 1;
      console.log(
        `pending  ${file}: ${offenders.length} reference(s) to the retired project's secrets, tracked in ${issue}`,
      );
      continue;
    }

    failures += offenders.length;
    for (const o of offenders) {
      console.error(
        `FAIL .github/workflows/${file}:${o.line} reads secrets.${o.secret}, ` +
          `which names the deleted hosted Supabase project.\n      ${o.text}`,
      );
    }
  }

  // A pending entry whose workflow no longer offends is a stale exemption, and
  // a stale exemption is how the next one gets added without argument.
  for (const [file, issue] of PENDING) {
    if (!files.includes(file)) {
      console.error(
        `FAIL PENDING names .github/workflows/${file} (${issue}), which does not exist. Remove the entry.`,
      );
      failures += 1;
    }
  }
  if (pendingSeen !== PENDING.size) {
    console.error(
      `FAIL ${PENDING.size - pendingSeen} PENDING entr(y|ies) no longer read a retired secret. Remove them.`,
    );
    failures += 1;
  }

  if (failures > 0) {
    console.error(
      `\n${failures} problem(s). A job that needs a Supabase stands up its own with ` +
        `scripts/ci-supabase-stack.sh; a job that must run against the deployment reads ` +
        `the deployment's own configuration. See issue #1059.`,
    );
    process.exit(1);
  }

  console.log(
    `ok: ${files.length} workflow file(s) scanned, ${PENDING.size} pending, no unexempted reference to the retired project's secrets`,
  );
}

main();
