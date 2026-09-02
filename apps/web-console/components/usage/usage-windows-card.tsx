import type { ReactElement } from "react";

import type { UsageWindow, UsageWindows } from "@/lib/control-plane/client";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

/**
 * Session and weekly consumption, as percentages with their reset times.
 *
 * Percentages and nothing else. The allowance behind them is a credit score,
 * credits convert to dollars by the constant this console publishes, and a
 * subscription's internal credit value is confidential (D-068), so a figure
 * here would hand every subscriber the peg (D-070).
 *
 * Before this card the console showed no rate limit anywhere: the only bar on
 * the API keys page is credit spend against a budget cap, which reads like a
 * rate limit and is not one (issue #1725).
 */
interface UsageWindowsCardProps {
  windows: UsageWindows | null;
}

const WINDOW_LABELS: Record<string, string> = {
  session: "Session",
  weekly: "Weekly",
};

function windowLabel(name: string): string {
  return WINDOW_LABELS[name] ?? name;
}

/**
 * How the window behaves, in the customer's words. The copy and the mechanism
 * have to agree: the session window slides continuously, the weekly one
 * restores in full at the account's anchor (D-069). Saying "resets" about a
 * sliding window would be a promise the limiter does not keep.
 */
function windowExplanation(window: UsageWindow): string {
  if (window.anchored) {
    return "Restores in full at your weekly reset.";
  }
  return "Measured over the last five hours, freeing up continuously.";
}

function formatReset(value: string | null): string | null {
  if (value === null || value === "") return null;
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return null;
  return parsed.toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

function WindowRow({ window }: { window: UsageWindow }): ReactElement {
  const label = windowLabel(window.window);

  if (!window.configured) {
    // Absence is stated, not drawn. An empty bar here would claim a limit the
    // account does not have, which is the same class of defect as a bar that
    // claims one it has already exceeded.
    return (
      <div
        className="flex items-baseline justify-between gap-3"
        data-testid={`usage-window-${window.window}`}
      >
        <span className="text-sm font-medium text-[var(--color-ink)]">
          {label}
        </span>
        <span className="text-sm text-[var(--color-ink-3)]">
          No limit configured
        </span>
      </div>
    );
  }

  const percent = Math.min(Math.max(window.used_percent, 0), 100);
  const reached = percent >= 100;
  const resetText = formatReset(window.resets_at);

  return (
    <div
      className="flex flex-col gap-1.5"
      data-testid={`usage-window-${window.window}`}
    >
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-sm font-medium text-[var(--color-ink)]">
          {label}
        </span>
        <span className="flex items-center gap-1.5">
          {reached ? (
            <Badge tone="danger" className="whitespace-nowrap">
              Limit reached
            </Badge>
          ) : null}
          <span
            className="text-xs tabular-nums text-[var(--color-ink-2)]"
            data-testid={`usage-window-${window.window}-percent`}
          >
            {percent}% used
          </span>
        </span>
      </div>
      <progress
        value={percent}
        max={100}
        aria-label={`${label} allowance used: ${percent} percent`}
        className="h-1.5 w-full overflow-hidden rounded-full [&::-webkit-progress-bar]:bg-[var(--color-surface-3)] [&::-webkit-progress-value]:bg-[var(--color-ink-2)] [&::-moz-progress-bar]:bg-[var(--color-ink-2)]"
      />
      <p className="text-xs text-[var(--color-ink-3)]">
        {windowExplanation(window)}
        {resetText === null ? null : (
          <>
            {" "}
            <span data-testid={`usage-window-${window.window}-reset`}>
              Resets {resetText}.
            </span>
          </>
        )}
      </p>
    </div>
  );
}

export function UsageWindowsCard({
  windows,
}: UsageWindowsCardProps): ReactElement {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Usage limits</CardTitle>
        <CardDescription>
          How much of each allowance this account has used. Hive may adjust an
          allowance, so these are proportions rather than a fixed quantity.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {windows === null || windows.windows.length === 0 ? (
          // Unavailable, said plainly. Rendering empty bars instead would tell
          // the customer they have used nothing, which is the opposite claim.
          <p className="text-sm text-[var(--color-ink-3)]">
            Usage limits are unavailable right now. This does not affect your
            requests.
          </p>
        ) : (
          windows.windows.map((window) => (
            <WindowRow key={window.window} window={window} />
          ))
        )}
      </CardContent>
    </Card>
  );
}
