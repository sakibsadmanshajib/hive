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
}

export function DataTable<T>({
  rows,
  columns,
  rowKey,
  empty,
  className,
}: DataTableProps<T>) {
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
            rows.map((row) => (
              <tr
                key={rowKey(row)}
                className={cn(
                  "border-b border-[var(--color-border)] last:border-b-0",
                  "transition-colors duration-[var(--duration-fast)]",
                  "hover:bg-[var(--color-surface-inset)]",
                )}
              >
                {columns.map((col) => (
                  <td
                    key={col.key}
                    className={cn(
                      "px-4 py-3 align-middle text-[var(--color-ink)]",
                      col.align === "right" && "text-right",
                      col.align === "center" && "text-center",
                      col.numeric && "tabular-nums text-[var(--color-ink)]",
                      col.className,
                    )}
                  >
                    {col.cell(row)}
                  </td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}
