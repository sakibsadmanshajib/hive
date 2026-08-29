/**
 * Shared recharts theming for the analytics charts.
 *
 * Recharts has no notion of our palette, so every colour it draws has to be
 * handed to it explicitly. These values are `var(--color-*)` references rather
 * than hex literals so the charts follow the light/dark palette defined in
 * app/globals.css instead of staying frozen at their light-mode colours, which
 * is what made every chart invert against a dark page (issues #490, #1270).
 * SVG accepts a CSS custom property in `fill`/`stroke`, so the same string
 * works for both the SVG marks and the tooltip's DOM node.
 */

/** Panel the chart is drawn on, inside the surrounding card. */
export const CHART_PANEL_CLASS =
  "mb-6 rounded-xl bg-[var(--color-surface-inset)] p-4";

/** Height every chart renders at, reused by the empty state so nothing jumps. */
export const CHART_HEIGHT = 300;

export const chartGridStroke = "var(--color-border)";

export const chartAxis = {
  stroke: "var(--color-border-strong)",
  tick: { fontSize: 12, fill: "var(--color-ink-3)" },
} as const;

export const chartTooltip = {
  contentStyle: {
    backgroundColor: "var(--color-surface)",
    border: "1px solid var(--color-border)",
    borderRadius: "var(--radius-md)",
    color: "var(--color-ink)",
  },
  labelStyle: { color: "var(--color-ink-2)" },
  itemStyle: { color: "var(--color-ink)" },
} as const;

export const chartLegend = {
  wrapperStyle: { color: "var(--color-ink-2)", fontSize: 12 },
} as const;

/** Series colours, one per data series drawn anywhere on the analytics tabs. */
export const chartSeries = {
  inputTokens: "var(--color-accent)",
  outputTokens: "var(--color-success)",
  credits: "var(--color-success)",
  errors: "var(--color-danger)",
  totalRequests: "var(--color-ink-4)",
} as const;
