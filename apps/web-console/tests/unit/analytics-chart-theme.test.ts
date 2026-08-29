import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// Guard for issues #490 and #1270. The analytics charts used to hardcode hex
// colours, which froze them at their light-mode values and left every chart
// inverted against the dark palette. Recharts takes its colours as props, so
// there is nothing a stylesheet can do about a hex literal that creeps back
// in: this reads the sources and fails if one does.

const CHART_DIR = resolve(__dirname, "../../components/analytics");

const chart = (name: string): string =>
  readFileSync(resolve(CHART_DIR, name), "utf8");

const CHARTS = ["usage-chart.tsx", "spend-chart.tsx", "error-chart.tsx"];

describe("analytics charts follow the themed palette", () => {
  it.each(CHARTS)("%s carries no hex colour literal", (name) => {
    expect(chart(name)).not.toMatch(/["']#[0-9a-fA-F]{3,8}["']/);
  });

  it.each(CHARTS)("%s takes its axes and tooltip from the shared theme", (name) => {
    const src = chart(name);
    expect(src).toContain("chartAxis");
    expect(src).toContain("chartTooltip");
    expect(src).toContain("chartGridStroke");
  });

  it.each(CHARTS)("%s draws every series from the shared palette", (name) => {
    expect(chart(name)).toContain("chartSeries.");
  });

  it("defines every series colour as a CSS variable, not a literal", () => {
    const theme = chart("chart-theme.ts");
    expect(theme).not.toMatch(/["']#[0-9a-fA-F]{3,8}["']/);
    const vars = theme.match(/var\(--color-[a-z0-9-]+\)/g) ?? [];
    expect(vars.length).toBeGreaterThan(5);
  });
});
