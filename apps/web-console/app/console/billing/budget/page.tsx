// Phase 14 FIX-14-25 — workspace budget settings page (BDT-only).
//
// Server component. Reads viewer + workspace budget via the typed client and
// renders the BudgetForm. Owner-only mutation: non-owners see disabled fields
// (defence-in-depth — backend also rejects with 403).
//
// Regulatory rule: BDT-only on the customer surface. Zero USD/FX strings.

import { redirect, unstable_rethrow } from "next/navigation";

import {
  getBudget,
  ControlPlaneError,
  type BudgetSettings,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
} from "@/lib/console/data";
import { BudgetForm } from "@/components/billing/budget-form";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";

/**
 * Three states, not two (issue #494).
 *
 * getBudget answers null for "no budget configured" and throws for everything
 * else, including the 403 a non-owner gets. Collapsing those into one null
 * told a member "We could not reach the budget service" -- a claim about a
 * healthy service, inferred from an authorization answer -- and withheld the
 * read-only form they are supposed to see.
 *
 * Shape follows loadMembers() in app/console/members/page.tsx: a refusal is a
 * real, known answer and never renders as an outage.
 */
async function loadBudget(workspaceId: string): Promise<{
  budget: BudgetSettings | null;
  unreadable: boolean;
}> {
  try {
    return { budget: await getBudget(workspaceId), unreadable: false };
  } catch (error) {
    unstable_rethrow(error);
    // Forbidden is an answer about this viewer, not about the service. The
    // form still renders, disabled by readOnly={!isOwner}, which is what a
    // member saw before and what tests/e2e/console-budgets.spec.ts pins.
    if (error instanceof ControlPlaneError && error.status === 403) {
      return { budget: null, unreadable: false };
    }
    console.error("BudgetSettingsPage: could not load the budget", error);
    return { budget: null, unreadable: true };
  }
}

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

  const { budget, unreadable } = await loadBudget(workspaceId);

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
      {!unreadable ? (
        <BudgetForm
          workspaceId={workspaceId}
          budget={budget}
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
