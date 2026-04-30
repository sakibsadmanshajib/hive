// Phase 14 FIX-14-25 — workspace budget settings page (BDT-only).
//
// Server component. Reads viewer + workspace budget via the typed client and
// renders the BudgetForm. Owner-only mutation: non-owners see disabled fields
// (defence-in-depth — backend also rejects with 403).
//
// Regulatory rule: BDT-only on the customer surface. Zero USD/FX strings.

import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getBudget,
  getViewer,
} from "@/lib/control-plane/client";
import { BudgetForm } from "@/components/billing/budget-form";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";

export default async function BudgetSettingsPage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const profile = await getAccountProfile();
  const workspaceId = viewer.current_account.id;
  const isOwner = viewer.current_account.role === "owner";

  const budget = await getBudget(workspaceId).catch((): null => null);

  return (
    <ConsoleShell
      workspace={{
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/billing"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Budget</span>
      }
    >
      <PageHeader
        eyebrow="Workspace"
        title="Budget settings"
        description="Set soft and hard caps for monthly spend in Bangladeshi taka."
      />
      <BudgetForm
        workspaceId={workspaceId}
        budget={budget}
        readOnly={!isOwner}
      />
    </ConsoleShell>
  );
}
