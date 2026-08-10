// Denominator guard.
//
// Coverage here is proven / enumerated, and both sides are read out of the
// rendered DOM at run time. That makes the ratio blind to the failure it most
// needs to catch: a route that crashes during render, comes back blank, or
// half hydrates offers fewer controls, the denominator shrinks with the
// numerator, and the percentage holds steady or climbs during an outage.
//
// The fix is a floor per route, committed to the repository and reviewed like
// any other file. A route that enumerates fewer controls than its floor fails
// the gate. Lowering a floor is then a deliberate line in a diff with a reason
// beside it, which is the only place that decision belongs.
//
// A floor rather than a comparison against the previous run: a CI job starts
// from a clean checkout with no artifact from the last run, so previous-run
// state would have to be fetched, could be missing, and would degrade to no
// check at all after any retention gap. The committed floor is available to
// every run, including the first one.

import { readFileSync, writeFileSync } from "node:fs";

const FLOOR_COMMENT =
  "Minimum control count per route, recorded from a live run. A run that enumerates fewer controls on a route than the number here fails the gate: coverage is proven/enumerated, so a route rendering less than it used to would otherwise shrink the denominator and hold the percentage up during an outage. Regenerate deliberately with INTERACTION_FLOORS=update and commit the result with a reason.";


export interface FloorFile {
  $comment?: string;
  routes: Record<string, number>;
}

export interface VisitedRoute {
  route: string;
  visited: boolean;
  enumerated: number;
}

export function loadFloors(path: string): Record<string, number> {
  const parsed: unknown = JSON.parse(readFileSync(path, "utf8"));
  const routes = (parsed as { routes?: unknown }).routes;
  if (typeof routes !== "object" || routes === null) {
    throw new Error(`${path} has no routes object`);
  }
  const out: Record<string, number> = {};
  for (const [route, value] of Object.entries(routes as Record<string, unknown>)) {
    if (typeof value !== "number" || !Number.isInteger(value) || value < 0) {
      throw new Error(`${path} floor for ${route} is not a non-negative integer`);
    }
    out[route] = value;
  }
  return out;
}

/**
 * Every way the floor file and a run can disagree, as gate problems.
 *
 * Both directions are failures. Fewer controls than the floor is the outage
 * this exists to catch. A visited route with no floor at all is the same hole
 * one step earlier: an unrecorded route can drop to zero controls and nothing
 * would notice.
 */
export function floorProblems(
  floors: Record<string, number>,
  routes: VisitedRoute[],
): string[] {
  const problems: string[] = [];
  for (const route of routes) {
    if (!route.visited) {
      continue;
    }
    const floor = floors[route.route];
    if (floor === undefined) {
      problems.push(
        `route ${route.route} enumerated ${String(route.enumerated)} controls but has no floor in route-floors.json; record one, or a later run that renders nothing there will report full coverage of an empty page`,
      );
      continue;
    }
    if (route.enumerated < floor) {
      problems.push(
        `route ${route.route} enumerated ${String(route.enumerated)} controls, below its recorded floor of ${String(floor)}; the page renders less than it used to, which shrinks the denominator and inflates coverage. Fix the route, or lower the floor in this PR with a reason`,
      );
    }
  }
  return problems;
}

/** Floor entries naming a route that no longer exists under app/. */
export function staleFloors(
  floors: Record<string, number>,
  discoveredPatterns: string[],
): string[] {
  const known = new Set(discoveredPatterns);
  return Object.keys(floors).filter((route) => !known.has(route));
}

/**
 * Rewrites the floor file from a run. Opt in through INTERACTION_FLOORS=update
 * so a run can never quietly lower its own bar: the new numbers land in the
 * working tree and have to be committed and reviewed.
 */
export function writeFloors(path: string, routes: VisitedRoute[]): void {
  let existing: Record<string, number> = {};
  try {
    existing = loadFloors(path);
  } catch {
    existing = {};
  }
  const next: Record<string, number> = { ...existing };
  for (const route of routes) {
    if (route.visited) {
      next[route.route] = route.enumerated;
    }
  }
  const ordered: Record<string, number> = {};
  for (const route of Object.keys(next).sort()) {
    ordered[route] = next[route];
  }
  const body: FloorFile = { $comment: FLOOR_COMMENT, routes: ordered };
  writeFileSync(path, JSON.stringify(body, null, 2) + "\n", "utf8");
}
