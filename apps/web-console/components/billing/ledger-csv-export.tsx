"use client";

import { Download } from "lucide-react";

import type { LedgerEntry } from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";
import { toCsv } from "@/lib/csv";

interface LedgerCsvExportProps {
  entries: LedgerEntry[];
}

export function LedgerCsvExport({ entries }: LedgerCsvExportProps) {
  function handleExport() {
    // Escaping lives in lib/csv so this export and the usage-logs export
    // neutralise a formula-leading cell the same way (issue #1401).
    const csv = toCsv(
      ["date", "type", "credits_delta", "idempotency_key"],
      entries.map((entry) => [
        entry.created_at,
        entry.entry_type,
        // A number, not a string, so the writer leaves it unprefixed and the
        // column still sums in a spreadsheet.
        entry.credits_delta,
        entry.idempotency_key,
      ])
    );
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
