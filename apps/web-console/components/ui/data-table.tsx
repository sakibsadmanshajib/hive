import * as React from "react";

import { cn } from "@/lib/cn";

export interface Column<T> {
  key: string;
  header: React.ReactNode;
  cell: (row: T) => React.ReactNode;
  className?: string;
  align?: "left" | "right" | "center";
  numeric?: boolean;
}

export interface DataTableProps<T> {
  rows: ReadonlyArray<T>;
  columns: ReadonlyArray<Column<T>>;
  rowKey: (row: T) => string;
  empty?: React.ReactNode;
  // While true the body shows a pending row instead of the empty state, so a
  // table that is still fetching does not read as an empty account. Rows that
  // are already on screen stay on screen: a background refresh should not
  // blank a populated table.
  loading?: boolean;
  className?: string;
  // Optional expandable detail row: when expandedKey matches a row's key,
  // renderDetail fills a full-width row directly beneath it and the row is
  // clickable/keyboard-toggleable. All props are optional so existing tables
  // are untouched.
  expandedKey?: string | null;
  renderDetail?: (row: T) => React.ReactNode;
  onRowToggle?: (key: string) => void;
}

export function DataTable<T>({
  rows,
  columns,
  rowKey,
  empty,
  loading = false,
  className,
  expandedKey = null,
  renderDetail,
  onRowToggle,
}: DataTableProps<T>) {
  const interactive = renderDetail !== undefined && onRowToggle !== undefined;
  return (
    // `relative overflow-x-auto`, and both words are load-bearing.
    //
    // overflow-x-auto rather than overflow-hidden: every console table is
    // wider than a 375px viewport, and hidden clipped the columns past the
    // fold away with no way to reach them. On a phone the API keys table
    // simply stopped after Name.
    //
    // relative because an over-wide <table> inside a non-positioned
    // overflow:auto box still propagates its layout overflow to the viewport
    // in Chromium, so the whole document scrolled sideways even though the
    // scroller itself was the right width. Measured at 375px on
    // /console/api-keys: the wrapper was already 343px with scrollWidth 802,
    // body scrollWidth was 375, and window.scrollX still reached 428.
    // Making the scroller a containing block takes it to 0 (issue #1367,
    // second half; /console/catalog was the same defect and the same fix).
    <div
      // Focusable so a keyboard-only user can scroll the overflow region at
      // all (WCAG 2.1.1). No role is set with it: a region without an
      // accessible name is worse than none, and the table inside already
      // carries the semantics.
      tabIndex={0}
      className={cn(
        "relative overflow-x-auto rounded-lg border border-[var(--color-border)]",
        "bg-[var(--color-surface)]",
        className,
      )}
    >
      <table className="w-full text-left text-sm">
        <thead>
          <tr className="border-b border-[var(--color-border)] bg-[var(--color-surface-inset)]">
            {columns.map((col) => (
              <th
                key={col.key}
                scope="col"
                className={cn(
                  "px-4 py-2.5 text-2xs font-medium uppercase tracking-wider",
                  "text-[var(--color-ink-3)]",
                  col.align === "right" && "text-right",
                  col.align === "center" && "text-center",
                  col.numeric && "tabular-nums",
                  col.className,
                )}
              >
                {col.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody aria-busy={loading}>
          {loading && rows.length === 0 ? (
            <tr>
              <td
                colSpan={columns.length}
                className="px-4 py-10 text-center text-sm text-[var(--color-ink-3)]"
              >
                Loading...
              </td>
            </tr>
          ) : rows.length === 0 ? (
            <tr>
              <td
                colSpan={columns.length}
                className="px-4 py-10 text-center text-sm text-[var(--color-ink-3)]"
              >
                {empty ?? "No records yet."}
              </td>
            </tr>
          ) : (
            rows.map((row) => {
              const key = rowKey(row);
              const isExpanded = interactive && expandedKey === key;
              return (
                <React.Fragment key={key}>
                  <tr
                    aria-expanded={interactive ? isExpanded : undefined}
                    onClick={interactive ? () => onRowToggle?.(key) : undefined}
                    onKeyDown={
                      interactive
                        ? (e) => {
                            if (e.key === "Enter" || e.key === " ") {
                              e.preventDefault();
                              onRowToggle?.(key);
                            }
                          }
                        : undefined
                    }
                    tabIndex={interactive ? 0 : undefined}
                    className={cn(
                      "border-b border-[var(--color-border)] last:border-b-0",
                      "transition-colors duration-[var(--duration-fast)]",
                      "hover:bg-[var(--color-surface-inset)]",
                      interactive && "cursor-pointer",
                      isExpanded && "bg-[var(--color-surface-inset)]",
                    )}
                  >
                    {columns.map((col) => (
                      <td
                        key={col.key}
                        className={cn(
                          "px-4 py-3 align-middle text-[var(--color-ink)]",
                          col.align === "right" && "text-right",
                          col.align === "center" && "text-center",
                          col.numeric && "metric text-[var(--color-ink)]",
                          col.className,
                        )}
                      >
                        {col.cell(row)}
                      </td>
                    ))}
                  </tr>
                  {isExpanded && renderDetail !== undefined ? (
                    <tr>
                      <td
                        colSpan={columns.length}
                        className="border-b border-[var(--color-border)] bg-[var(--color-surface-inset)] px-6 py-4"
                      >
                        {renderDetail(row)}
                      </td>
                    </tr>
                  ) : null}
                </React.Fragment>
              );
            })
          )}
        </tbody>
      </table>
    </div>
  );
}
