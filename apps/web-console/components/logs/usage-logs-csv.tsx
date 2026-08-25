"use client";

import { Download } from "lucide-react";

import type { UsageEventRow } from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";

interface UsageLogsCsvProps {
  rows: UsageEventRow[];
  keyNames: Record<string, string>;
}

export function UsageLogsCsv({ rows, keyNames }: UsageLogsCsvProps) {
  function handleExport() {
    // cache_read_tokens and cache_write_tokens sit next to the token columns
    // they belong with. An absent value exports as an EMPTY cell rather than
    // 0, for the same reason the table renders an em-dash: the control-plane
    // omits both fields when they are zero, so the console cannot tell a
    // measured zero from an unmeasured one, and a spreadsheet full of zeroes
    // is exactly the shape a customer would reconcile a bill against.
    const header =
      "created_at,request_id,model_alias,status,input_tokens,output_tokens," +
      "cache_read_tokens,cache_write_tokens,hive_credit_delta,error_code,api_key\n";
    const lines = rows.map((row) => {
      const key = row.api_key_id
        ? (keyNames[row.api_key_id] ?? `…${row.api_key_id.slice(-6)}`)
        : "—";
      const cell = (value: string) => value.replace(/[",\n]/g, " ");
      return [
        row.created_at,
        row.request_id,
        row.model_alias,
        row.status,
        String(row.input_tokens),
        String(row.output_tokens),
        row.cache_read_tokens === undefined
          ? ""
          : String(row.cache_read_tokens),
        row.cache_write_tokens === undefined
          ? ""
          : String(row.cache_write_tokens),
        String(row.hive_credit_delta),
        row.error_code ?? "",
        key,
      ]
        .map(cell)
        .join(",");
    });

    const csv = header + lines.join("\n");
    const blob = new Blob([csv], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "usage-events.csv"
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <Button
      type="button"
      variant="secondary"
      size="sm"
      onClick={handleExport}
      disabled={rows.length === 0}
    >
      <Download size={14} aria-hidden="true" />
      Export CSV
    </Button>
  );
}
