#!/usr/bin/env node
// Structural guard against the `0.0.0.0` redirect-origin bug family.
//
// Next.js does not build a request's URL from the Host header. With
// `experimental.trustHostHeader` unset it composes it from the server's OWN
// bind address (next/dist/server/lib/router-utils/resolve-routes.js builds
// `initUrl` from `opts.hostname` + `opts.port`, and `formatHostname('0.0.0.0')`
// returns it verbatim). Both console containers start with
// `--hostname 0.0.0.0 --port 3000`, so any route handler that derives an
// absolute redirect target from the request URL emits
// `http(s)://0.0.0.0:3000/...` and strands the user.
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
// Route handlers must resolve absolute targets through lib/http/origin.ts's
// resolveCanonicalOrigin, and read query params from
// `request.nextUrl.searchParams` (correct, since it comes from the request line)
// rather than reparsing the request URL.
//
// The rules are a whitelist, not a blacklist of the three expressions that
// happened to be wrong. A blacklist is trivially evaded by writing the same
// mistake another way (destructuring, an inline `.origin`, an aliased variable,
// a template literal), which is how this family keeps recurring. So: no `.url`
// read, no `.origin` read, no `nextUrl` member outside a safe list, no
// destructuring an origin-bearing property, no computed access to one.
//
// `--self-test` asserts every known evasion is still caught and that legitimate
// handler code is not flagged. It runs as a preflight on every invocation, so
// the guard proves itself non-vacuous on every CI run instead of only when
// someone remembers to check.
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
import assert from "node:assert/strict";

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

// Only these two come from the request line and are safe to read. Everything
// else on a NextURL (origin, href, host, protocol, port, toString, clone)
// carries the server's bind address.
const SAFE_NEXTURL_MEMBERS = ["searchParams", "pathname"];

// Property names that carry an origin. Reading any of them off a
// request-derived object, by member access or by destructuring, is the defect.
const ORIGIN_BEARING = ["origin", "host", "hostname", "href", "protocol", "port"];

