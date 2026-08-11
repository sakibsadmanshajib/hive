// Coverage accounting and the machine readable report.
//
// Control surface coverage = proven / enumerated, per route and overall.
// Declared controls (registry entries) sit outside the numerator on purpose:
// declaring a control inert is a way to record a decision, never a way to
// inflate the number.

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import type { ControlKind } from "./enumerate";

export type ControlStatus = "proven" | "declared" | "disabled" | "unproven";

export interface ControlRecord {
  route: string;
  url: string;
  key: string;
  kind: ControlKind;
  name: string;
  /** Why the enumerator picked this element up. */
  matchedBy: string;
  /** Chain of controls that had to be activated to reveal this one. */
  revealPath: string[];
  status: ControlStatus;
  proofType: string;
  detail: string;
  declaredKind?: string;
  owner?: string;
}

export interface RouteRecord {
  route: string;
  url: string;
  visited: boolean;
  note: string;
  enumerated: number;
  proven: number;
  declared: number;
  unproven: number;
  coverage: number;
}

export interface CoverageReport {
  generatedAt: string;
  baseUrl: string;
  partial: boolean;
  totals: {
    routesDiscovered: number;
    routesVisited: number;
    /** Primary: distinct control identities, independent of row counts. */
    identities: number;
    identitiesProven: number;
    identityCoverage: number;
    /** Secondary and not comparable between runs: raw instances. */
    enumerated: number;
    proven: number;
    declared: number;
    /** Rendered disabled: never proof, never a failure on its own. */
    disabled: number;
    unproven: number;
    coverage: number;
  };
  proofTypes: Record<string, number>;
  routes: RouteRecord[];
  controls: ControlRecord[];
  unprovenControls: ControlRecord[];
  /**
   * Controls the run found disabled.
   *
   * Listed rather than counted as proof. A disabled control used to pass this
   * gate on any `title` attribute, which made greying a surface out the
   * cheapest way to stay green. What fails an accidental disable now is
   * route-floors.json, which names the controls each route must leave usable.
   */
  disabledControls: ControlRecord[];
  /** Registry and route-fixture integrity failures. */
  problems: string[];
}

/**
 * Collapses an instance key to the identity it is an instance of.
 *
 * `controlKey` appends an ordinal to a duplicate, so a list that renders the
 * same row action forty times produces forty keys differing only by that
 * ordinal. Counting those as forty controls makes the denominator track how
 * much data the account holds rather than what the product offers, and two
 * runs against different data are then not comparable. One identity is one
 * thing a user can do.
 */
export function identityOf(route: string, key: string): string {
  return route + " " + key.replace(/~\d+$/, "");
}

/**
 * Coverage as a fraction, with no denominator of zero scoring as full marks.
 *
 * It used to return 1 for 0 of 0, which read as "everything here is proven"
 * for a route that rendered nothing at all. Two routes shipped in that state:
 * /oauth/consent, whose recorded floor was literally zero controls, and
 * /invitations/accept. Both reported one hundred percent coverage of a page
 * the gate had never seen a single control on. An empty measurement is not a
 * perfect one, and the sweep additionally raises a problem for any visited
 * route that enumerates nothing, so this can only be reached by a route that
 * was never visited.
 */
export function ratio(proven: number, enumerated: number): number {
  if (enumerated === 0) {
    return 0;
  }
  return Math.round((proven / enumerated) * 10000) / 10000;
}

