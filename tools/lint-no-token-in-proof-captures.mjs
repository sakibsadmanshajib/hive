#!/usr/bin/env node
// Structural guard against a real credential landing in docs/proof/.
//
// PR #578 shipped four real account-invitation tokens as `?token=` query
// params in a captured `window.location.href` overlay, committed straight
// into docs/proof/invite-journey-534/journey-log-part2.txt. The visual-proof
// practice (.claude/rules/orchestrator.md, "Visual proof before merge")
// captures the real URL of a running page, and any auth flow that carries its
// credential in the query string (invitation accept, password reset, magic
// link, OAuth callback) will print that credential straight into the log the
// same way. GitGuardian caught it after the fact; this makes the same class
// of leak fail locally and in CI before it is ever committed.
//
// Deliberately scoped to docs/proof/, not the whole repo: that directory is
// captured, human-curated evidence of a live run, and is exactly where a
// pasted real URL ends up. Source code has its own guards (lint-no-*.mjs
// siblings) for different failure modes.
//
// Mirrors the pattern of tools/lint-no-request-url-origin.mjs: a small,
// dependency-free scanner with a MUST_CATCH / MUST_ALLOW self-test that runs
// as a preflight on every invocation.

import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { dirname, resolve, join, extname } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(HERE, "..");
const PROOF_DIR = join(REPO_ROOT, "docs", "proof");

// Screenshots and recordings live alongside the text logs that describe them.
// A token can only leak through something this scanner can read as text.
const BINARY_EXT = new Set([
  ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico",
  ".mp4", ".mov", ".webm", ".pdf", ".zip",
]);

// The parameter names a credential rides in on. `code=` covers an OAuth
// authorization code, which is a bearer credential exactly like a token.
// `token_hash`/`hashed_token`/`email_otp` are the one-time credentials the
// live-session helper handles (docs/live-test-auth.md); each one is a valid
// login on its own until consumed.
const PARAM_NAMES = [
  "access_token",
  "refresh_token",
  "token_hash",
  "hashed_token",
  "email_otp",
  "token",
  "code",
];

// A bare JWT with no parameter name in front of it. On 2026-08-08 an agent
// printed a live session token to stdout as a plain value, so the leak a
// proof capture can carry is not always shaped like `name=value`.
const BARE_JWT_RE = /\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}/g;

// A Hive API key, which rides in an Authorization header rather than a query
// string and so is invisible to PARAM_RE. `generateSecret`
// (apps/control-plane/internal/apikeys/service.go) emits "hk_" plus 43
// base64url characters, and a live walkthrough that mints one writes it into
// the same captured step log everything else lands in. The list view's masked
// form ("hk_xxxx" plus bullets plus a six character suffix) is far short of
// the length floor here, so a redacted capture stays quiet.
const BARE_API_KEY_RE = /\bhk_[A-Za-z0-9_-]{24,}/g;

// An obvious placeholder, not a real value. Matched before the entropy check
// so a deliberately redacted or documented example never trips the guard.
const ALLOWED_VALUE = /redact|changeme|example|placeholder|^<.*>$|^\{.*\}$|^\.\.\.+$/i;

function shannonEntropyBitsPerChar(value) {
  const counts = new Map();
  for (const ch of value) counts.set(ch, (counts.get(ch) ?? 0) + 1);
  let entropy = 0;
  for (const count of counts.values()) {
    const p = count / value.length;
    entropy -= p * Math.log2(p);
  }
  return entropy;
}

// "Long high-entropy value": at least 20 characters (shorter than that is a
// language code, a short id, or similar) and a per-character Shannon entropy
// that a hex or base64url token clears but repetitive or wordy text does not.
function looksLikeToken(value) {
  if (value.length < 20) return false;
  if (ALLOWED_VALUE.test(value)) return false;
  return shannonEntropyBitsPerChar(value) >= 3.0;
}

const PARAM_RE = new RegExp(
  String.raw`\b(?:${PARAM_NAMES.join("|")})=([^\s&"'<>]+)`,
  "g",
);

export function findOffenders(text) {
  const offenders = [];
  const lines = text.split("\n");
  lines.forEach((line, i) => {
    PARAM_RE.lastIndex = 0;
    let match = PARAM_RE.exec(line);
    while (match !== null) {
      const value = match[1];
      if (looksLikeToken(value)) {
        offenders.push({ line: i + 1, text: line.trim() });
      }
      match = PARAM_RE.exec(line);
    }
    BARE_JWT_RE.lastIndex = 0;
    BARE_API_KEY_RE.lastIndex = 0;
    if (BARE_JWT_RE.test(line) || BARE_API_KEY_RE.test(line)) {
      offenders.push({ line: i + 1, text: line.trim() });
    }
  });
  return offenders;
}

