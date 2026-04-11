interface ColumnDef {
  key: string;
  label: string;
}

interface AnalyticsTableProps {
  data: Array<Record<string, unknown>>;
  columns: ColumnDef[];
}

export function AnalyticsTable({ data, columns }: AnalyticsTableProps) {
  if (data.length === 0) {
    return (
      <div
        style={{
          textAlign: "center",
          padding: "2rem",
          color: "#6b7280",
          backgroundColor: "#f9fafb",
          borderRadius: "0.75rem",
          border: "1px solid #e5e7eb",
        }}
      >
        <p style={{ margin: 0, fontWeight: 500 }}>No usage data</p>
        <p style={{ margin: "0.5rem 0 0", fontSize: "0.875rem" }}>
          Usage data will appear here after your first API request.
        </p>
      </div>
    );
  }

  return (
    <div
      style={{
        overflowX: "auto",
        border: "1px solid #e5e7eb",
        borderRadius: "0.75rem",
      }}
    >
      <table style={{ width: "100%", borderCollapse: "collapse", fontSize: "0.875rem" }}>
        <thead>
          <tr style={{ backgroundColor: "#f9fafb" }}>
            {columns.map((col) => (
              <th
                key={col.key}
                style={{
                  padding: "0.75rem 1rem",
                  textAlign: "left",
                  fontWeight: 600,
                  color: "#374151",
                  borderBottom: "1px solid #e5e7eb",
                  whiteSpace: "nowrap",
                }}
              >
                {col.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {data.map((row, rowIndex) => (
            <tr
              key={rowIndex}
              style={{ borderBottom: rowIndex < data.length - 1 ? "1px solid #f3f4f6" : "none" }}
            >
              {columns.map((col) => {
                const cellValue = row[col.key];
                const displayValue =
                  cellValue === null || cellValue === undefined
                    ? "—"
                    : typeof cellValue === "number"
                    ? cellValue.toLocaleString()
                    : String(cellValue);
                return (
                  <td
                    key={col.key}
                    style={{
                      padding: "0.75rem 1rem",
                      color: "#111827",
                    }}
                  >
                    {displayValue}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
