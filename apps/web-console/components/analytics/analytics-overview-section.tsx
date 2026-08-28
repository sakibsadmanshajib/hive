import type { UsageSummaryRow } from "@/lib/control-plane/client";
import type {
  OverviewTiles,
  PeriodDelta,
} from "@/lib/analytics/cache-metrics";
import { CachedVsUncachedCard } from "@/components/analytics/cached-vs-uncached-card";
import { ChartCard } from "@/components/analytics/chart-card";
import { Sparkline } from "@/components/analytics/sparkline";
import { TopApiKeysCard } from "@/components/analytics/top-api-keys-card";
import { UsageChart } from "@/components/analytics/usage-chart";
import { Card, CardContent } from "@/components/ui/card";
import { cn } from "@/lib/cn";
import { formatCredits, formatNumber, formatPercent } from "@/lib/format/credits";
import { formatUsdFromCredits } from "@/lib/format/model-pricing";

interface SummaryCardProps {
  label: string;
  value: string;
  // Says what the number above actually covers. Required on any tile whose
  // value is derived rather than read straight off the server aggregate, so
  // the sample or the formula is never left implicit.
  note?: string;
  testId?: string;
  // Percent change vs the prior equal-length period, computed from the same
  // full server aggregate as `value` (see derivePeriodDelta). Omitted
  // entirely, not rendered as an em-dash, when the window has no defined
  // prior period (a custom range) -- `note` carries the explanation instead
  // (deriveOverviewTiles' windowUnsupportedNote).
  delta?: PeriodDelta;
  // Per-bucket trend across the bounded usage_events sample the cache tile
  // already fetches. A sample, not the full window; sparklineCaption states
  // exactly which sample, both visibly and via the chart's own aria-label.
  sparkline?: ReadonlyArray<number | null>;
  sparklineCaption?: string;
}

function formatDeltaPercent(percent: number): string {
  const magnitude = Math.abs(percent);
  const digits = magnitude >= 100 ? 0 : 1;
  return `${magnitude.toFixed(digits)}%`;
}

function DeltaRow({ delta }: { delta: PeriodDelta }) {
  if (delta.unavailable) {
    return (
      <p className="text-2xs leading-tight text-[var(--color-ink-3)]">
        Unavailable
      </p>
    );
  }
  // A measured prior of exactly zero, with a real move off it. Percent change
  // against zero is undefined, but the move is not, so the tile states the
  // move rather than claiming there was no prior figure. An account whose
  // cache hit rate went from 0% to 20% rose; it did not lack a prior period.
  if (delta.fromZero) {
    const zeroTone =
      delta.direction === "down"
        ? "text-2xs leading-tight text-[var(--color-danger)]"
        : "text-2xs leading-tight text-[var(--color-success)]";
    const zeroArrow = delta.direction === "down" ? "↓" : "↑";
    return (
      <p className={zeroTone}>
        {zeroArrow} from zero in the prior period
      </p>
    );
  }
  // No prior figure exists to compare against at all.
  if (delta.percent === null) {
    return (
      <p className="text-2xs leading-tight text-[var(--color-ink-3)]">
        No prior data
      </p>
    );
  }
  const colorClass =
    delta.direction === "up"
      ? "text-[var(--color-success)]"
      : delta.direction === "down"
        ? "text-[var(--color-danger)]"
        : "text-[var(--color-ink-3)]";
  const arrow =
    delta.direction === "up" ? "↑" : delta.direction === "down" ? "↓" : "→";
  return (
    <p className={cn("text-2xs leading-tight", colorClass)}>
      {arrow} {formatDeltaPercent(delta.percent)} vs prior period
    </p>
  );
}

