// @vitest-environment node
//
// Static guard for issue #539. Every redirect this app emits must carry the
// basePath, because Next.js does NOT prepend basePath to raw absolute URLs.
// The two APIs that bypass it are `window.location.*` and
// `NextResponse.redirect(new URL(...))`. When one of them emits a bare
// "/tasks", the Location header lands outside Caddy's /agent-workspace/*
// route (deploy/docker/Caddyfile.owui) and Open WebUI's SPA catch-all answers
// instead of this app -- the user is silently handed a different product.
//
// The per-site unit tests (middleware.test.ts, app/auth/callback/route.test.ts,
// app/auth/sign-in/page.test.tsx) assert the correct target for the redirects
// that exist today. They cannot fail for a redirect nobody remembered to
// test, which is exactly how #539 shipped: middleware.ts documented the trap
// in a comment while sign-in/page.tsx walked straight into it. This file
// closes that gap by scanning the source instead of the behaviour, so a NEW
// redirect added anywhere in the app fails the suite until it carries
// BASE_PATH.
//
// Deliberately NOT flagged:
//   * `redirect()` / `permanentRedirect()` from next/navigation, `next/link`
//     and `useRouter()`. These are Next's own navigation primitives and Next
//     applies basePath to them itself; prefixing those would double it to
//     "/agent-workspace/agent-workspace/...".
//   * Raw <a href> values. components/brand.tsx's "Back to chat" anchor is
//     deliberately unprefixed: the chat SPA is served from the origin root by
//     the same Caddy listener, so leaving the origin is the intent there.
import { describe, it, expect } from "vitest";
import { readFileSync, readdirSync } from "node:fs";
import { join, relative, sep } from "node:path";

import { BASE_PATH } from "./base-path";

const APP_ROOT = join(__dirname, "..");

const SKIP_DIRS = new Set(["node_modules", ".next", ".git", "coverage"]);

// The APIs that escape basePath. Each entry is matched against source text and
// the balanced argument list (or, for the assignment form, the rest of the
// line) is then required to mention BASE_PATH.
const CALL_APIS = [
  "window.location.assign(",
  "window.location.replace(",
  "NextResponse.redirect(",
  "Response.redirect(",
] as const;

const ASSIGN_APIS = ["window.location.href =", "window.location =", "location.href ="] as const;

function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (SKIP_DIRS.has(entry.name)) continue;
      out.push(...sourceFiles(join(dir, entry.name)));
      continue;
    }
    if (!/\.tsx?$/.test(entry.name)) continue;
    // Tests legitimately contain bare "/tasks" strings as expected values.
    if (/\.test\.tsx?$/.test(entry.name)) continue;
    out.push(join(dir, entry.name));
  }
  return out;
}

// Returns the source slice from `openParenIndex` through its matching close
// paren. A regex cannot do this: `new URL(`${BASE_PATH}/tasks`, origin)` nests
// parens, and a lazy `\(([^)]*)\)` would stop at the first one and miss the
// half of the expression that carries the prefix.
function balancedArgs(source: string, openParenIndex: number): string {
  let depth = 0;
  for (let i = openParenIndex; i < source.length; i += 1) {
    const ch = source[i];
    if (ch === "(") depth += 1;
    else if (ch === ")") {
      depth -= 1;
      if (depth === 0) return source.slice(openParenIndex, i + 1);
    }
  }
  return source.slice(openParenIndex);
}

interface Site {
  readonly file: string;
  readonly line: number;
  readonly api: string;
  readonly expression: string;
}

function redirectSites(): Site[] {
  const sites: Site[] = [];

  for (const file of sourceFiles(APP_ROOT)) {
    const source = readFileSync(file, "utf8");
    const rel = relative(APP_ROOT, file).split(sep).join("/");
    const lineAt = (index: number) => source.slice(0, index).split("\n").length;

    for (const api of CALL_APIS) {
      let from = 0;
      for (;;) {
        const found = source.indexOf(api, from);
        if (found === -1) break;
        from = found + api.length;
        // Skip matches inside comments: the files here document these APIs at
        // length, and a comment is not a redirect.
        const lineStart = source.lastIndexOf("\n", found) + 1;
        const linePrefix = source.slice(lineStart, found);
        if (/^\s*(\/\/|\*|\/\*)/.test(linePrefix)) continue;
        sites.push({
          file: rel,
          line: lineAt(found),
          api,
          expression: balancedArgs(source, found + api.length - 1),
        });
      }
    }

    for (const api of ASSIGN_APIS) {
      let from = 0;
      for (;;) {
        const found = source.indexOf(api, from);
        if (found === -1) break;
        from = found + api.length;
        const lineStart = source.lastIndexOf("\n", found) + 1;
        const linePrefix = source.slice(lineStart, found);
        if (/^\s*(\/\/|\*|\/\*)/.test(linePrefix)) continue;
        const lineEnd = source.indexOf("\n", found);
        sites.push({
          file: rel,
          line: lineAt(found),
          api,
          expression: source.slice(found, lineEnd === -1 ? source.length : lineEnd),
        });
      }
    }
  }

  return sites;
}

describe("every basePath-escaping redirect carries BASE_PATH (#539)", () => {
  const sites = redirectSites();

  // Without this, deleting or renaming every redirect in the app would make
  // the scan match nothing and the guard below would pass vacuously.
  it("finds the redirect sites it is meant to police", () => {
    expect(sites.length).toBeGreaterThanOrEqual(4);
    const files = new Set(sites.map((site) => site.file));
    expect(files).toContain("middleware.ts");
    expect(files).toContain("app/auth/callback/route.ts");
    expect(files).toContain("app/auth/sign-in/page.tsx");
  });

  it("prefixes every redirect target with BASE_PATH", () => {
    const offenders = sites
      .filter((site) => !site.expression.includes("BASE_PATH"))
      .map(
        (site) =>
          `${site.file}:${site.line} calls ${site.api} without BASE_PATH -- ` +
          `${site.expression.trim().replace(/\s+/g, " ")}`
      );

    // Reported as one list rather than one failing case per site, so a change
    // that misses the prefix in several places names all of them at once.
    expect(
      offenders,
      `Next.js does not prepend basePath to raw URLs, so these targets escape ` +
        `Caddy's ${BASE_PATH}/* route (deploy/docker/Caddyfile.owui) and Open ` +
        `WebUI's SPA catch-all answers instead of this app. Prefix each target ` +
        `with BASE_PATH from @/lib/base-path.`
    ).toEqual([]);
  });

  // BASE_PATH is a hand-maintained copy of next.config.ts's literal. If the
  // two drift, every redirect above is prefixed with a path Caddy does not
  // route and the per-site tests still pass, because they assert against this
  // same constant.
  it("matches the basePath configured in next.config.ts", () => {
    const config = readFileSync(join(APP_ROOT, "next.config.ts"), "utf8");
    const match = config.match(/basePath:\s*"([^"]+)"/);
    expect(match?.[1]).toBe(BASE_PATH);
  });
});
