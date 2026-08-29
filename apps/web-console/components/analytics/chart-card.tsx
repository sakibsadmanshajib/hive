import type { ReactNode } from "react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { CHART_HEIGHT } from "@/components/analytics/chart-theme";

interface ChartCardProps<T> {
  title: string;
  description?: string;
  /**
   * The rows the wrapped chart is drawn from. With none, the chart is not
   * rendered at all: recharts happily draws its axes over an empty series,
   * which left a new account staring at three bare axis frames with no
   * explanation of why they were empty (issue #516).
   */
  rows: ReadonlyArray<T>;
  emptyMessage?: string;
  children: ReactNode;
}

/** Titled card wrapper shared by every chart on the analytics tabs. */
export function ChartCard<T>({
  title,
  description,
  rows,
  emptyMessage,
  children,
}: ChartCardProps<T>) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{title}</CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
      </CardHeader>
      <CardContent className="px-5 py-5">
        {rows.length === 0 ? (
          <p
            className="flex items-center justify-center text-center text-sm text-[var(--color-ink-3)]"
            style={{ height: CHART_HEIGHT }}
          >
            {emptyMessage ?? "No activity in this time range yet."}
          </p>
        ) : (
          children
        )}
      </CardContent>
    </Card>
  );
}
