// Denominator guard.
//
// Coverage here is proven / enumerated, and both sides are read out of the
// rendered DOM at run time. That makes the ratio blind to the failure it most
// needs to catch: a route that crashes during render, comes back blank, or
// half hydrates offers fewer controls, the denominator shrinks with the
// numerator, and the percentage holds steady or climbs during an outage.
//
// WHY THIS IS NOT A COUNT
// -----------------------
// It used to be one: a minimum number of controls per route, recorded from a
// live run. That number is not comparable between environments, which the
// report itself says about the very same figure. The floors were taken against
// the demo tenant, and CI signs in as a freshly seeded account whose workspace
// holds no keys, no invoices and no members, so it legitimately renders fewer
// rows. Either the gate is red forever in CI, or the numbers get regenerated
// there and the floor then sits below what the live console renders and stops
// detecting anything. A metric that cannot survive a change of data was never
// measuring the product.
//
// So the floor is a set of control identities that have to be there, and have
// to be usable. Presence and reachability, not volume. The console shell
// renders the same navigation on every /console route regardless of what the
// account holds, and each route's primary action is rendered by the page
// itself rather than by its data, so both are stable across environments while
// still failing loudly on a blank page, a crashed route, or a permission
// regression that greys the surface out.
//
// A floor is hand written and reviewed, and there is deliberately no command
// that regenerates it from a run: a bar a run can rewrite is a bar a run can
// lower.

import { readFileSync } from "node:fs";

export interface FloorFile {
  $comment?: string;
  /** Controls every route under `prefix` must render and leave usable. */
  shell: { prefix: string; require: string[] };
  /** Per route requirements, on top of the shell. */
  routes: Record<string, string[]>;
}

export interface VisitedRoute {
  route: string;
  visited: boolean;
  /** Identities enumerated on the route, in an enabled state. */
  enabled: string[];
  /** Identities enumerated on the route, rendered disabled. */
  disabled: string[];
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringArray(value: unknown, where: string): string[] {
  if (!Array.isArray(value)) {
    throw new Error(`${where} must be an array of control keys`);
  }
  return value.map((entry) => {
    if (typeof entry !== "string" || entry.trim() === "") {
      throw new Error(`${where} contains an entry that is not a control key`);
    }
    return entry;
  });
}

export function parseFloors(raw: string, source: string): FloorFile {
  const parsed: unknown = JSON.parse(raw);
  if (!isRecord(parsed)) {
    throw new Error(`${source} must be a JSON object`);
  }
  const shell = parsed.shell;
  if (!isRecord(shell) || typeof shell.prefix !== "string" || shell.prefix === "") {
    throw new Error(`${source} needs a "shell" object with a non-empty "prefix"`);
  }
  const routes = parsed.routes;
  if (!isRecord(routes)) {
    throw new Error(`${source} needs a "routes" object`);
  }
  const out: Record<string, string[]> = {};
  for (const [route, value] of Object.entries(routes)) {
    out[route] = stringArray(value, `${source} routes["${route}"]`);
  }
  return {
    $comment: typeof parsed.$comment === "string" ? parsed.$comment : undefined,
    shell: { prefix: shell.prefix, require: stringArray(shell.require, `${source} shell.require`) },
    routes: out,
  };
}

export function loadFloors(path: string): FloorFile {
  return parseFloors(readFileSync(path, "utf8"), path);
}

/** What a route has to render, shell requirements included. */
export function requirementsFor(floors: FloorFile, route: string): string[] {
  const own = floors.routes[route] ?? [];
  if (!route.startsWith(floors.shell.prefix)) {
    return own;
  }
  return [...new Set([...floors.shell.require, ...own])];
}

/**
 * Every way the floor file and a run can disagree, as gate problems.
 *
 * Three failures, and they are the three shapes a degraded surface takes:
 * nothing rendered at all, a required control missing, and a required control
 * rendered but unusable. The last one is the permission regression case, and
 * it is the only place this suite asserts that a control must be enabled: a
 * disabled control is otherwise reported, never counted as proof.
 */
export function floorProblems(floors: FloorFile, routes: VisitedRoute[]): string[] {
  const problems: string[] = [];
  for (const route of routes) {
    if (!route.visited) {
      continue;
    }
    if (route.enabled.length === 0 && route.disabled.length === 0) {
      problems.push(
        `route ${route.route} rendered no interactive control at all; an empty page cannot be measured and must not score as covered`,
      );
      continue;
    }
    const required = requirementsFor(floors, route.route);
    if (required.length === 0) {
      problems.push(
        `route ${route.route} has no entry in route-floors.json and is not under the shell prefix; record what it must render, or a later run that renders nothing there will report full coverage of an empty page`,
      );
      continue;
    }
    const enabled = new Set(route.enabled);
    const disabled = new Set(route.disabled);
    for (const control of required) {
      if (enabled.has(control)) {
        continue;
      }
      problems.push(
        disabled.has(control)
          ? `route ${route.route} rendered required control "${control}" disabled; either the surface regressed (a permission or a broken precondition) or the requirement is wrong, and both are decisions for this PR`
          : `route ${route.route} did not render required control "${control}"; the page is rendering less than it must, which shrinks the denominator and holds coverage up while the surface degrades`,
      );
    }
  }
  return problems;
}

/** Floor entries naming a route that no longer exists under app/. */
export function staleFloors(floors: FloorFile, discoveredPatterns: string[]): string[] {
  const known = new Set(discoveredPatterns);
  return Object.keys(floors.routes).filter((route) => !known.has(route));
}
