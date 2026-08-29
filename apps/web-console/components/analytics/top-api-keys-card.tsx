import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import type { TopKeyRow } from "@/lib/analytics/cache-metrics";
import { formatUsdFromCredits } from "@/lib/format/model-pricing";

interface TopApiKeysCardProps {
  topKeys: TopKeyRow[];
  /**
   * True when the fetch behind this panel failed, as opposed to succeeding
   * with a real empty result. Rendered as a distinct "Unavailable" state:
   * an account with real per-key spend that saw "No API keys with spend"
   * during an outage would reasonably, and wrongly, conclude it had none.
   */
  failed: boolean;
}

export function TopApiKeysCard({ topKeys, failed }: TopApiKeysCardProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Top API keys by spend</CardTitle>
        <CardDescription>
          Your own account&apos;s keys, ranked by credits charged this
          window.
        </CardDescription>
      </CardHeader>
      <CardContent className="px-5 py-5">
        {failed ? (
          <p className="text-sm text-[var(--color-ink-3)]">Unavailable.</p>
        ) : topKeys.length > 0 ? (
          <ol className="flex flex-col gap-3">
            {topKeys.map((row, index) => (
              <li
                key={row.id}
                className="flex items-center justify-between gap-3 text-sm"
              >
                <span className="flex min-w-0 flex-1 items-center gap-2 text-[var(--color-ink)]">
                  <span className="text-2xs text-[var(--color-ink-3)]">
                    {index + 1}
                  </span>
                  {/* min-w-0 is required here, not just on the parent flex
                      row: Tailwind's truncate relies on min-width: 0, and a
                      flex item's default min-width is content-based `auto`
                      regardless of what its flex parent sets. Without it a
                      long nickname overflows instead of eliding. */}
                  <span className="min-w-0 flex-1 truncate">{row.label}</span>
                  <span className="shrink-0 text-2xs text-[var(--color-ink-3)]">
                    {row.suffix}
                  </span>
                </span>
                <span className="metric shrink-0 text-[var(--color-ink)]">
                  {formatUsdFromCredits(row.credits)}
                </span>
              </li>
            ))}
          </ol>
        ) : (
          <p className="text-sm text-[var(--color-ink-3)]">
            No API keys with spend in this window.
          </p>
        )}
      </CardContent>
    </Card>
  );
}
