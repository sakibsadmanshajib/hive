import Link from "next/link";
import { redirect, unstable_rethrow } from "next/navigation";

import {
  getBalance,
  getBudgetThreshold,
  getInvoices,
  getLedgerEntries,
  ControlPlaneError,
  type BudgetThreshold,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
  tolerate,
} from "@/lib/console/data";
import { BillingOverview } from "@/components/billing/billing-overview";
import { CheckoutLauncher } from "@/components/billing/checkout-launcher";
import { BudgetAlertForm } from "@/components/billing/budget-alert-form";
import { BillingLinks } from "@/components/billing/billing-links";
import { InvoiceList } from "@/components/billing/invoice-list";
import { LedgerTable } from "@/components/billing/ledger-table";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { cn } from "@/lib/cn";

/**
 * Three states, not two (issue #494).
 *
 * getBudgetThreshold answers null for "no threshold set" and throws for
 * everything else, and GET /api/v1/accounts/current/budget is gated on
 * billing.view, which authz.Policy grants to owners only. Collapsing the
 * refusal into the unreadable bucket told every member "We could not reach
 * the budget service" -- a claim about a healthy service, inferred from an
 * authorization answer.
 *
 * The refusal is read from the status rather than from the viewer's role on
 * purpose. The console would have to restate the policy to infer it, and that
 * copy drifts the moment the policy splits read from write, which
 * apps/control-plane/internal/budgets/http.go already says it expects to do.
 *
 * Shape follows loadMembers() in app/console/members/page.tsx.
 */
async function loadBudgetThreshold(): Promise<
  | { kind: "ok"; threshold: BudgetThreshold | null }
  | { kind: "forbidden" }
  | { kind: "unreadable" }
> {
  try {
    return { kind: "ok", threshold: await getBudgetThreshold() };
  } catch (error) {
    unstable_rethrow(error);
    if (error instanceof ControlPlaneError && error.status === 403) {
      return { kind: "forbidden" };
    }
    console.error("BillingPage: could not load the alert threshold", error);
    return { kind: "unreadable" };
  }
}

interface BillingPageProps {
  searchParams: Promise<{
    tab?: string;
    cursor?: string;
    type?: string;
    action?: string;
  }>;
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
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const params = await searchParams;
  const activeTab: TabName = isValidTab(params.tab) ? params.tab : "overview";
  const cursor = params.cursor ?? null;
  const typeFilter = params.type ?? null;
  // "Buy credits" links to /console/billing?action=buy
  // (components/billing/billing-overview.tsx). Nothing read the parameter and
  // CheckoutModal had no import anywhere in the app, so the link navigated back
  // to this same page and no modal ever appeared. Issue #1386.
  const showCheckout = params.action === "buy";

  const [balance, profile, budgetThreshold, recentLedger] = await Promise.all([
    // A balance the console cannot read is unknown, not zero, and this page is
    // where a customer comes to find out what it is. tolerate() keeps the rest
    // of the page (ledger, invoices, buy credits) reachable while the card
    // says plainly that the figure is unavailable (issue #494).
    tolerate(getBalance()),
    requireAccountProfile(),
    // Three-stated rather than tolerated: getBudgetThreshold's own null means
    // "no threshold set", so a bare tolerate() would render an outage as
    // "none set" and invite the customer to overwrite a threshold that is
    // still in force -- and a member's 403 is neither of those.
    loadBudgetThreshold(),
    // Issue #856: the Overview tab hardcoded recentEntries={[]} since PR #89
    // (the original Go rewrite), so "No transactions yet" rendered
    // unconditionally regardless of what the ledger held. getLedgerEntries
    // already reads the correct "entries" wrapper key and is unaffected by
    // the analytics key-mismatch fixed elsewhere in this change; it was
    // simply never called for this tab. A failed fetch here degrades to an
    // empty preview rather than breaking the page, matching budgetThreshold's
    // own fallback above.
    tolerate(getLedgerEntries({ limit: 5 })),
  ]);

  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      viewer={viewer}
      user={{ email: viewer.user.email, name: profile?.owner_name || null }}
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
            recentEntries={recentLedger?.entries ?? null}
            accountCountryCode={profile?.country_code ?? ""}
          />
          {budgetThreshold.kind === "ok" ? (
            <BudgetAlertForm currentThreshold={budgetThreshold.threshold} />
          ) : budgetThreshold.kind === "forbidden" ? (
            <EmptyState
              title="You cannot view the alert threshold"
              description="Only workspace owners can see and change the spend alert threshold on this workspace. Ask an owner if you need it."
            />
          ) : (
            <EmptyState
              title="Could not load your alert threshold"
              description="We could not reach the budget service, so this form is not showing the threshold currently in force. Refresh to try again."
            />
          )}
          <BillingLinks />
        </div>
      ) : null}

      {activeTab === "ledger" ? (
        <LedgerEntries cursor={cursor} typeFilter={typeFilter} />
      ) : null}

      {activeTab === "invoices" ? <InvoicesTab /> : null}

      {showCheckout ? (
        <CheckoutLauncher accountCountryCode={profile?.country_code ?? ""} />
      ) : null}
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
  const ledgerPage = await tolerate(
    getLedgerEntries({
      limit: 25,
      cursor: cursor ?? undefined,
      type: typeFilter ?? undefined,
    }),
  );

  // A ledger that failed to load must not render as a ledger with no entries.
  // "No transactions" is a claim about the account; this is a claim about the
  // request (issue #494).
  if (!ledgerPage) {
    return (
      <EmptyState
        title="Could not load the ledger"
        description="We could not reach the billing service. Refresh to try again."
      />
    );
  }

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
  const invoices = await tolerate(getInvoices());

  // Same rule as the ledger above: an unreachable invoice service is not the
  // same statement as "you have no invoices".
  if (!invoices) {
    return (
      <EmptyState
        title="Could not load invoices"
        description="We could not reach the billing service. Refresh to try again."
      />
    );
  }

  return <InvoiceList invoices={invoices} />;
}
