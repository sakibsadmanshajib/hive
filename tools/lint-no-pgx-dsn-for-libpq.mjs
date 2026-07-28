#!/usr/bin/env node
// Structural guard against handing a pgx-flavoured DSN to a libpq consumer.
//
// PR #593 split the Supabase pooler DSNs and stamped `pool_max_conns` and
// `default_query_exec_mode` onto them. Those are pgx's own connection-string
// parameters, read by pgxpool before it ever talks to Postgres. libpq has no
// such connection options and rejects the entire DSN the moment it meets an
// unknown one, so Open WebUI's psycopg2 answered:
//
//   sqlalchemy.exc.ProgrammingError: (psycopg2.ProgrammingError) invalid dsn:
//   invalid connection option "pool_max_conns"
//
// The container never became healthy, the deploy failed on `dependency failed
// to start: container hive-open-webui-1 is unhealthy`, and chat-hive.scubed.co
// returned 502 for the better part of an hour. control-plane and edge-api, both
// pgx, parsed the same DSNs without complaint in the same run, which is exactly
// why nothing caught it: the asymmetry is invisible unless something checks for
// it.
//
// Three checks, all cheap and all structural:
//
//   A. Default-deny by service. Only the services listed in PGX_SERVICES may
//      reference a pgx-only DSN variable. A new service added to compose is a
//      libpq consumer until someone declares otherwise, so the failure mode is
//      a lint error rather than a crash-looping container.
//   B. No dangling reference. A pooler DSN variable a libpq consumer reads must
//      actually be emitted by scripts/derive-pooler-dsn.py. Renaming the
//      variable in one place and not the other would otherwise fall through to
//      the compose default silently.
//   C. No unstripped parameter. Every parameter the derivation script stamps
//      onto a DSN must appear in its own PGX_ONLY_PARAMS strip list, so adding
//      a fourth pgx parameter later cannot leak into the libpq flavour.
//
// Mirrors the pattern of tools/lint-no-token-in-proof-captures.mjs: a small
// scanner with a MUST_CATCH / MUST_ALLOW self-test that runs as a preflight on
// every invocation.

