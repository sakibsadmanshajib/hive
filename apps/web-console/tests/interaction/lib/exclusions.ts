// Exclusion expiry.
//
// Two things can take a control out of the denominator: a skipped route in
// route-fixtures.json, and a declared entry in control-registry.json. Both
// were already required to carry an owner and a reason. Neither had an end.
//
// An exclusion with no end is how a denominator shrinks without anyone
// deciding it should. The blocker gets fixed, the exclusion outlives it, and
// the surface it was hiding is never measured again. So an exclusion that
// exists because something else is broken must name that something: an issue
// number. When the issue closes, the exclusion has expired and the gate says
// so by name.
//
// Exclusions that are not waiting on anything (a chart with no activation
// behaviour, a route that redirects by design) name no issue and never
// expire; they are permanent decisions, not deferrals. The difference is
// which of the two an entry declares, and it has to declare one.

import type { RegistryFile } from "./registry";
import type { RouteFixtureFile } from "./routes";

export interface Exclusion {
  /** Human readable identity, used in failure messages. */
  what: string;
  /** Issue that must still be open for this exclusion to be valid. */
  issue: number | null;
  /** True when the entry declared itself permanent rather than blocked. */
  permanent: boolean;
  owner: string;
}

export function collectExclusions(
  fixtures: RouteFixtureFile,
  registry: RegistryFile,
): Exclusion[] {
  const out: Exclusion[] = [];
  for (const [route, fixture] of Object.entries(fixtures.routes)) {
    if (fixture.skip === undefined || fixture.skip === "") {
      continue;
    }
    out.push({
      what: `route-fixtures.json skip of ${route}`,
      issue: fixture.issue ?? null,
      permanent: fixture.permanent === true,
      owner: fixture.owner ?? "",
    });
  }
  for (const entry of registry.entries) {
    out.push({
      what: `control-registry.json entry ${entry.route} :: ${entry.control}`,
      issue: entry.issue ?? null,
      permanent: entry.issue === undefined,
      owner: entry.owner,
    });
  }
  return out;
}

/**
 * A skipped route has to say what it is waiting for, or say that it is
 * permanent. A registry entry with no issue is permanent by construction.
 */
export function exclusionProblems(exclusions: Exclusion[]): string[] {
  const problems: string[] = [];
  for (const exclusion of exclusions) {
    if (exclusion.issue === null && !exclusion.permanent) {
      problems.push(
        `${exclusion.what} has no issue and is not declared permanent; an exclusion that names no blocker can never be found again once the blocker is fixed`,
      );
    }
    if (exclusion.issue !== null && exclusion.permanent) {
      problems.push(
        `${exclusion.what} declares itself permanent and also names issue ${String(exclusion.issue)}; it is one or the other`,
      );
    }
    if (exclusion.owner.trim() === "") {
      problems.push(`${exclusion.what} has no owner`);
    }
  }
  return problems;
}

/** Exclusions whose blocking issue has closed, so they no longer apply. */
export function expiredExclusions(
  exclusions: Exclusion[],
  closed: ReadonlySet<number>,
): string[] {
  return exclusions
    .filter((exclusion) => exclusion.issue !== null && closed.has(exclusion.issue))
    .map(
      (exclusion) =>
        `${exclusion.what} is excluded because issue ${String(exclusion.issue ?? 0)} blocks it, but that issue is closed; delete the exclusion and let the gate measure the surface, or cite the issue that still blocks it`,
    );
}
