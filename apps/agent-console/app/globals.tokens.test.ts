import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, it, expect } from "vitest";

/*
 * Guard for the one rule that keeps Hive from growing a fourth visual
 * language: this app never invents a colour. Its `@theme` block is a subset of
 * apps/web-console's, and every token it does declare must carry the console's
 * exact value.
 *
 * A subset is allowed (this sidecar has no tables, badges, or charts), so the
 * assertion is one-directional: web-console may hold tokens this app does not,
 * but a shared token whose value drifts fails here rather than shipping two
 * slightly different sienna accents to production.
 */

// Resolved from the vitest project root (apps/agent-console) rather than
// import.meta.url: Vite hands test modules a non-file: URL, so new URL(...)
// throws. `npm run test:unit` always runs from this app's directory, locally
// and in CI (.github/workflows/ci.yml sets working-directory).
const AGENT_CSS = resolve(process.cwd(), "app/globals.css");
const CONSOLE_CSS = resolve(process.cwd(), "../web-console/app/globals.css");

/** Extracts `--name: value` pairs from the first top-level `@theme { ... }`. */
function themeTokens(file: string): Map<string, string> {
  const css = readFileSync(file, "utf8");
  const start = css.indexOf("@theme");
  if (start === -1) throw new Error(`no @theme block in ${file}`);

  const open = css.indexOf("{", start);
  let depth = 0;
  let end = -1;
  for (let i = open; i < css.length; i += 1) {
    if (css[i] === "{") depth += 1;
    if (css[i] === "}") {
      depth -= 1;
      if (depth === 0) {
        end = i;
        break;
      }
    }
  }
  if (end === -1) throw new Error(`unterminated @theme in ${file}`);

  // Comments are stripped first: both files document their tokens inline, and
  // prose that mentions a variable name followed by a colon (e.g. "--font-sans:
  // ...") would otherwise be parsed as a declaration.
  const body = css.slice(open + 1, end).replace(/\/\*[\s\S]*?\*\//g, "");
  const tokens = new Map<string, string>();
  for (const match of body.matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/gi)) {
    tokens.set(match[1], match[2].trim());
  }
  return tokens;
}

describe("agent-console design tokens", () => {
  const agent = themeTokens(AGENT_CSS);
  const consoleTokens = themeTokens(CONSOLE_CSS);

  it("parses a non-trivial number of tokens from both apps", () => {
    // Cheap sanity check: a silently-broken parser returning an empty map
    // would make every assertion below vacuously pass.
    expect(agent.size).toBeGreaterThan(20);
    expect(consoleTokens.size).toBeGreaterThan(20);
  });

  it("declares no colour the console does not also declare", () => {
    const unknown = [...agent.keys()].filter(
      (name) => name.startsWith("--color-") && !consoleTokens.has(name),
    );
    expect(unknown).toEqual([]);
  });

  it("matches the console's value for every shared colour token", () => {
    const drifted = [...agent.entries()]
      .filter(([name]) => name.startsWith("--color-"))
      .filter(([name, value]) => consoleTokens.get(name) !== value)
      .map(([name, value]) => `${name}: ${value} (console: ${consoleTokens.get(name)})`);
    expect(drifted).toEqual([]);
  });

  it("uses the same font families as the console", () => {
    for (const name of ["--font-sans", "--font-display", "--font-mono"]) {
      expect(agent.get(name)).toBe(consoleTokens.get(name));
    }
  });

  it("keeps the display face out of the serif register", () => {
    // The console's technical type direction (see either globals.css): display
    // resolves to the geometric sans, never a serif. Chat keeps the serif.
    expect(agent.get("--font-display")).toContain("--font-geist-sans");
    expect(consoleTokens.get("--font-display")).toContain("--font-geist-sans");
  });
});
