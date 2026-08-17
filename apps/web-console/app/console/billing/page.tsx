import Link from "next/link";
import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getBalance,
  getBudgetThreshold,
  getInvoices,
  getLedgerEntries,
  getViewer,
  type LedgerPage,
} from "@/lib/control-plane/client";
import { BillingOverview } from "@/components/billing/billing-overview";
import { BudgetAlertForm } from "@/components/billing/budget-alert-form";
import { InvoiceList } from "@/components/billing/invoice-list";
import { LedgerTable } from "@/components/billing/ledger-table";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { cn } from "@/lib/cn";

interface BillingPageProps {
  searchParams: Promise<{ tab?: string; cursor?: string; type?: string }>;
}

type TabName = "overview" | "ledger" | "invoices";

function isValidTab(tab: string | undefined): tab is TabName {
  return tab === "overview" || tab === "ledger" || tab === "invoices";
}

const TABS: ReadonlyArray<{ id: TabName; label: string }> = [
  { id: "overview", label: "Overview" },
  { id: "ledger", label: "Ledger" },
  { id: "invoices", label: "Invoices" },
];

export default async function BillingPage({ searchParams }: BillingPageProps) {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const params = await searchParams;
  const activeTab: TabName = isValidTab(params.tab) ? params.tab : "overview";
  const cursor = params.cursor ?? null;
  const typeFilter = params.type ?? null;

  const [balance, profile, budgetThreshold, recentLedger] = await Promise.all([
    getBalance(),
    getAccountProfile(),
    getBudgetThreshold().catch((): null => null),
    // Issue #856: the Overview tab hardcoded recentEntries={[]} since PR #89
    // (the original Go rewrite), so "No transactions yet" rendered
    // unconditionally regardless of what the ledger held. getLedgerEntries
    // already reads the correct "entries" wrapper key and is unaffected by
    // the analytics key-mismatch fixed elsewhere in this change; it was
    // simply never called for this tab. A failed fetch here degrades to an
    // empty preview rather than breaking the page, matching budgetThreshold's
    // own fallback above.
    getLedgerEntries({ limit: 5 }).catch(
      (): LedgerPage => ({ entries: [], next_cursor: null }),
    ),
  ]);

  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/billing"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Billing</span>
      }
    >
      <PageHeader
        eyebrow="Workspace"
        title="Billing"
        description="Top up credits, browse the ledger, and download invoices for past purchases."
      />

      <nav
        aria-label="Billing sections"
        className="mb-6 flex items-center gap-1 border-b border-[var(--color-border)]"
      >
        {TABS.map((tab) => {
          const isActive = activeTab === tab.id;
          return (
            <Link
              key={tab.id}
              href={`/console/billing?tab=${tab.id}`}
              className={cn(
                "relative -mb-px inline-flex h-9 items-center px-3 text-sm transition-colors",
                isActive
                  ? "border-b-2 border-[var(--color-ink)] text-[var(--color-ink)]"
                  : "border-b-2 border-transparent text-[var(--color-ink-3)] hover:text-[var(--color-ink)]",
              )}
            >
              {tab.label}
            </Link>
          );
        })}
      </nav>

      {activeTab === "overview" ? (
        <div className="flex flex-col gap-6">
          <BillingOverview
            balance={balance}
            recentEntries={recentLedger.entries}
            accountCountryCode={profile.country_code}
          />
          <BudgetAlertForm currentThreshold={budgetThreshold} />
        </div>
      ) : null}

      {activeTab === "ledger" ? (
        <LedgerEntries cursor={cursor} typeFilter={typeFilter} />
      ) : null}

      {activeTab === "invoices" ? <InvoicesTab /> : null}
    </ConsoleShell>
  );
}

async function LedgerEntries({
  cursor,
  typeFilter,
}: {
  cursor: string | null;
  typeFilter: string | null;
}) {
  const ledgerPage = await getLedgerEntries({
    limit: 25,
    cursor: cursor ?? undefined,
    type: typeFilter ?? undefined,
  });

  return (
    <LedgerTable
      entries={ledgerPage.entries}
      nextCursor={ledgerPage.next_cursor}
      currentType={typeFilter}
      currentCursor={cursor}
    />
  );
}

async function InvoicesTab() {
  const invoices = await getInvoices();
  return <InvoiceList invoices={invoices} />;
}
