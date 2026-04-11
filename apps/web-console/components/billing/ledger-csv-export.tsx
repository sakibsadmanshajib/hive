"use client";

import type { LedgerEntry } from "@/lib/control-plane/client";

interface LedgerCsvExportProps {
  entries: LedgerEntry[];
}

export function LedgerCsvExport({ entries }: LedgerCsvExportProps) {
  function handleExport() {
    const header = "date,type,credits_delta,idempotency_key\n";
    const rows = entries
      .map((entry) => {
        const date = entry.created_at;
        const type = entry.entry_type;
        const delta = String(entry.credits_delta);
        const key = entry.idempotency_key.replace(/,/g, "");
        return `${date},${type},${delta},${key}`;
      })
      .join("\n");

    const csv = header + rows;
    const blob = new Blob([csv], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "ledger-export.csv";
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <button
      type="button"
      onClick={handleExport}
      style={{
        padding: "0.375rem 0.75rem",
        borderRadius: "0.375rem",
        fontSize: "0.875rem",
        fontWeight: 400,
        background: "#f9fafb",
        color: "#4b5563",
        border: "1px solid #e5e7eb",
        cursor: "pointer",
      }}
    >
      Export CSV
    </button>
  );
}