function SummaryCard({
  label,
  value,
  note,
  testId,
  delta,
  sparkline,
  sparklineCaption,
}: SummaryCardProps) {
  return (
    <Card>
      <CardContent className="flex items-start justify-between gap-3 px-5 py-5">
        <div className="flex min-w-0 flex-col gap-1">
          <p className="text-2xs font-medium uppercase tracking-wider text-[var(--color-ink-3)]">
            {label}
          </p>
          <p
            className="metric text-2xl text-[var(--color-ink)]"
            data-numeric
            data-testid={testId}
          >
            {value}
          </p>
          {delta ? <DeltaRow delta={delta} /> : null}
          {note ? (
            <p className="text-2xs leading-tight text-[var(--color-ink-3)]">
              {note}
            </p>
          ) : null}
        </div>
        {sparkline ? (
          <div className="flex shrink-0 flex-col items-end gap-1 pt-1">
            <Sparkline
              values={sparkline}
              label={sparklineCaption ?? "Recent trend"}
            />
            {sparklineCaption ? (
              <span className="text-2xs leading-tight text-[var(--color-ink-4)]">
                {sparklineCaption}
              </span>
            ) : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

interface AnalyticsOverviewSectionProps {
  tiles: OverviewTiles;
  usageData: UsageSummaryRow[];
}

/**
 * The six summary tiles, the usage chart, and the two supporting panels
 * (cached-vs-uncached, top API keys by spend) on the analytics overview tab.
 * All of it reads from `tiles`, a plain data object deriveOverviewTiles
 * (lib/analytics/cache-metrics.ts) already computed -- this component is
 * presentation only.
 */
export function AnalyticsOverviewSection({
  tiles,
  usageData,
}: AnalyticsOverviewSectionProps) {
  const blendedNote =
    tiles.blendedNoteKind === "no-tokens"
      ? "No tokens served in this window."
      : tiles.blendedNoteKind === "window-unsupported"
        ? (tiles.windowUnsupportedNote ?? "No comparison for this window.")
        : `${formatNumber(tiles.blendedCreditsPerMillion as number)} credits. Credits spent divided by input plus output tokens, per million. Effective, so cache reads are already priced in.`;

  return (
    <div className="flex flex-col gap-6">
      <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
        <SummaryCard
          label="Total requests"
          value={formatCredits(tiles.totalRequests)}
          delta={tiles.requestsDelta}
          sparkline={tiles.requestsSparkline}
          sparklineCaption={tiles.sparklineCaption}
          note={tiles.windowUnsupportedNote}
        />
        <SummaryCard
          label="Input tokens"
          value={formatCredits(tiles.totalInputTokens)}
          delta={tiles.inputTokensDelta}
          sparkline={tiles.inputTokensSparkline}
          sparklineCaption={tiles.sparklineCaption}
          note={tiles.windowUnsupportedNote}
        />
        <SummaryCard
          label="Output tokens"
          value={formatCredits(tiles.totalOutputTokens)}
          delta={tiles.outputTokensDelta}
          sparkline={tiles.outputTokensSparkline}
          sparklineCaption={tiles.sparklineCaption}
          note={tiles.windowUnsupportedNote}
        />
        <SummaryCard
          label="Total spend"
          value={formatUsdFromCredits(tiles.totalCreditsSpent)}
          delta={tiles.creditsDelta}
          sparkline={tiles.creditsSparkline}
          sparklineCaption={tiles.sparklineCaption}
          note={tiles.windowUnsupportedNote}
          testId="total-spend"
        />
        <SummaryCard
          label="Cache hit rate"
          value={formatPercent(tiles.cacheHitRate)}
          note={tiles.cacheHitNote}
          delta={tiles.cacheHitDelta}
          sparkline={tiles.cacheHitSparkline}
          sparklineCaption={tiles.sparklineCaption}
          testId="cache-hit-rate"
        />
        <SummaryCard
          label="Blended price / 1M"
          value={
            tiles.blendedCreditsPerMillion === null
              ? "—"
              : formatUsdFromCredits(tiles.blendedCreditsPerMillion)
          }
          note={blendedNote}
          delta={tiles.blendedDelta}
          testId="blended-credits-per-million"
        />
      </section>
      <ChartCard title="Usage" description="Requests and tokens.">
        <UsageChart data={usageData} />
      </ChartCard>
      <div className="grid gap-6 lg:grid-cols-2">
        <CachedVsUncachedCard
          cachedTokens={tiles.cachedTokens}
          uncachedTokens={tiles.uncachedTokens}
          hasData={tiles.hasCacheSplit}
          emptyNote={tiles.cacheHitNote}
        />
        <TopApiKeysCard topKeys={tiles.topKeys} failed={tiles.topKeysFailed} />
      </div>
    </div>
  );
}
