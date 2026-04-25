"use client";

import { Download } from "lucide-react";

import type { LedgerEntry } from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";

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
    <Button
      type="button"
      variant="secondary"
      size="sm"
      onClick={handleExport}
      disabled={entries.length === 0}
    >
      <Download size={14} aria-hidden="true" />
      Export CSV
    </Button>
  );
}
