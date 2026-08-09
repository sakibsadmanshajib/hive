/**
 * Every relative `/api/...` URL a Client Component fetches must have a route
 * handler in this app. Nothing enforced that, so four browser calls pointed at
 * `/api/v1/accounts/current/...`, a namespace that only ever existed on the
 * control-plane. Next answered each with its own 404 HTML page, which made
 * creating an API key, revoking an API key, listing payment rails and starting
 * a checkout all silently impossible from the console.
 *
 * Issue #552 fixed exactly this mistake on the per-key limits page, but it was
 * fixed one call site at a time, so the sibling call sites stayed broken. The
 * existing suite could not catch any of them: api-key-limits-client.test.ts
 * asserts the shape of the *server* client (an absolute `${BASE_URL}/api/v1/...`
 * built from CONTROL_PLANE_BASE_URL), which is a different code path from the
 * browser fetch and is correct either way. No test ever compared a URL the
 * browser requests against the set of routes this app actually serves.
 *
 * This walks the real files rather than a hand-maintained list, so a new client
 * component with a bad path fails here without anyone remembering to add it.
 *
 * Its ceiling, so nobody reads it as stronger than it is: a catch-all route
 * matches every remaining segment, so for a `[...path]` namespace this proves
 * only that a handler exists, never that the operation is on that handler's
 * allowlist. `/api/v1/accounts/current/members/x/promote` passes here and
 * answers 404 at runtime. The allowlist itself is pinned in
 * api-keys-proxy-route.test.ts.
 */
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, resolve } from "node:path";

import { describe, expect, it } from "vitest";

const APP_ROOT = resolve(__dirname, "..");
const API_ROOT = join(APP_ROOT, "app", "api");

function walk(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    if (entry === "node_modules" || entry === ".next") continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      out.push(...walk(full));
    } else {
      out.push(full);
    }
  }
  return out;
}

// routePatterns lists every served path as segments, where a `[seg]` directory
// becomes "*" (matches one segment) and `[...seg]` becomes "**" (one or more).
function routePatterns(): string[][] {
  return walk(API_ROOT)
    .filter((file) => /[/\\]route\.tsx?$/.test(file))
    .map((file) =>
      file
        .slice(API_ROOT.length + 1)
        .replace(/[/\\]route\.tsx?$/, "")
        .split(/[/\\]/)
        .filter((segment) => segment !== "")
        // Route groups like (marketing) do not appear in the URL.
        .filter((segment) => !/^\(.*\)$/.test(segment))
        .map((segment) => {
          if (/^\[\.\.\..+\]$/.test(segment)) return "**";
          if (/^\[.+\]$/.test(segment)) return "*";
          return segment;
        }),
    );
}

function matches(pattern: string[], actual: string[]): boolean {
  let p = 0;
  let a = 0;
  while (p < pattern.length && a < actual.length) {
    if (pattern[p] === "**") {
      // Catch-all consumes the rest; Next requires at least one segment.
      return p === pattern.length - 1;
    }
    if (pattern[p] !== "*" && pattern[p] !== actual[a]) return false;
    p += 1;
    a += 1;
  }
  return p === pattern.length && a === actual.length;
}

// fetchedApiPaths collects `/api/...` literals from Client Components. A
// `${...}` interpolation stands in for exactly one dynamic segment.
function fetchedApiPaths(): { file: string; path: string }[] {
  const sources = [join(APP_ROOT, "app"), join(APP_ROOT, "components")]
    .flatMap((dir) => walk(dir))
    .filter((file) => /\.tsx?$/.test(file) && !/\.test\.tsx?$/.test(file));

  const found: { file: string; path: string }[] = [];
  for (const file of sources) {
    const source = readFileSync(file, "utf8");
    if (!/^\s*["']use client["']/m.test(source)) continue;
    for (const match of source.matchAll(/["'`](\/api\/[^"'`\s]*)["'`]/g)) {
      const raw = match[1];
      const normalized = raw
        .split("?")[0]
        .replace(/\$\{[^}]*\}/g, "*")
        .replace(/\/+$/, "");
      found.push({ file: file.slice(APP_ROOT.length + 1), path: normalized });
    }
  }
  return found;
}

describe("client components only fetch paths this app serves", () => {
  const patterns = routePatterns();

  // Without this the suite would pass vacuously if the discovery walk broke.
  it("discovers the app's route handlers", () => {
    expect(patterns.length).toBeGreaterThan(5);
  });

  it("discovers client-side api fetches", () => {
    expect(fetchedApiPaths().length).toBeGreaterThan(5);
  });

  it.each(fetchedApiPaths())(
    "$file fetches $path, which has a route handler",
    ({ path }) => {
      const actual = path.split("/").filter((segment) => segment !== "");
      // Drop the leading "api" segment; patterns are relative to app/api.
      const relative = actual.slice(1);
      const matched = patterns.some((pattern) => matches(pattern, relative));
      expect(
        matched,
        `No route handler serves ${path}. A browser fetch to a path this app ` +
          `does not serve returns Next's 404 HTML page, not an API error.`,
      ).toBe(true);
    },
  );
});
