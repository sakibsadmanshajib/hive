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
 * How much of a key's budget is gone: the dollar figures for the exact number,
 * and a bar for the proportion at a glance (issue #1683). Before this the
 * column pair was plain text and the reader had to divide two dollar amounts
 * in their head.
 *
 * The numerator is budget_spend_credits, which is the counter edge-api
 * enforces against (api_key_budget_windows consumed plus reserved), and NOT
 * spend_credits. The two are written by different paths: the lifetime rollup
 * takes every settled request, while the budget window is only written once a
 * cap exists and is never backfilled for the spend that came before it. A bar
 * drawn from the lifetime figure paints a key that was capped after it spent
 * as refused, at a hundred and eighty percent and in red, while the gateway
 * goes on serving it. This surface must not claim a state the system does not
 * have, so when the enforced counter is absent it draws no bar at all and
 * states the two figures separately, with no part-of-whole connective between
 * them.
 *
 * Native <progress> rather than a coloured div: the progressbar role, the
 * value and the maximum come from the element itself, so a screen reader
 * reads the proportion without a hand-built ARIA triple that can drift out of
 * step with the rendered fill.
 */
function BudgetUsageCell({ row }: { row: ApiKey }) {
  const lifetimeText = formatUsdFromCredits(row.spend_credits);
  const limit = row.budget_limit_credits;

  if (limit === null) {
    // No cap, so there is no proportion to state. The lifetime total is the
    // only spend figure that exists for such a key: the budget window is
    // never written while the budget kind is "none".
    return (
      <div className="flex items-baseline gap-1.5 whitespace-nowrap">
        <span className="tabular-nums text-[var(--color-ink)]">
          {lifetimeText}
        </span>
        <span className="text-xs text-[var(--color-ink-3)]">of</span>
        <span className="text-[var(--color-ink-3)]">Unlimited</span>
      </div>
    );
  }

  const limitText = `${formatUsdFromCredits(limit)}${limitSuffix(row.budget_summary.kind)}`;
  // Coalesced rather than read straight: a control-plane that predates the
  // field sends no key at all, and undefined would sail past a === null check
  // into the arithmetic below.
  const budgetSpend = row.budget_spend_credits ?? null;

  if (budgetSpend === null) {
    // A cap with no enforced counter to divide: an older control-plane, or a
    // shape this console does not recognise. Both figures, no "of" between
    // them and no bar, because the proportion is exactly what is unknown.
    return (
      <div className="flex items-baseline gap-1.5 whitespace-nowrap">
        <span className="tabular-nums text-[var(--color-ink)]">
          {lifetimeText}
        </span>
        <span className="text-xs text-[var(--color-ink-3)]">lifetime</span>
        <span className="text-[var(--color-ink-3)]">&middot;</span>
        <span className="tabular-nums text-[var(--color-ink-2)]">
          {limitText}
        </span>
        <span className="text-xs text-[var(--color-ink-3)]">cap</span>
      </div>
    );
  }

  // A non-positive cap is exhausted by definition, whatever has been spent
  // against it: enforcement refuses when consumed + reserved + estimated
  // exceeds the limit, and every request carries a positive estimate, so a
  // zero cap rejects the first call. Resolving it to 1 here also keeps the
  // division out of the render path, so no NaN from 0/0 and no Infinity.
  const ratio = limit > 0 ? budgetSpend / limit : 1;
  const reached = ratio >= 1;
  // The fill is clamped to the track at both ends and only the percentage
  // carries the overshoot, so a key at 150% cannot render a bar wider than its
  // column. The lower bound is for a counter the wire says is negative:
  // nothing writes one, and a bar that renders a negative width if something
  // ever does is not worth the risk of finding out.
  const fill = Number((Math.min(Math.max(ratio, 0), 1) * 100).toFixed(1));
  const percentText = formatPercent(ratio);
  const budgetSpendText = formatUsdFromCredits(budgetSpend);

  return (
    <div className="flex min-w-[12rem] max-w-[16rem] flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-3">
        <div className="flex items-baseline gap-1.5 whitespace-nowrap">
          <span className="tabular-nums text-[var(--color-ink)]">
            {budgetSpendText}
          </span>
          <span className="text-xs text-[var(--color-ink-3)]">of</span>
          <span className="tabular-nums text-[var(--color-ink-2)]">
            {limitText}
          </span>
        </div>
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
        aria-label={`Budget used: ${budgetSpendText} of ${limitText}`}
        // The clamped fill would otherwise announce "100%" for a key at 150%.
        // aria-valuetext keeps what a screen reader hears identical to the
        // percentage printed beside the bar. Support for it on a native
        // progress is not uniform, which is why the true percentage is also a
        // text node in this cell rather than only an attribute.
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
      {row.spend_credits === budgetSpend ? null : (
        // The two counters differ whenever the cap resets (a monthly window is
        // this month, the lifetime total is every month) or when the key spent
        // before it was capped. Printing the lifetime figure here keeps the
        // number this column has always carried, without letting it near the
        // ratio above.
        <span className="text-2xs text-[var(--color-ink-3)] whitespace-nowrap">
          {lifetimeText} lifetime
        </span>
      )}
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
      // "Budget used", not "Spend": the figure beside the cap is the enforced
      // window (budget_spend_credits), which for a monthly cap is this month
      // and not the key's whole life. The header said "Spend (lifetime)" while
      // the number under it was the lifetime rollup; naming the new number
      // "spend" would carry that reading onto a different counter.
      header: "Budget used",
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
