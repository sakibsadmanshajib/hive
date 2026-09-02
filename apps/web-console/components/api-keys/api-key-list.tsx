import Link from "next/link";

import type { ApiKey } from "@/lib/control-plane/client";
import { Badge } from "@/components/ui/badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { formatPercent, formatShortDate } from "@/lib/format/credits";
import { formatUsdFromCredits } from "@/lib/format/model-pricing";
import { cn } from "@/lib/cn";
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

/**
 * Spend against a key's cap: both dollar figures for the exact number, and a
 * bar for the proportion at a glance (issue #1683). Before this the column
 * pair was plain text and the reader had to divide two dollar amounts in
 * their head to learn how much of the budget was gone.
 *
 * The bar renders only when the cap's window matches the spend figure's
 * window. spend_credits is lifetime (RecordUsageFinalization writes the
 * lifetime rollup on every settled request, Repository.GetLifetimeSpend sums
 * it), so it divides honestly by a lifetime cap and not by a monthly one: a
 * key at $500 lifetime against a $10/mo cap is nowhere near its monthly cap,
 * and a bar reading 5000% would raise an alarm the enforcement path never
 * raises. A monthly cap therefore keeps both figures and claims no ratio,
 * until the list endpoint reports a month-to-date spend worth dividing.
 *
 * Native <progress> rather than a coloured div: the progressbar role, the
 * value and the maximum come from the element itself, so a screen reader
 * reads the proportion without a hand-built ARIA triple that can drift out of
 * step with the rendered fill.
 */
function BudgetUsageCell({ row }: { row: ApiKey }) {
  const spendText = formatUsdFromCredits(row.spend_credits);
  const limit = row.budget_limit_credits;

  if (limit === null) {
    return (
      <div className="flex items-baseline gap-1.5 whitespace-nowrap">
        <span className="tabular-nums text-[var(--color-ink)]">{spendText}</span>
        <span className="text-xs text-[var(--color-ink-3)]">of</span>
        <span className="text-[var(--color-ink-3)]">Unlimited</span>
      </div>
    );
  }

  const limitText = `${formatUsdFromCredits(limit)}${limitSuffix(row.budget_summary.kind)}`;
  const figures = (
    <div className="flex items-baseline gap-1.5 whitespace-nowrap">
      <span className="tabular-nums text-[var(--color-ink)]">{spendText}</span>
      <span className="text-xs text-[var(--color-ink-3)]">of</span>
      <span className="tabular-nums text-[var(--color-ink-2)]">{limitText}</span>
    </div>
  );

  if (row.budget_summary.kind !== "lifetime") {
    return figures;
  }

  // A non-positive cap refuses every request, so any spend at all against one
  // is a budget that is gone. Resolving it here to a plain 0 or 1 keeps the
  // division out of the render path entirely: no NaN from 0/0, no Infinity
  // from a spend over a zero cap, and nothing downstream has to defend
  // against either.
  const ratio =
    limit > 0 ? row.spend_credits / limit : row.spend_credits > 0 ? 1 : 0;
  const reached = ratio >= 1;
  // The fill is clamped to the track and only the percentage carries the
  // overshoot, so a key at 150% cannot render a bar wider than its column.
  const fill = Number((Math.min(ratio, 1) * 100).toFixed(1));
  const percentText = formatPercent(ratio);

  return (
    <div className="flex min-w-[12rem] max-w-[16rem] flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-3">
        {figures}
        <span className="flex items-center gap-1.5">
          {reached ? (
            <Badge tone="danger" className="whitespace-nowrap">
              Limit reached
            </Badge>
          ) : null}
          <span
            className={cn(
              "text-xs tabular-nums",
              reached
                ? "text-[var(--color-danger)]"
                : "text-[var(--color-ink-2)]",
            )}
          >
            {percentText}
          </span>
        </span>
      </div>
      <progress
        value={fill}
        max={100}
        aria-label={`Budget used: ${spendText} of ${limitText}`}
        // The clamped fill would otherwise announce "100%" for a key at 150%.
        // aria-valuetext keeps what a screen reader hears identical to the
        // percentage printed beside the bar.
        aria-valuetext={reached ? `${percentText}, limit reached` : percentText}
        className={cn(
          "h-1.5 w-full appearance-none overflow-hidden rounded-full border-0",
          "bg-[var(--color-surface-inset)]",
          "[&::-webkit-progress-bar]:rounded-full",
          "[&::-webkit-progress-bar]:bg-[var(--color-surface-inset)]",
          "[&::-webkit-progress-value]:rounded-full",
          "[&::-moz-progress-bar]:rounded-full",
          reached
            ? "[&::-webkit-progress-value]:bg-[var(--color-danger)] [&::-moz-progress-bar]:bg-[var(--color-danger)]"
            : "[&::-webkit-progress-value]:bg-[var(--color-accent)] [&::-moz-progress-bar]:bg-[var(--color-accent)]",
        )}
      />
    </div>
  );
}

export function ApiKeyList({ keys, canManage }: ApiKeyListProps) {
  const columns: Column<ApiKey>[] = [
    {
      key: "nickname",
      header: "Name",
      // The nickname is free text a workspace member chose, and a key minted
      // before the cap landed can still be arbitrarily long. Without a bound
      // here one such row widened the table to fifty thousand pixels and put
      // the Revoke control of every key in the workspace out of reach, with
      // nothing in the product able to shorten the stored value (issue
      // #1400). The title attribute keeps the full name readable on hover.
      cell: (row) => (
        <span
          className="block max-w-[16rem] truncate font-medium text-[var(--color-ink)]"
          title={row.nickname}
        >
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
      key: "budget_usage",
      // "lifetime" stays in the header, not implied. spend_credits is the
      // key's whole-life total (Repository.GetLifetimeSpend), while the cap
      // beside it can be monthly, and two numbers side by side read as a
      // ratio. A key at $500 lifetime against a $10/mo cap is not over its
      // cap, and an unqualified "Spend" column says it is.
      header: "Spend (lifetime) vs limit",
      cell: (row) => <BudgetUsageCell row={row} />,
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
