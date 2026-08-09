// Route discovery for the interaction coverage gate.
//
// Routes come from the filesystem (`app/**/page.tsx`), never from a hand
// written list. A new page added to the app is therefore enumerated by the
// next run of the gate with no edit to this file, and a route that needs a
// query string or a dynamic segment fails loudly until somebody records how
// to reach it. That is the property that makes the denominator trustworthy:
// nothing gets covered by accident, and nothing gets dropped by accident.

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

export interface RouteFixture {
  /** Query string appended when visiting the route, without the leading "?". */
  query?: string;
  /** Recorded reason this route is not visited at all. */
  skip?: string;
  /** Owner accountable for a skip. */
  owner?: string;
  /** Values for dynamic segments when no instance can be discovered live. */
  params?: Record<string, string>;
  /** Which session the route is visited with. Defaults by path. */
  auth?: "user" | "anon";
  /** Path prefix this route is expected to redirect to, when it redirects. */
  expectRedirect?: string;
}

export interface RouteFixtureFile {
  routes: Record<string, RouteFixture>;
}

export interface DiscoveredRoute {
  /** The Next.js route pattern, e.g. `/console/api-keys/[id]/limits`. */
  pattern: string;
  /** A concrete path to visit, once params are resolved. */
  path: string;
  /** True when the pattern carries at least one dynamic segment. */
  dynamic: boolean;
  fixture: RouteFixture;
}

function walk(dir: string, out: string[]): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walk(full, out);
    } else if (entry === "page.tsx" || entry === "page.ts") {
      out.push(full);
    }
  }
  return out;
}

/** Converts an `app/` relative page file path into a Next.js route pattern. */
export function patternForPageFile(appRelativePath: string): string {
  const segments = appRelativePath
    .split(sep)
    .slice(0, -1)
    // Route groups `(marketing)` and parallel slots `@modal` contribute no
    // URL segment.
    .filter((s) => !(s.startsWith("(") && s.endsWith(")")))
    .filter((s) => !s.startsWith("@"));
  return "/" + segments.join("/");
}

export function isDynamic(pattern: string): boolean {
  return pattern.includes("[");
}

/** Substitutes `[id]` style segments from a params map. Throws when unmapped. */
export function fillPattern(
  pattern: string,
  params: Record<string, string>,
): string {
  return pattern.replace(/\[(\.\.\.)?([^\]]+)\]/g, (_match, _spread, rawName) => {
    const name = String(rawName);
    const value = params[name];
    if (value === undefined || value === "") {
      throw new Error(
        `route ${pattern} has an unmapped dynamic segment [${name}]; add a params entry to route-fixtures.json or make an instance reachable by a link`,
      );
    }
    return value;
  });
}

export function loadRouteFixtures(file: string): RouteFixtureFile {
  const parsed: unknown = JSON.parse(readFileSync(file, "utf8"));
  if (
    typeof parsed !== "object" ||
    parsed === null ||
    !("routes" in parsed) ||
    typeof (parsed as { routes: unknown }).routes !== "object"
  ) {
    throw new Error(`${file} must be an object with a "routes" map`);
  }
  return parsed as RouteFixtureFile;
}

/**
 * Discovers every route the console renders.
 *
 * `appDir` is the Next.js `app` directory. Dynamic routes are returned with
 * `path` still holding the pattern; the caller resolves them from live links
 * or from declared params and fails when neither is available.
 */
export function discoverRoutes(
  appDir: string,
  fixtures: RouteFixtureFile,
): DiscoveredRoute[] {
  const files = walk(appDir, []);
  const routes: DiscoveredRoute[] = [];
  for (const file of files) {
    const pattern = patternForPageFile(relative(appDir, file));
    const fixture = fixtures.routes[pattern] ?? {};
    const dynamic = isDynamic(pattern);
    let path = pattern;
    if (dynamic && fixture.params) {
      path = fillPattern(pattern, fixture.params);
    }
    if (fixture.query) {
      path = `${path}?${fixture.query}`;
    }
    routes.push({ pattern, path, dynamic, fixture });
  }
  routes.sort((a, b) => a.pattern.localeCompare(b.pattern));
  return routes;
}

/** Fixture entries pointing at routes that no longer exist are a rot signal. */
export function staleFixtureRoutes(
  fixtures: RouteFixtureFile,
  discovered: DiscoveredRoute[],
): string[] {
  const known = new Set(discovered.map((r) => r.pattern));
  return Object.keys(fixtures.routes).filter((pattern) => !known.has(pattern));
}