const FORBIDDEN_PATTERNS = [
  {
    // `.url` on the request or on ANY alias of it. Matching only
    // `request.url` / `req.url` would miss
    // `const r = request; new URL(p, r.url)`. No route handler in either app
    // has a legitimate `.url` read.
    re: /\.\s*url\b/g,
    what: "a `.url` read (the request URL carries the server's bind address)",
  },
  {
    // `.origin` on anything: covers `new URL(request.url).origin`,
    // `request.nextUrl.origin`, and an aliased `u.origin`. Route handlers get
    // an origin from resolveCanonicalOrigin(), never off an object.
    re: /\.\s*origin\b/g,
    what: "an `.origin` read (use resolveCanonicalOrigin() instead)",
  },
  {
    // Any nextUrl member that is not on the safe list.
    re: new RegExp(
      String.raw`\bnextUrl\s*\.\s*(?!(?:${SAFE_NEXTURL_MEMBERS.join("|")})\b)[A-Za-z_$]`,
      "g",
    ),
    what: `an unsafe request.nextUrl member (only ${SAFE_NEXTURL_MEMBERS.join(" and ")} are safe)`,
  },
  {
    // Destructuring an origin-bearing property, e.g.
    // `const { searchParams, origin } = new URL(request.url)`. Matched against
    // the whole file rather than per line, so a multi-line destructure cannot
    // slip through.
    re: new RegExp(
      String.raw`\{[^{}]*\b(?:${ORIGIN_BEARING.join("|")})\b[^{}]*\}\s*=`,
      "g",
    ),
    what: "destructuring an origin-bearing property from a request-derived URL",
  },
  {
    // Computed access, e.g. request["url"] or nextUrl["origin"]. The property
    // name lives *inside* a string literal, so this rule has to run against the
    // text with literals preserved; blanking them would erase the very thing
    // being matched. Comments are still stripped, so a comment mentioning
    // `request["url"]` does not trip it.
    re: new RegExp(
      String.raw`\[\s*(['"])(?:url|${ORIGIN_BEARING.join("|")})\1\s*\]`,
      "g",
    ),
    what: "computed access to an origin-bearing property",
    keepLiterals: true,
  },
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

// Blank out comments, and optionally string and template-literal *text*, by
// overwriting them with spaces. Replacing rather than deleting keeps every line
// and column number accurate for reporting.
//
// A naive `line.replace(/\/\/.*$/, "")` is not good enough: it treats the `//`
// inside a literal such as `"https://example.test"` as the start of a comment
// and drops the rest of the line, so a violation later on that same line becomes
// invisible. That is a false negative in precisely the bug family this lint
// exists to catch, so the scan below tracks literal state properly.
//
// Interpolated expressions inside a template literal are real code and are
// preserved, so `` `${request.nextUrl.origin}/x` `` is still scanned.
export function blankOut(src, { literals = true } = {}) {
  const out = src.split("");
  const blank = (i) => {
    if (i < out.length && out[i] !== "\n") out[i] = " ";
  };
  // Brace depth at which each currently-open template literal resumes.
  const templateStack = [];
  let state = "code";
  let braceDepth = 0;
  let i = 0;

  while (i < src.length) {
    const c = src[i];
    const c2 = src[i + 1];

    if (state === "code") {
      if (c === "/" && c2 === "/") {
        blank(i);
        blank(i + 1);
        state = "line";
        i += 2;
        continue;
      }
      if (c === "/" && c2 === "*") {
        blank(i);
        blank(i + 1);
        state = "block";
        i += 2;
        continue;
      }
      if (c === "'" || c === '"') {
        state = c === "'" ? "single" : "double";
        if (literals) blank(i);
        i += 1;
        continue;
      }
      if (c === "`") {
        state = "template";
        if (literals) blank(i);
        i += 1;
        continue;
      }
      if (c === "{") braceDepth += 1;
      if (c === "}") {
        braceDepth -= 1;
        if (
          templateStack.length > 0 &&
          braceDepth === templateStack[templateStack.length - 1]
        ) {
          templateStack.pop();
          state = "template";
          if (literals) blank(i);
          i += 1;
          continue;
        }
      }
      i += 1;
      continue;
    }

    if (state === "line") {
      if (c === "\n") {
        state = "code";
        i += 1;
        continue;
      }
      blank(i);
      i += 1;
      continue;
    }

    if (state === "block") {
      if (c === "*" && c2 === "/") {
        blank(i);
        blank(i + 1);
        state = "code";
        i += 2;
        continue;
      }
      blank(i);
      i += 1;
      continue;
    }

    if (state === "single" || state === "double") {
      const quote = state === "single" ? "'" : '"';
      if (c === "\\") {
        if (literals) {
          blank(i);
          blank(i + 1);
        }
        i += 2;
        continue;
      }
      if (c === quote) {
        if (literals) blank(i);
        state = "code";
        i += 1;
        continue;
      }
      if (literals) blank(i);
      i += 1;
      continue;
    }

    // state === "template"
    if (c === "\\") {
      if (literals) {
        blank(i);
        blank(i + 1);
      }
      i += 2;
      continue;
    }
    if (c === "`") {
      if (literals) blank(i);
      state = "code";
      i += 1;
      continue;
    }
    if (c === "$" && c2 === "{") {
      // Interpolated expression: real code, so stop blanking and scan it.
      if (literals) {
        blank(i);
        blank(i + 1);
      }
      templateStack.push(braceDepth);
      braceDepth += 1;
      state = "code";
      i += 2;
      continue;
    }
    if (literals) blank(i);
    i += 1;
    continue;
  }

  return { text: out.join(""), unterminated: state !== "code" };
}

function lineOf(text, index) {
  let line = 1;
  for (let i = 0; i < index; i += 1) if (text[i] === "\n") line += 1;
  return line;
}

export function findOffenders(src) {
  const stripped = blankOut(src, { literals: true });
  // Same scan, but string and template text kept. Comments are blanked in both.
  const withLiterals = blankOut(src, { literals: false });
  const offenders = [];
  const srcLines = src.split("\n");

  // A literal state left open at EOF means the scan lost its place (an
  // unhandled regex literal, say). Fail loudly rather than silently
  // under-reporting, which is the failure mode this lint cannot afford.
  if (stripped.unterminated) {
    offenders.push({
      line: 1,
      what: "unparseable source: a string, template or comment was left open",
      text: "(scanner could not establish literal boundaries)",
    });
  }

  for (const { re, what, keepLiterals } of FORBIDDEN_PATTERNS) {
    const text = keepLiterals ? withLiterals.text : stripped.text;
    re.lastIndex = 0;
    let match = re.exec(text);
    while (match !== null) {
      const line = lineOf(text, match.index);
      offenders.push({ line, what, text: (srcLines[line - 1] ?? "").trim() });
      match = re.exec(text);
    }
  }

  return offenders.sort((a, b) => a.line - b.line);
}

// Compare the two helper copies on their code alone, so differing header
// comments (each names its own app) do not read as drift. String literals are
// deliberately KEPT: a copy whose fallback origin or wildcard list was edited
// has genuinely drifted, and blanking literals would hide exactly that.
export function normalizeForDrift(src) {
  return blankOut(src, { literals: false }).text.replace(/\s+/g, " ").trim();
}

// Every evasion raised in review, encoded as a fixture. Each must be caught, or
// the guard is decorative.
const MUST_CATCH = [
  ["plain request.url", "return NextResponse.redirect(new URL(safeNext, request.url));"],
  ["req.url shorthand", "const u = new URL(req.url);"],
  ["destructured origin", "const { searchParams, origin } = new URL(request.url);"],
  ["destructured origin off nextUrl", "const { origin } = request.nextUrl;"],
  [
    "multi-line destructured origin",
    "const {\n  searchParams,\n  origin,\n} = request.nextUrl;",
  ],
  ["inline .origin", "const o = new URL(request.url).origin;"],
  ["nextUrl.origin", "return NextResponse.redirect(new URL(p, request.nextUrl.origin));"],
  ["nextUrl.href", "const h = request.nextUrl.href;"],
  ["nextUrl.toString()", "const s = request.nextUrl.toString();"],
  ["nextUrl.clone()", "const c = request.nextUrl.clone();"],
  [
    "template literal built from nextUrl.origin",
    "const t = `${request.nextUrl.origin}/console`;",
  ],
  [
    "aliased request then .url",
    "const r = request;\nreturn NextResponse.redirect(new URL(p, r.url));",
  ],
  ["aliased nextUrl then .origin", "const nu = request.nextUrl;\nconst o = nu.origin;"],
  ["computed request['url']", 'const u = request["url"];'],
  ["computed nextUrl['origin']", "const o = nextUrl['origin'];"],
  [
    // The CodeRabbit finding: a URL literal earlier on the same line used to
    // truncate the line before the violation was ever seen.
    "violation after a URL literal on the same line",
    'const base = "https://example.test"; const o = new URL(request.url).origin;',
  ],
  [
    "violation after a template URL literal on the same line",
    "const base = `https://${h}/x`; const o = new URL(request.url).origin;",
  ],
  [
    "violation on the line after a block comment mentioning https://",
    "/* see https://example.test/docs */\nconst o = new URL(request.url).origin;",
  ],
];

// Legitimate code that must NOT be flagged, or the lint is unusable.
const MUST_ALLOW = [
  [
    "the fixed handler shape",
    "const { searchParams } = request.nextUrl;\n" +
      "const origin = resolveCanonicalOrigin(request);\n" +
      "return NextResponse.redirect(new URL(safeNext, origin));",
  ],
  ["safe nextUrl.pathname", "const { pathname } = request.nextUrl;"],
  [
    "nextUrl.searchParams member access",
    "const c = request.nextUrl.searchParams.get('code');",
  ],
  [
    "a comment naming the forbidden expressions",
    "// Never derive an origin from request.url or request.nextUrl.origin.\n" +
      "const origin = resolveCanonicalOrigin(request);",
  ],
  [
    "a doc block naming the forbidden expressions",
    "/**\n * request.url and nextUrl.origin both carry the bind address.\n */\n" +
      "const origin = resolveCanonicalOrigin(request);",
  ],
  ["a string containing a URL", 'const FALLBACK_ORIGIN = "http://localhost:3000";'],
];

function selfTest() {
  for (const [name, src] of MUST_CATCH) {
    assert.ok(
      findOffenders(src).length > 0,
      `self-test: evasion NOT caught -> ${name}\n  source: ${JSON.stringify(src)}`,
    );
  }
  for (const [name, src] of MUST_ALLOW) {
    const offenders = findOffenders(src);
    assert.equal(
      offenders.length,
      0,
      `self-test: false positive -> ${name}\n  flagged: ${JSON.stringify(offenders)}`,
    );
  }

  // Drift detection must notice a changed string constant, which is why
  // normalizeForDrift keeps literals.
  assert.notEqual(
    normalizeForDrift('const F = "http://localhost:3000";'),
    normalizeForDrift('const F = "http://localhost:9999";'),
    "self-test: drift detector ignored a changed string constant",
  );
  // ...and must ignore a comment-only difference, which is why it strips them.
  assert.equal(
    normalizeForDrift("// web-console copy\nconst F = 1;"),
    normalizeForDrift("// agent-console copy\nconst F = 1;"),
    "self-test: drift detector tripped on a comment-only difference",
  );

  return MUST_CATCH.length + MUST_ALLOW.length + 2;
}

function main() {
  const selfTestOnly = process.argv.includes("--self-test");

  // Preflight on every run: a guard that cannot prove it still catches the
  // known evasions is not a guard.
  let assertions;
  try {
    assertions = selfTest();
  } catch (err) {
    console.error(`lint-no-request-url-origin: SELF-TEST FAILED\n${err.message}`);
    process.exit(2);
  }
  console.log(
    `lint-no-request-url-origin: self-test ok (${assertions} assertions, ${MUST_CATCH.length} evasions covered)`,
  );
  if (selfTestOnly) process.exit(0);

  let failed = false;

  const files = APP_DIRS.flatMap(collectRouteHandlers);
  if (files.length === 0) {
    console.error(
      "lint-no-request-url-origin: found no route handlers to scan — expected paths moved?",
    );
    process.exit(2);
  }

  for (const file of files) {
    for (const o of findOffenders(readFileSync(file, "utf8"))) {
      console.error(
        `${file.slice(REPO_ROOT.length + 1)}:${o.line}: forbidden ${o.what} — ${o.text}`,
      );
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
        "resolveCanonicalOrigin() in lib/http/origin.ts, never from the request URL. " +
        "Next.js derives that URL from the server's own `--hostname 0.0.0.0` bind address, " +
        "not the Host header, so such a redirect is emitted as https://0.0.0.0:3000/... " +
        "Read query params from request.nextUrl.searchParams instead.",
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