import { readFileSync, readdirSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import YAML from "yaml";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..");
const COMPOSE_DIR = join(REPO_ROOT, "deploy", "docker");
const DERIVE_SCRIPT = join(REPO_ROOT, "scripts", "derive-pooler-dsn.py");

// Every compose file, not just the base one. An overlay (enterprise, staging,
// relay, override) can set the same env key on the same service, and the last
// file on the command line wins, so a pgx-flavoured DSN reintroduced in an
// overlay would be just as fatal and twice as easy to miss.
function composeFiles() {
  return readdirSync(COMPOSE_DIR)
    .filter((f) => /^docker-compose.*\.ya?ml$/.test(f))
    .sort()
    .map((f) => join(COMPOSE_DIR, f));
}

// Services whose database driver is pgx, and which may therefore be handed a
// DSN carrying pgx's own parameters. Both are Go services on pgxpool. Every
// other service in compose is treated as a libpq consumer by default: Open
// WebUI reaches pgvector through psycopg2, and anything shelling out to psql
// parses its DSN through the same libpq.
const PGX_SERVICES = new Set(["edge-api", "control-plane"]);

// The environment variables the derivation script emits with pgx parameters
// stamped on. `SUPABASE_DB_URL` is deliberately NOT here: it is the fallback
// every profile without a Supavisor pooler depends on (local, enterprise, a
// direct Postgres), where it is a plain DSN with no parameters at all. In the
// environments where the derivation script does stamp it, that same script
// always emits the libpq flavour alongside it, so the fallback is never the
// value that actually reaches a libpq consumer.
const PGX_ONLY_VARS = new Set(["SUPABASE_DB_POOL_URL"]);

// A compose interpolation, braced or bare, at any nesting depth:
// `${A:-${B:-}}` yields ["A", "B"].
const VAR_RE = /\$\{?([A-Za-z_][A-Za-z0-9_]*)/g;

export function referencedVars(value) {
  const found = [];
  VAR_RE.lastIndex = 0;
  let match = VAR_RE.exec(value);
  while (match !== null) {
    found.push(match[1]);
    match = VAR_RE.exec(value);
  }
  return found;
}

// Reads the strip list out of the derivation script rather than restating it,
// so the two cannot drift apart.
export function parsePgxOnlyParams(source) {
  const block = source.match(/PGX_ONLY_PARAMS\s*=\s*\(([^)]*)\)/);
  assert.ok(block, "derive-pooler-dsn.py: PGX_ONLY_PARAMS tuple not found");
  return [...block[1].matchAll(/["']([^"']+)["']/g)].map((m) => m[1]);
}

// Every parameter the script stamps onto a DSN, from its with_params() calls.
// Balanced-paren scan rather than a regex, so a multi-line call and a nested
// `str(...)` argument both read correctly.
export function parseStampedParams(source) {
  const params = new Set();
  const NEEDLE = "with_params(";
  for (let idx = source.indexOf(NEEDLE); idx !== -1; idx = source.indexOf(NEEDLE, idx + 1)) {
    let depth = 1;
    let i = idx + NEEDLE.length;
    const start = i;
    while (i < source.length && depth > 0) {
      if (source[i] === "(") depth += 1;
      else if (source[i] === ")") depth -= 1;
      i += 1;
    }
    // Drop nested groups so a call's own arguments cannot be read as ours.
    let flat = source.slice(start, i - 1);
    for (let prev = null; prev !== flat; ) {
      prev = flat;
      flat = flat.replace(/\([^()]*\)/g, "");
    }
    for (const kw of flat.matchAll(/([A-Za-z_][A-Za-z0-9_]*)\s*=/g)) params.add(kw[1]);
  }
  return [...params];
}

// The name every emitted variable is printed under, from the script's own
// `print(f"NAME={...}")` lines.
export function parseEmittedVars(source) {
  return [...source.matchAll(/print\(f["']([A-Z_][A-Z0-9_]*)=\{/g)].map((m) => m[1]);
}

// One compose env value, checked against the two ways a libpq consumer can be
// handed something it cannot parse: a reference to a pgx-flavoured variable, or
// a literal DSN with the parameter written out.
export function findOffenders(value, pgxOnlyParams) {
  const offenders = [];
  for (const name of referencedVars(value)) {
    if (PGX_ONLY_VARS.has(name)) {
      offenders.push(`references the pgx-flavoured $${name}`);
    }
  }
  for (const param of pgxOnlyParams) {
    if (value.includes(`${param}=`)) {
      offenders.push(`carries the pgx-only parameter \`${param}\``);
    }
  }
  return offenders;
}

const SELF_TEST_PARAMS = ["pool_max_conns", "default_query_exec_mode"];

const MUST_CATCH = [
  // The exact line PR #593 shipped, which took chat-hive.scubed.co to 502.
  ["the #593 regression verbatim", "${SUPABASE_DB_POOL_URL:-${SUPABASE_DB_URL:-}}"],
  ["bare reference", "${SUPABASE_DB_POOL_URL}"],
  ["unbraced reference", "$SUPABASE_DB_POOL_URL"],
  ["buried in a fallback chain", "${SOMETHING_ELSE:-${SUPABASE_DB_POOL_URL}}"],
  ["literal pgx parameter", "postgresql://u:p@pooler:6543/postgres?pool_max_conns=8"],
  ["literal exec-mode parameter", "postgresql://u:p@pooler:6543/postgres?default_query_exec_mode=exec"],
];

const MUST_ALLOW = [
  // The libpq flavour shares a prefix with the banned name; matching on the
  // prefix alone would reject the fix itself.
  ["the libpq flavour with its fallback", "${SUPABASE_DB_POOL_URL_LIBPQ:-${SUPABASE_DB_URL:-}}"],
  ["the libpq flavour alone", "${SUPABASE_DB_POOL_URL_LIBPQ}"],
  ["the session DSN, which every poolerless profile falls back to", "${SUPABASE_DB_URL}"],
  ["a plain literal DSN", "postgresql://postgres:pw@supabase-db:5432/postgres"],
  ["an unrelated value", "pgvector"],
];

function selfTest() {
  for (const [name, value] of MUST_CATCH) {
    assert.ok(
      findOffenders(value, SELF_TEST_PARAMS).length > 0,
      `self-test: NOT caught -> ${name}: ${value}`,
    );
  }
  for (const [name, value] of MUST_ALLOW) {
    const offenders = findOffenders(value, SELF_TEST_PARAMS);
    assert.equal(
      offenders.length,
      0,
      `self-test: false positive -> ${name}: ${JSON.stringify(offenders)}`,
    );
  }
  assert.deepEqual(
    referencedVars("${A:-${B:-}}"),
    ["A", "B"],
    "self-test: nested interpolation not extracted",
  );
  return MUST_CATCH.length + MUST_ALLOW.length + 1;
}

function main() {
  let assertions;
  try {
    assertions = selfTest();
  } catch (err) {
    console.error(`lint-no-pgx-dsn-for-libpq: SELF-TEST FAILED\n${err.message}`);
    process.exit(2);
  }
  console.log(`lint-no-pgx-dsn-for-libpq: self-test ok (${assertions} assertions)`);
  if (process.argv.includes("--self-test")) process.exit(0);

  const deriveSource = readFileSync(DERIVE_SCRIPT, "utf8");
  const pgxOnlyParams = parsePgxOnlyParams(deriveSource);
  const emittedVars = new Set(parseEmittedVars(deriveSource));

  let failed = false;
  const fail = (msg) => {
    console.error(msg);
    failed = true;
  };

  // C. Every parameter the script stamps must also be stripped for libpq.
  for (const param of parseStampedParams(deriveSource)) {
    if (!pgxOnlyParams.includes(param)) {
      fail(
        `scripts/derive-pooler-dsn.py: \`${param}\` is stamped onto a DSN but is not in ` +
          "PGX_ONLY_PARAMS, so it survives into the libpq flavour and libpq will reject the " +
          "whole connection string.",
      );
    }
  }

  let checked = 0;
  const files = composeFiles();
  for (const file of files) {
    const where = file.slice(REPO_ROOT.length + 1);
    const compose = YAML.parse(readFileSync(file, "utf8"));
    for (const [service, spec] of Object.entries(compose?.services ?? {})) {
      const env = spec?.environment;
      if (!env || Array.isArray(env)) continue;
      for (const [key, raw] of Object.entries(env)) {
        if (typeof raw !== "string") continue;
        const referenced = referencedVars(raw);
        const isPoolerDsn = referenced.some((n) => n.startsWith("SUPABASE_DB_"));
        if (!isPoolerDsn && !pgxOnlyParams.some((p) => raw.includes(`${p}=`))) continue;
        checked += 1;

        // A. Only a declared pgx service may read a pgx-flavoured DSN.
        if (!PGX_SERVICES.has(service)) {
          for (const offence of findOffenders(raw, pgxOnlyParams)) {
            fail(
              `${where}: service \`${service}\` env \`${key}\` ${offence}. ` +
                `\`${service}\` is not declared as a pgx consumer, so its DSN is parsed by libpq, ` +
                "which fails the whole connection on an unknown option. Use " +
                "$SUPABASE_DB_POOL_URL_LIBPQ, or add the service to PGX_SERVICES in this lint if " +
                "it really does speak pgx.",
            );
          }
        }

        // B. No reference to a pooler variable the derivation script never emits.
        for (const name of referenced) {
          if (name.startsWith("SUPABASE_DB_") && !emittedVars.has(name)) {
            fail(
              `${where}: service \`${service}\` env \`${key}\` reads $${name}, which ` +
                "scripts/derive-pooler-dsn.py does not emit. The interpolation would silently " +
                "fall through to its default instead of failing.",
            );
          }
        }
      }
    }
  }

  if (failed) {
    console.error(
      "\nlint-no-pgx-dsn-for-libpq: a DSN destined for a libpq consumer carries a pgx-only " +
        "option. psycopg2 and psql both parse through libpq, which rejects the entire " +
        "connection string on an unknown connection option rather than ignoring it.",
    );
    process.exit(1);
  }

  console.log(
    `lint-no-pgx-dsn-for-libpq: ok (${checked} DSN-bearing env values across ${files.length} ` +
      `compose files, pgx-only params: ${pgxOnlyParams.join(", ")})`,
  );
  process.exit(0);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
