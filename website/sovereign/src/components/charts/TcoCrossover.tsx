// TCO crossover chart. The ONE Recharts island in the whole site, reserved for
// the /pricing page only and mounted with client:visible.
//
// It draws a 36-month cumulative-cost BAND (on-prem range, Strix lower bound to
// DGX upper bound) against the cloud-API line, with a single gold breakeven
// annotation. Recharts writes inline style attributes on SVG nodes, which is
// why /pricing carries the scoped style-src-attr 'unsafe-inline' relaxation in
// public/_headers. No other route loads this component.
import {
  ComposedChart,
  Area,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
} from 'recharts';

const TEAL = '#2dd4bf';
const GREY = '#6b7585';
const GOLD = '#f0b429';
const GRID = '#1e2733';
const AXIS = '#828b99';

export interface TcoPoint {
  month: number;
  // On-prem band bounds (cumulative USD).
  onpremLow: number;
  onpremHigh: number;
  // Cloud API cumulative USD.
  cloud: number;
}

interface Props {
  data: TcoPoint[];
  // Month index where the entry box breaks even versus cloud.
  breakevenMonth: number;
}

export default function TcoCrossover({ data, breakevenMonth }: Props) {
  return (
    <div style={{ width: '100%', height: 360 }}>
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart
          data={data}
          margin={{ top: 16, right: 24, bottom: 24, left: 8 }}
        >
          <CartesianGrid stroke={GRID} vertical={false} />
          <XAxis
            dataKey="month"
            stroke={AXIS}
            tick={{ fill: AXIS, fontFamily: 'Geist Mono, monospace', fontSize: 12 }}
            tickLine={false}
            label={{
              value: 'Month',
              position: 'insideBottom',
              offset: -8,
              fill: AXIS,
              fontSize: 12,
            }}
          />
          <YAxis
            stroke={AXIS}
            tick={{ fill: AXIS, fontFamily: 'Geist Mono, monospace', fontSize: 12 }}
            tickLine={false}
            tickFormatter={(v: number) => `$${v.toLocaleString()}`}
            width={72}
          />
          <Tooltip
            contentStyle={{
              background: '#141b26',
              border: '1px solid #1e2733',
              borderRadius: 10,
              color: '#f5f7fa',
              fontFamily: 'Geist Mono, monospace',
              fontSize: 12,
            }}
            formatter={(value: number, name: string) => [`$${value.toLocaleString()}`, name]}
            labelFormatter={(label: number) => `Month ${label}`}
          />
          {/* On-prem band: area between low and high bounds. */}
          <Area
            type="monotone"
            dataKey="onpremHigh"
            stroke={TEAL}
            strokeWidth={2}
            fill={TEAL}
            fillOpacity={0.14}
            name="On-prem (range)"
            isAnimationActive={false}
          />
          <Area
            type="monotone"
            dataKey="onpremLow"
            stroke={TEAL}
            strokeWidth={1}
            strokeDasharray="4 4"
            fill="#141b26"
            fillOpacity={1}
            name="On-prem lower bound"
            isAnimationActive={false}
          />
          {/* Cloud API line: competitor grey. */}
          <Line
            type="monotone"
            dataKey="cloud"
            stroke={GREY}
            strokeWidth={2}
            dot={false}
            name="Cloud API"
            isAnimationActive={false}
          />
          {/* Single gold breakeven annotation. */}
          <ReferenceLine
            x={breakevenMonth}
            stroke={GOLD}
            strokeWidth={1.5}
            label={{
              value: `Breakeven ~month ${breakevenMonth}`,
              position: 'top',
              fill: GOLD,
              fontSize: 12,
              fontWeight: 600,
            }}
          />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  );
}
