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
  className,
  expandedKey = null,
  renderDetail,
  onRowToggle,
}: DataTableProps<T>) {
  const interactive = renderDetail !== undefined && onRowToggle !== undefined;
  return (
    <div
      className={cn(
        "overflow-hidden rounded-lg border border-[var(--color-border)]",
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
        <tbody>
          {rows.length === 0 ? (
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
