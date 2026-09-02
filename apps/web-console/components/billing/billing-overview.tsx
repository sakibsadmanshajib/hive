import Link from "next/link";

import type { BalanceSummary, LedgerEntry } from "@/lib/control-plane/client";
import { buttonVariants } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { formatCredits, formatShortDate } from "@/lib/format/credits";
import { CreditBalance } from "@/components/billing/credit-balance";

interface BillingOverviewProps {
  // null when the balance could not be read. Rendering an unknown balance as
  // a figure is how a customer ends up trusting a number the system never
  // had; the card says "Unavailable" instead (issue #494).
  balance: BalanceSummary | null;
  // null when the ledger could not be read. [] is an account with no
  // transactions; the card says which of the two it is (issue #494).
  recentEntries: LedgerEntry[] | null;
  // Country code is intentionally accepted but not displayed — locale rendering
  // is done downstream by checkout-modal which uses Intl with the rail's
  // currency. Surfacing FX hints to BD accounts is a regulatory violation.
  accountCountryCode: string;
}

function entryTypeLabel(entryType: string): string {
  switch (entryType) {
    case "grant":
      return "Purchase";
    case "usage_charge":
      return "Usage charge";
    case "refund":
      return "Refund";
    case "adjustment":
      return "Adjustment";
    case "reservation_hold":
      return "Reserved";
    case "reservation_release":
      return "Released";
    default:
      return entryType;
  }
}

export function BillingOverview({
  balance,
  recentEntries,
}: BillingOverviewProps) {
  const recent = (recentEntries ?? []).slice(0, 5);

  const ledgerColumns: Column<LedgerEntry>[] = [
    {
      key: "type",
      header: "Type",
      cell: (row) => entryTypeLabel(row.entry_type),
    },
    {
      key: "credits",
      header: "Credits",
      numeric: true,
      align: "right",
      cell: (row) => (
        <span
          className={
            row.credits_delta >= 0
              ? "text-[var(--color-success)]"
              : "text-[var(--color-danger)]"
          }
        >
          {row.credits_delta >= 0 ? "+" : ""}
          {formatCredits(row.credits_delta)}
        </span>
      ),
    },
    {
      key: "date",
      header: "Date",
      numeric: true,
      align: "right",
      cell: (row) => formatShortDate(row.created_at),
    },
  ];

  return (
    <div className="grid gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Available balance</CardTitle>
          <CardDescription>
            Top up to keep production traffic flowing without interruption.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-5 px-5 py-5 sm:flex-row sm:items-end sm:justify-between">
          {balance ? (
            <CreditBalance balance={balance} />
          ) : (
            <div className="flex flex-col gap-1">
              <p className="text-3xl text-[var(--color-ink-3)]">Unavailable</p>
              <p className="text-xs text-[var(--color-ink-3)]">
                We could not read your balance just now. Refresh to try again.
              </p>
            </div>
          )}
          <div className="flex items-center gap-3">
            <Link
              href="/console/billing?action=buy"
              className={buttonVariants({ variant: "accent", size: "md" })}
            >
              Buy credits
            </Link>
            <Link
              href="/console/settings/billing"
              className="text-xs text-[var(--color-ink-3)] underline-offset-4 hover:text-[var(--color-ink)] hover:underline"
            >
              Tax profile
            </Link>
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <div className="flex flex-col gap-1">
            <CardTitle>Recent transactions</CardTitle>
            <CardDescription>The last five ledger events.</CardDescription>
          </div>
          {recent.length > 0 ? (
            <Link
              href="/console/billing?tab=ledger"
              className="text-xs text-[var(--color-accent)] underline-offset-4 hover:underline"
            >
              View ledger
            </Link>
          ) : null}
        </CardHeader>
        <CardContent className="px-5 py-5">
          {recentEntries === null ? (
            <EmptyState
              title="Could not load recent transactions"
              description="We could not reach the billing service, so this is not showing your latest ledger activity. Refresh to try again."
            />
          ) : recent.length === 0 ? (
            <EmptyState
              title="No transactions yet"
              description="Your credit ledger fills up after the first top-up."
              action={
                <Link
                  href="/console/billing?action=buy"
                  className={buttonVariants({ variant: "accent", size: "sm" })}
                >
                  Buy credits
                </Link>
              }
            />
          ) : (
            <DataTable<LedgerEntry>
              rows={recent}
              columns={ledgerColumns}
              rowKey={(row) => row.id}
              className="border-0 shadow-none"
            />
          )}
        </CardContent>
      </Card>
    </div>
  );
}

