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
  /**
   * Issue that has to stay open for a skip to remain valid. Required on any
   * skip that is not declared permanent: see lib/exclusions.ts.
   */
  issue?: number;
  /** True for a skip that is a standing decision rather than a deferral. */
  permanent?: boolean;
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

const FIXTURE_FIELDS: ReadonlySet<string> = new Set([
  "query",
  "skip",
  "owner",
  "issue",
  "permanent",
  "params",
  "auth",
  "expectRedirect",
]);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function optionalString(value: unknown, where: string): string | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (typeof value !== "string") {
    throw new Error(`${where} must be a string`);
  }
  return value;
}

/**
 * Reads the fixture file into the shape the sweep expects.
 *
 * Field by field rather than by asserting the parsed JSON is already of this
 * type: a typo in a key name, or an issue number written as a string, would
 * otherwise be a silently ignored field, and every field here either widens or
 * narrows what gets measured.
 */
export function parseRouteFixtures(raw: string, source: string): RouteFixtureFile {
  const parsed: unknown = JSON.parse(raw);
  if (!isRecord(parsed) || !isRecord(parsed.routes)) {
    throw new Error(`${source} must be an object with a "routes" map`);
  }
  const routes: Record<string, RouteFixture> = {};
  for (const [pattern, value] of Object.entries(parsed.routes)) {
    if (!isRecord(value)) {
      throw new Error(`${source} entry for "${pattern}" must be an object`);
    }
    const where = `${source} entry for "${pattern}"`;
    for (const field of Object.keys(value)) {
      if (!FIXTURE_FIELDS.has(field)) {
        throw new Error(`${where} carries unknown field "${field}"`);
      }
    }
    let params: Record<string, string> | undefined;
    if (value.params !== undefined) {
      if (!isRecord(value.params)) {
        throw new Error(`${where} has a params field that is not an object`);
      }
      params = {};
      for (const [name, param] of Object.entries(value.params)) {
        if (typeof param !== "string") {
          throw new Error(`${where} has a non-string value for param "${name}"`);
        }
        params[name] = param;
      }
    }
    const auth = optionalString(value.auth, `${where} auth`);
    if (auth !== undefined && auth !== "user" && auth !== "anon") {
      throw new Error(`${where} auth must be "user" or "anon"`);
    }
    if (value.issue !== undefined && !Number.isInteger(value.issue)) {
      throw new Error(`${where} issue must be an integer issue number`);
    }
    if (value.permanent !== undefined && typeof value.permanent !== "boolean") {
      throw new Error(`${where} permanent must be a boolean`);
    }
    routes[pattern] = {
      query: optionalString(value.query, `${where} query`),
      skip: optionalString(value.skip, `${where} skip`),
      owner: optionalString(value.owner, `${where} owner`),
      issue: typeof value.issue === "number" ? value.issue : undefined,
      permanent: value.permanent === true,
      params,
      auth,
      expectRedirect: optionalString(value.expectRedirect, `${where} expectRedirect`),
    };
  }
  return { routes };
}

export function loadRouteFixtures(file: string): RouteFixtureFile {
  return parseRouteFixtures(readFileSync(file, "utf8"), file);
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
