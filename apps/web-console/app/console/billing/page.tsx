import {
  getBalance,
  getLedgerEntries,
  getInvoices,
  getAccountProfile,
  getBudgetThreshold,
} from "@/lib/control-plane/client";
import { BillingOverview } from "@/components/billing/billing-overview";
import { LedgerTable } from "@/components/billing/ledger-table";
import { InvoiceList } from "@/components/billing/invoice-list";
import { BudgetAlertForm } from "@/components/billing/budget-alert-form";

interface BillingPageProps {
  searchParams: Promise<{ tab?: string; cursor?: string; type?: string }>;
}

type TabName = "overview" | "ledger" | "invoices";

function isValidTab(tab: string | undefined): tab is TabName {
  return tab === "overview" || tab === "ledger" || tab === "invoices";
}

export default async function BillingPage({ searchParams }: BillingPageProps) {
  const params = await searchParams;
  const activeTab: TabName = isValidTab(params.tab) ? params.tab : "overview";
  const cursor = params.cursor ?? null;
  const typeFilter = params.type ?? null;

  const [balance, profile, budgetThreshold] = await Promise.all([
    getBalance(),
    getAccountProfile(),
    getBudgetThreshold().catch(() => null),
  ]);

  const tabs: { id: TabName; label: string }[] = [
    { id: "overview", label: "Overview" },
    { id: "ledger", label: "Ledger" },
    { id: "invoices", label: "Invoices" },
  ];

  return (
    <div style={{ display: "grid", gap: "1.5rem", maxWidth: "72rem" }}>
      <h1 style={{ margin: 0, fontSize: "1.5rem", fontWeight: 700 }}>Billing</h1>

      {/* Tab bar */}
      <div
        style={{
          display: "flex",
          borderBottom: "1px solid #e5e7eb",
          gap: "0",
          marginBottom: "0",
        }}
      >
        {tabs.map((tab) => {
          const isActive = activeTab === tab.id;
          return (
            <a
              key={tab.id}
              href={`/console/billing?tab=${tab.id}`}
              style={{
                padding: "0.5rem 1rem",
                color: isActive ? "#1d4ed8" : "#6b7280",
                borderBottom: isActive ? "2px solid #1d4ed8" : "2px solid transparent",
                fontWeight: isActive ? 700 : 400,
                textDecoration: "none",
                fontSize: "1rem",
              }}
            >
              {tab.label}
            </a>
          );
        })}
      </div>

      {/* Tab content */}
      {activeTab === "overview" && (
        <>
          <BillingOverview
            balance={balance}
            recentEntries={[]}
            accountCountryCode={profile.country_code}
          />
          <BudgetAlertForm currentThreshold={budgetThreshold} />
        </>
      )}

      {activeTab === "ledger" && (
        <LedgerEntries cursor={cursor} typeFilter={typeFilter} />
      )}

      {activeTab === "invoices" && <InvoicesTab />}
    </div>
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
