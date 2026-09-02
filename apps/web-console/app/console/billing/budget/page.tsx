// Phase 14 FIX-14-25 — workspace budget settings page (BDT-only).
//
// Server component. Reads viewer + workspace budget via the typed client and
// renders the BudgetForm. Owner-only mutation: non-owners see disabled fields
// (defence-in-depth — backend also rejects with 403).
//
// Regulatory rule: BDT-only on the customer surface. Zero USD/FX strings.

import { redirect } from "next/navigation";

import {
  getBudget,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
  tolerate,
} from "@/lib/console/data";
import { BudgetForm } from "@/components/billing/budget-form";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";

export default async function BudgetSettingsPage() {
  // The redirect-to-sign-in fallback this page used to spell out itself now
  // lives in requireViewer(), so every console page has it and none of them
  // carry a private copy that also has to remember to let Next.js's own
  // control-flow errors past (issue #494).
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  // A profile-fetch failure is not a session problem: the page still knows
  // who the viewer is and which workspace this is, and it only needs the
  // profile for a display-name fallback. requireAccountProfile() resolves it
  // to null and logs, so this page no longer carries its own copy of that
  // decision (issue #494).
  const profile = await requireAccountProfile();
  const workspaceId = viewer.current_account.id;
  const isOwner = viewer.current_account.role === "owner";

  // getBudget answers null for "no budget configured", so tolerate() alone
  // could not tell that apart from "we could not read it" -- and BudgetForm
  // renders empty caps for both, which invites an owner to save blanks over a
  // soft cap that is still in force. Wrapping the answer keeps the two
  // distinct: null here means unreadable, and the form is withheld
  // (issue #494).
  const budgetRead = await tolerate(
    getBudget(workspaceId).then((budget) => ({ budget })),
  );

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
        <span className="font-medium text-[var(--color-ink-2)]">Budget</span>
      }
    >
      <PageHeader
        eyebrow="Workspace"
        title="Budget settings"
        description="Set soft and hard caps for monthly spend in Bangladeshi taka."
      />
      {budgetRead ? (
        <BudgetForm
          workspaceId={workspaceId}
          budget={budgetRead.budget}
          readOnly={!isOwner}
        />
      ) : (
        <EmptyState
          title="Could not load your budget"
          description="We could not reach the budget service, so this form is not showing the caps currently in force. Refresh to try again."
        />
      )}
    </ConsoleShell>
  );
}