function collectProofFiles(dir) {
  if (!existsSync(dir)) return [];
  const found = [];
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      found.push(...collectProofFiles(full));
    } else if (!BINARY_EXT.has(extname(entry).toLowerCase())) {
      found.push(full);
    }
  }
  return found;
}

// Every evasion this guard exists to catch, encoded as a fixture. Values are
// synthetic (a cycled hex alphabet), never a real leaked token.
const FAKE_HEX = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd";
const MUST_CATCH = [
  ["token= query param", `shot 12: http://web-invite534:3000/accept?token=${FAKE_HEX}`],
  ["access_token= query param", `redirect: /callback?access_token=${FAKE_HEX}`],
  ["refresh_token= query param", `refresh_token=${FAKE_HEX}&expires_in=3600`],
  ["code= query param (OAuth authorization code)", `/auth/callback?code=${FAKE_HEX}`],
  // GoTrue returns a verified session in the FRAGMENT, never the query string.
  ["access_token= in a URL fragment", `landed on /tasks#access_token=${FAKE_HEX}&token_type=bearer`],
  ["token_hash= one-time credential", `generate_link -> /auth/v1/verify?token_hash=${FAKE_HEX}`],
  ["bare JWT with no parameter name", `stdout: eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJkZW1vIn0.${FAKE_HEX}`],
  ["bare Hive API key in a step log", `"snippet": "curl -H 'Authorization: Bearer hk_${FAKE_HEX}'"`],
];

const MUST_ALLOW = [
  ["redacted placeholder", "shot 12: http://web-invite534:3000/accept?token=REDACTED_INVITE_TOKEN"],
  ["angle-bracket placeholder", "curl '.../callback?access_token=<ACCESS_TOKEN>'"],
  ["short id, not a token", "GET /invitations/accept?token=abc123"],
  ["language code", "GET /oauth/callback?code=en"],
  ["documented example value", "?token=example-not-a-real-value-but-twenty-chars"],
  ["masked API key as the console list renders it", "hk_xxxx•••s2Yn0s  Revoked"],
  ["redacted API key placeholder", "created key: hk_<redacted by demo-walkthrough>"],
];

function selfTest() {
  for (const [name, text] of MUST_CATCH) {
    assert.ok(findOffenders(text).length > 0, `self-test: evasion NOT caught -> ${name}`);
  }
  for (const [name, text] of MUST_ALLOW) {
    const offenders = findOffenders(text);
    assert.equal(offenders.length, 0, `self-test: false positive -> ${name}: ${JSON.stringify(offenders)}`);
  }
  return MUST_CATCH.length + MUST_ALLOW.length;
}

function main() {
  const selfTestOnly = process.argv.includes("--self-test");

  let assertions;
  try {
    assertions = selfTest();
  } catch (err) {
    console.error(`lint-no-token-in-proof-captures: SELF-TEST FAILED\n${err.message}`);
    process.exit(2);
  }
  console.log(`lint-no-token-in-proof-captures: self-test ok (${assertions} assertions)`);
  if (selfTestOnly) process.exit(0);

  const files = collectProofFiles(PROOF_DIR);
  let failed = false;

  for (const file of files) {
    let content;
    try {
      content = readFileSync(file, "utf8");
    } catch {
      continue; // not readable as UTF-8 text; not this guard's job
    }
    for (const o of findOffenders(content)) {
      console.error(
        `${file.slice(REPO_ROOT.length + 1)}:${o.line}: looks like a real credential (query param, JWT, or API key) — ${o.text}`,
      );
      failed = true;
    }
  }

  if (failed) {
    console.error(
      "\nlint-no-token-in-proof-captures: a capture under docs/proof/ carries what looks " +
        "like a real credential (token=, access_token=, refresh_token=, code=, a bare JWT, " +
        "or an hk_ API key). Redact it before committing, e.g. token=REDACTED_INVITE_TOKEN, " +
        "and mask the same value in any screenshot that shows it.",
    );
    process.exit(1);
  }

  console.log(`lint-no-token-in-proof-captures: ok (${files.length} files under docs/proof/ scanned)`);
  process.exit(0);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
