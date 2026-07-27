#!/usr/bin/env node
// Structural guard against the `0.0.0.0` redirect-origin bug family.
//
// Next.js does not build a request's URL from the Host header. With
// `experimental.trustHostHeader` unset it composes it from the server's OWN
// bind address (next/dist/server/lib/router-utils/resolve-routes.js builds
// `initUrl` from `opts.hostname` + `opts.port`, and `formatHostname('0.0.0.0')`
// returns it verbatim). Both console containers start with
// `--hostname 0.0.0.0 --port 3000`, so any route handler that derives an
// absolute redirect target from `request.url` or `request.nextUrl.origin`
// emits `http(s)://0.0.0.0:3000/...` and strands the user.
//
// This has now shipped five times in this family:
//   * PR #157 added lib/http/origin.ts and wired only 2 of 5 call sites.
//   * PR #438 fixed the *path* half (missing basePath) in the same handlers
//     and left the origin half wrong.
//   * console-hive.scubed.co/auth/callback 307ing to https://0.0.0.0:3000/console.
//   * chat-hive.scubed.co/agent-workspace/auth/callback 307ing to
//     https://0.0.0.0:3000/agent-workspace/auth/sign-in.
//   * apps/web-console/app/console/account-switch/route.ts, all four redirects.
//
// Code review alone clearly does not hold this line, so make it structural.
// Route handlers must resolve absolute targets through
// lib/http/origin.ts's resolveCanonicalOrigin, and read query params from
// `request.nextUrl.searchParams` (correct, since it comes from the request
// line) rather than reparsing `request.url`.
//
// Deliberately NOT extended to middleware.ts. Verified live against the running
// stack: a same-origin `NextResponse.redirect` from middleware is emitted with a
// relative Location ("Location: /auth/sign-in"), so middleware never leaks the
// bind address and both apps' middleware are correct as written.
//
// Mirrors the pattern of tools/lint-no-direct-*.mjs (a small, dependency-free
// source scanner wired into the repo-policy-lints CI job).

import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { dirname, resolve, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..");

const APP_DIRS = [
  join(REPO_ROOT, "apps", "web-console", "app"),
  join(REPO_ROOT, "apps", "agent-console", "app"),
];

// The two deliberate copies of the origin helper. They cannot be shared by
// import (separate npm packages, separate Docker build contexts), so this lint
// is what keeps them from drifting apart.
const ORIGIN_COPIES = [
  join(REPO_ROOT, "apps", "web-console", "lib", "http", "origin.ts"),
  join(REPO_ROOT, "apps", "agent-console", "lib", "http", "origin.ts"),
];

// Accessing `.url` on the handler's request argument, or reading an origin off
// nextUrl. `.searchParams` and `.pathname` on nextUrl are fine and not matched.
const FORBIDDEN_PATTERNS = [
  { re: /\b(?:request|req)\.url\b/, what: "request.url" },
  { re: /\bnextUrl\.origin\b/, what: "request.nextUrl.origin" },
  { re: /\bnextUrl\.href\b/, what: "request.nextUrl.href" },
  { re: /\bnextUrl\.toString\s*\(/, what: "request.nextUrl.toString()" },
];

function collectRouteHandlers(dir) {
  if (!existsSync(dir)) return [];
  const found = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      found.push(...collectRouteHandlers(full));
    } else if (entry === "route.ts" || entry === "route.tsx") {
      found.push(full);
    }
  }
  return found;
}

// Strip comments so a doc block naming the forbidden pattern (as the fixed
// handlers do, to explain why they avoid it) does not trip the lint.
function stripComments(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.replace(/\/\/.*$/, ""))
    .join("\n");
}

export function findOffenders(src) {
  const offenders = [];
  stripComments(src)
    .split("\n")
    .forEach((line, i) => {
      for (const { re, what } of FORBIDDEN_PATTERNS) {
        if (re.test(line)) {
          offenders.push({ line: i + 1, what, text: line.trim() });
        }
      }
    });
  return offenders;
}

// Compare the two helper copies on their code alone, so differing header
// comments (each names its own app) do not read as drift.
export function normalizeForDrift(src) {
  return stripComments(src).replace(/\s+/g, " ").trim();
}

function main() {
  let failed = false;

  const files = APP_DIRS.flatMap(collectRouteHandlers);
  if (files.length === 0) {
    console.error(
      "lint-no-request-url-origin: found no route handlers to scan — expected paths moved?",
    );
    process.exit(2);
  }

  for (const file of files) {
    const offenders = findOffenders(readFileSync(file, "utf8"));
    for (const o of offenders) {
      const rel = file.slice(REPO_ROOT.length + 1);
      console.error(`${rel}:${o.line}: forbidden '${o.what}' — ${o.text}`);
      failed = true;
    }
  }

  for (const copy of ORIGIN_COPIES) {
    if (!existsSync(copy)) {
      console.error(
        `lint-no-request-url-origin: missing origin helper: ${copy.slice(REPO_ROOT.length + 1)}`,
      );
      process.exit(2);
    }
  }
  const [webConsole, agentConsole] = ORIGIN_COPIES.map((p) =>
    normalizeForDrift(readFileSync(p, "utf8")),
  );
  if (webConsole !== agentConsole) {
    console.error(
      "lint-no-request-url-origin: the two lib/http/origin.ts copies have drifted. " +
        "apps/web-console and apps/agent-console cannot share the helper by import " +
        "(separate npm packages and Docker build contexts), so both copies must be edited together.",
    );
    failed = true;
  }

  if (failed) {
    console.error(
      "\nlint-no-request-url-origin: route handlers must build absolute redirect targets from " +
        "resolveCanonicalOrigin() in lib/http/origin.ts, never from request.url or " +
        "request.nextUrl.origin. Next.js derives those from the server's own " +
        "`--hostname 0.0.0.0` bind address, not the Host header, so such a redirect is " +
        "emitted as https://0.0.0.0:3000/... Read query params from " +
        "request.nextUrl.searchParams instead of reparsing request.url.",
    );
    process.exit(1);
  }

  console.log(
    `lint-no-request-url-origin: ok (${files.length} route handlers clean, origin helper copies in sync)`,
  );
  process.exit(0);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
