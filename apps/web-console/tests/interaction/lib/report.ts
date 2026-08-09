// Coverage accounting and the machine readable report.
//
// Control surface coverage = proven / enumerated, per route and overall.
// Declared controls (registry entries) sit outside the numerator on purpose:
// declaring a control inert is a way to record a decision, never a way to
// inflate the number.

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

import type { ControlKind } from "./enumerate";

export type ControlStatus = "proven" | "declared" | "unproven";

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
    enumerated: number;
    proven: number;
    declared: number;
    unproven: number;
    coverage: number;
  };
  proofTypes: Record<string, number>;
  routes: RouteRecord[];
  controls: ControlRecord[];
  unprovenControls: ControlRecord[];
  /** Registry and route-fixture integrity failures. */
  problems: string[];
}

export function ratio(proven: number, enumerated: number): number {
  if (enumerated === 0) {
    return 1;
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
  const declared = input.controls.filter((c) => c.status === "declared").length;
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
      enumerated: input.controls.length,
      proven,
      declared,
      unproven: unprovenControls.length,
      coverage: ratio(proven, input.controls.length),
    },
    proofTypes,
    routes: input.routes,
    controls: input.controls,
    unprovenControls,
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
    `  controls      ${String(report.totals.enumerated)} enumerated, ${String(report.totals.proven)} proven, ${String(report.totals.declared)} declared, ${String(report.totals.unproven)} unproven`,
  );
  lines.push(`  COVERAGE      ${percent(report.totals.coverage)}`);
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
