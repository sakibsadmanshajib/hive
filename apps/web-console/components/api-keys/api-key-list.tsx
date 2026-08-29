import type { ApiKey } from "@/lib/control-plane/client";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { formatShortDate } from "@/lib/format/credits";
import { formatUsdFromCredits } from "@/lib/format/model-pricing";
import { RevokeConfirmPanel } from "./revoke-confirm-panel";

// Reset-cadence suffix for the limit cell, matching the New Key modal's own
// "Reset limit every..." wording. budget_summary.kind is "none" | "lifetime"
// | "monthly" (apps/control-plane/internal/apikeys budgetSummary); "none" is
// handled separately as "Unlimited" before this ever runs.
function limitSuffix(budgetKind: string): string {
  return budgetKind === "monthly" ? "/mo" : " total";
}

interface ApiKeyListProps {
  keys: ApiKey[];
  canManage: boolean;
}

type ToneName = "success" | "danger" | "neutral";

function statusTone(status: string): { label: string; tone: ToneName } {
  switch (status) {
    case "active":
      return { label: "Active", tone: "success" };
    case "revoked":
      return { label: "Revoked", tone: "danger" };
    case "expired":
      return { label: "Expired", tone: "neutral" };
    default:
      return { label: status, tone: "neutral" };
  }
}

export function ApiKeyList({ keys, canManage }: ApiKeyListProps) {
  const columns: Column<ApiKey>[] = [
    {
      key: "nickname",
      header: "Name",
      cell: (row) => (
        <span className="font-medium text-[var(--color-ink)]">
          {row.nickname}
        </span>
      ),
    },
    {
      key: "key",
      header: "Key",
      cell: (row) => (
        <code className="font-mono text-xs text-[var(--color-ink-2)]">
          hk_xxxx&bull;&bull;&bull;{row.redacted_suffix}
        </code>
      ),
    },
    {
      key: "status",
      header: "Status",
      cell: (row) => {
        const { label, tone } = statusTone(row.status);
        return <Badge tone={tone}>{label}</Badge>;
      },
    },
    {
      key: "spend_credits",
      // "lifetime" is in the header, not implied. spend_credits is the key's
      // whole-life total (Repository.GetLifetimeSpend), while the cap beside
      // it can be monthly, and two numbers side by side read as a ratio. A
      // key at $500 lifetime against a $10/mo cap is not over its cap, and
      // an unqualified "Spend" column says it is.
      header: "Spend (lifetime)",
      numeric: true,
      align: "right",
      cell: (row) => (
        <span className="tabular-nums">{formatUsdFromCredits(row.spend_credits)}</span>
      ),
    },
    {
      key: "budget_limit_credits",
      header: "Credit limit",
      numeric: true,
      align: "right",
      cell: (row) =>
        row.budget_limit_credits === null ? (
          <span className="text-[var(--color-ink-3)]">Unlimited</span>
        ) : (
          <span className="tabular-nums">
            {formatUsdFromCredits(row.budget_limit_credits)}
            {limitSuffix(row.budget_summary.kind)}
          </span>
        ),
    },
    {
      key: "expires_at",
      header: "Expires",
      numeric: true,
      align: "right",
      cell: (row) => formatShortDate(row.expires_at),
    },
    {
      key: "last_used_at",
      header: "Last used",
      numeric: true,
      align: "right",
      cell: (row) => formatShortDate(row.last_used_at),
    },
  ];

  if (canManage) {
    columns.push({
      key: "actions",
      header: <span className="sr-only">Actions</span>,
      align: "right",
      cell: (row) =>
        row.status === "active" ? (
          <div className="flex items-center justify-end gap-3">
            <RevokeConfirmPanel keyId={row.id} keyNickname={row.nickname} />
          </div>
        ) : (
          <span className="text-xs text-[var(--color-ink-3)]">—</span>
        ),
    });
  }

  return (
    <DataTable<ApiKey>
      rows={keys}
      columns={columns}
      rowKey={(row) => row.id}
    />
  );
}
