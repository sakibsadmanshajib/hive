import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { formatCredits, formatPercent } from "@/lib/format/credits";

interface CacheSplitBarProps {
  cachedTokens: number;
  uncachedTokens: number;
}

// Single two-segment bar, cached share first. Renders an all-uncached bar
// rather than an empty one when cachedTokens is a real, measured zero (a
// window with prompt tokens but no cache hits at all) -- distinct from the
// "no sample" case the caller already routes to its own note instead of
// rendering this component at all.
function CacheSplitBar({ cachedTokens, uncachedTokens }: CacheSplitBarProps) {
  const total = cachedTokens + uncachedTokens;
  const cachedPercent = total > 0 ? (cachedTokens / total) * 100 : 0;
  return (
    <div
      className="flex h-3 w-full overflow-hidden rounded-full bg-[var(--color-surface-2)]"
      role="img"
      aria-label={`${formatPercent(total > 0 ? cachedTokens / total : null)} of prompt tokens served from cache`}
    >
      <div
        className="h-full bg-[var(--color-success)]"
        style={{ width: `${cachedPercent}%` }}
      />
    </div>
  );
}

interface CachedVsUncachedCardProps {
  cachedTokens: number;
  uncachedTokens: number;
  /** True only when the sample actually carries prompt tokens to split. */
  hasData: boolean;
  /** Shown in place of the bar when hasData is false, same text as the cache-hit tile's own note. */
  emptyNote: string;
}

export function CachedVsUncachedCard({
  cachedTokens,
  uncachedTokens,
  hasData,
  emptyNote,
}: CachedVsUncachedCardProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Cached vs uncached</CardTitle>
        <CardDescription>
          Prompt tokens served from cache versus tokens re-processed fresh,
          over the same bounded sample as the cache hit rate tile above.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-5 py-5">
        {hasData ? (
          <div className="flex flex-col gap-4">
            <CacheSplitBar
              cachedTokens={cachedTokens}
              uncachedTokens={uncachedTokens}
            />
            <dl className="grid grid-cols-2 gap-3 text-sm">
              <div>
                <dt className="text-2xs uppercase tracking-wider text-[var(--color-ink-3)]">
                  Cached
                </dt>
                <dd className="metric text-lg text-[var(--color-ink)]">
                  {formatCredits(cachedTokens)}
                </dd>
              </div>
              <div>
                <dt className="text-2xs uppercase tracking-wider text-[var(--color-ink-3)]">
                  Uncached
                </dt>
                <dd className="metric text-lg text-[var(--color-ink)]">
                  {formatCredits(uncachedTokens)}
                </dd>
              </div>
            </dl>
          </div>
        ) : (
          <p className="text-sm text-[var(--color-ink-3)]">{emptyNote}</p>
        )}
      </CardContent>
    </Card>
  );
}
