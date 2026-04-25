import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";

interface ColumnDef {
  key: string;
  label: string;
}

type AnalyticsRow = Record<string, unknown>;

interface AnalyticsTableProps {
  data: ReadonlyArray<AnalyticsRow>;
  columns: ReadonlyArray<ColumnDef>;
}

function isNumber(value: unknown): value is number {
  return typeof value === "number";
}

function formatCell(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (isNumber(value)) return value.toLocaleString();
  return String(value);
}

export function AnalyticsTable({ data, columns }: AnalyticsTableProps) {
  if (data.length === 0) {
    return (
      <EmptyState
        title="No usage data"
        description="Usage data will appear here once your first API request lands."
      />
    );
  }

  // Map ColumnDef[] into typed Column<AnalyticsRow>[]; first column is text,
  // remaining numeric columns get tabular-nums + right-align via numeric flag.
  const tableColumns: Column<AnalyticsRow>[] = columns.map((col, idx) => {
    const numeric = idx > 0;
    return {
      key: col.key,
      header: col.label,
      align: numeric ? "right" : "left",
      numeric,
      cell: (row) => formatCell(row[col.key]),
    };
  });

  // Augment each row with a stable string key for DataTable rowKey.
  type Indexed = AnalyticsRow & { __key: string };
  const rows: Indexed[] = data.map((row, idx) => ({
    ...row,
    __key: typeof row.group_key === "string" ? row.group_key : String(idx),
  }));

  return (
    <DataTable<Indexed>
      rows={rows}
      columns={tableColumns}
      rowKey={(row) => row.__key}
    />
  );
}
