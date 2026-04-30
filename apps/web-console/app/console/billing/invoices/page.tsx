// Phase 14 FIX-14-27 — workspace invoices page (BDT-only).
//
// Server component. Lists invoices for the current workspace with PDF
// download links. Member-readable (any workspace member can list); the
// backend gates cross-workspace access.

import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getViewer,
  listWorkspaceInvoices,
  type InvoiceRecord,
} from "@/lib/control-plane/client";
import { InvoiceRow } from "@/components/billing/invoice-row";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

export default async function WorkspaceInvoicesPage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const profile = await getAccountProfile();
  const workspaceId = viewer.current_account.id;

  const invoices: InvoiceRecord[] = await listWorkspaceInvoices(
    workspaceId,
  ).catch((): InvoiceRecord[] => []);

  return (
    <ConsoleShell
      workspace={{
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/billing"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Invoices</span>
      }
    >
      <PageHeader
        eyebrow="Workspace"
        title="Invoices"
        description="Monthly invoices for this workspace. All amounts are in Bangladeshi taka."
      />

      <Card>
        <CardHeader>
          <CardTitle>Workspace invoices</CardTitle>
          <CardDescription>
            {invoices.length === 0
              ? "No invoices generated yet. Invoices appear here on the first of each month."
              : `${invoices.length} invoice${invoices.length === 1 ? "" : "s"} on file.`}
          </CardDescription>
        </CardHeader>
        {invoices.length > 0 ? (
          <CardContent className="px-5 py-5">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-[var(--color-border)] text-left text-xs uppercase text-[var(--color-ink-3)]">
                  <th className="px-3 py-2">Period</th>
                  <th className="px-3 py-2">Total</th>
                  <th className="px-3 py-2">Models</th>
                  <th className="px-3 py-2">Download</th>
                </tr>
              </thead>
              <tbody>
                {invoices.map((invoice) => (
                  <InvoiceRow key={invoice.id} invoice={invoice} />
                ))}
              </tbody>
            </table>
          </CardContent>
        ) : null}
      </Card>
    </ConsoleShell>
  );
}