export function buildReport(input: {
  baseUrl: string;
  partial: boolean;
  routesDiscovered: number;
  routes: RouteRecord[];
  controls: ControlRecord[];
  problems: string[];
}): CoverageReport {
  const proven = input.controls.filter((c) => c.status === "proven").length;
  // Primary figure. An identity counts as proven only when every instance of
  // it proved, so a control that works in one row and fails in another is
  // unproven; the gate fails on the failing instance regardless, because it
  // asserts that the unproven list is empty.
  const byIdentity = new Map<string, { instances: number; proven: number }>();
  for (const control of input.controls) {
    const id = identityOf(control.route, control.key);
    const cell = byIdentity.get(id) ?? { instances: 0, proven: 0 };
    cell.instances += 1;
    if (control.status === "proven") cell.proven += 1;
    byIdentity.set(id, cell);
  }
  const identities = byIdentity.size;
  const identitiesProven = [...byIdentity.values()].filter(
    (cell) => cell.proven === cell.instances,
  ).length;
  const declared = input.controls.filter((c) => c.status === "declared").length;
  const disabledControls = input.controls.filter((c) => c.status === "disabled");
  const unprovenControls = input.controls.filter((c) => c.status === "unproven");
  const proofTypes: Record<string, number> = {};
  for (const control of input.controls) {
    proofTypes[control.proofType] = (proofTypes[control.proofType] ?? 0) + 1;
  }
  return {
    generatedAt: new Date().toISOString(),
    baseUrl: input.baseUrl,
    partial: input.partial,
    totals: {
      routesDiscovered: input.routesDiscovered,
      routesVisited: input.routes.filter((r) => r.visited).length,
      identities,
      identitiesProven,
      identityCoverage: ratio(identitiesProven, identities),
      enumerated: input.controls.length,
      proven,
      declared,
      disabled: disabledControls.length,
      unproven: unprovenControls.length,
      coverage: ratio(proven, input.controls.length),
    },
    proofTypes,
    routes: input.routes,
    controls: input.controls,
    unprovenControls,
    disabledControls,
    problems: input.problems,
  };
}

export function writeReport(dir: string, report: CoverageReport): string {
  mkdirSync(dir, { recursive: true });
  const file = join(dir, report.partial ? "coverage.partial.json" : "coverage.json");
  writeFileSync(file, `${JSON.stringify(report, null, 2)}\n`, "utf8");
  return file;
}

function percent(value: number): string {
  return `${(value * 100).toFixed(1)}%`;
}

export function formatSummary(report: CoverageReport): string {
  const lines: string[] = [];
  lines.push("");
  lines.push("Interaction coverage");
  lines.push(`  origin        ${report.baseUrl}`);
  lines.push(
    `  routes        ${String(report.totals.routesVisited)} visited of ${String(report.totals.routesDiscovered)} discovered`,
  );
  lines.push(
    `  controls      ${String(report.totals.enumerated)} enumerated, ${String(report.totals.proven)} proven, ${String(report.totals.declared)} declared, ${String(report.totals.disabled)} disabled, ${String(report.totals.unproven)} unproven`,
  );
  lines.push(
    `  identities    ${String(report.totals.identitiesProven)} proven of ${String(report.totals.identities)} distinct`,
  );
  lines.push(`  COVERAGE      ${percent(report.totals.identityCoverage)}  (distinct control identities)`);
  lines.push(
    `  instances     ${percent(report.totals.coverage)}  (secondary, moves with row counts, not comparable between runs)`,
  );
  if (report.partial) {
    lines.push("  PARTIAL RUN: a route filter was applied, this number is not the gate number");
  }
  lines.push("");
  lines.push("  route                                    enum  proven  decl  unprov  coverage");
  for (const route of [...report.routes].sort((a, b) => a.coverage - b.coverage)) {
    const label = route.visited ? route.route : `${route.route} (not visited: ${route.note})`;
    lines.push(
      `  ${label.padEnd(40).slice(0, 40)}  ${String(route.enumerated).padStart(4)}  ${String(route.proven).padStart(6)}  ${String(route.declared).padStart(4)}  ${String(route.unproven).padStart(6)}  ${percent(route.coverage).padStart(8)}`,
    );
  }
  if (report.disabledControls.length > 0) {
    lines.push("");
    lines.push(
      `  DISABLED CONTROLS (${String(report.disabledControls.length)}) — not proof of anything, and not counted as covered`,
    );
    for (const control of report.disabledControls) {
      lines.push(`    ${control.route}  ${control.key}`);
      lines.push(`      ${control.detail}`);
    }
  }
  if (report.unprovenControls.length > 0) {
    lines.push("");
    lines.push(`  UNPROVEN CONTROLS (${String(report.unprovenControls.length)})`);
    for (const control of report.unprovenControls) {
      const reveal =
        control.revealPath.length > 0 ? ` [revealed by ${control.revealPath.join(" > ")}]` : "";
      lines.push(`    ${control.route}  ${control.key}${reveal}`);
      lines.push(`      ${control.proofType}: ${control.detail}`);
    }
  }
  if (report.problems.length > 0) {
    lines.push("");
    lines.push(`  INTEGRITY PROBLEMS (${String(report.problems.length)})`);
    for (const problem of report.problems) {
      lines.push(`    ${problem}`);
    }
  }
  lines.push("");
  return lines.join("\n");
}
