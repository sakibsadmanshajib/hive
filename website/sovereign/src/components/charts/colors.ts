// Chart colour mapping. Charts read token names ('teal' | 'grey' | 'red' |
// 'gold') from the data layer and resolve them to hex here so the SVG and the
// Recharts island agree.
//
// Semantics (do not cross the streams):
//   cost charts: teal = Hive / open, grey = competitor, gold = single callout.
//   risk charts: teal = sovereign / safe, red = exposed.
// The competitor series is GREY, never red.
export type ChartColorName = 'teal' | 'grey' | 'red' | 'gold';

export const CHART_COLORS: Record<ChartColorName, string> = {
  teal: '#2dd4bf',
  grey: '#6b7585',
  red: '#f04438',
  gold: '#f0b429',
};

// Horizontal gridlines use this and only this colour.
export const GRIDLINE_COLOR = '#1e2733';
export const AXIS_TEXT_COLOR = '#828b99';
export const LABEL_TEXT_COLOR = '#aeb6c2';

export function resolveColor(name: ChartColorName | undefined): string {
  return CHART_COLORS[name ?? 'grey'];
}
