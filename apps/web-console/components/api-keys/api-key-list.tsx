import Link from "next/link";

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
    {
      // Issue #543: /console/api-keys/[id]/limits had zero inbound links, so
      // per-key request and token limits, a control a regulated buyer asks to
      // see, could only be reached by typing a URL.
      //
      // Deliberately its own column rather than an entry in the actions column
      // below: that column only renders when canManage, while the limits page
      // renders read-only for a member without api_keys.write (it passes
      // canEdit through to the form). Hiding the link behind canManage would
      // hide a page they may read.
      //
      // Revoked and expired keys get no link. Their limits no longer decide
      // anything, and a link to a page whose only control is inert is the same
      // dead end this change exists to remove.
      key: "limits",
      header: <span className="sr-only">Rate limits</span>,
      align: "right",
      cell: (row) =>
        row.status === "active" ? (
          <Link
            href={`/console/api-keys/${row.id}/limits`}
            // Every row's visible text is the same word, so the link is only
            // disambiguated by its cell in table-navigation mode. A screen
            // reader's links list is flat, and would otherwise read as
            // "Limits, Limits, Limits" with no way to tell the keys apart.
            aria-label={`Rate limits for ${row.nickname}`}
            className="text-xs text-[var(--color-ink-2)] underline underline-offset-2 hover:text-[var(--color-ink)]"
          >
            Limits
          </Link>
        ) : (
          <span className="text-xs text-[var(--color-ink-3)]">—</span>
        ),
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
