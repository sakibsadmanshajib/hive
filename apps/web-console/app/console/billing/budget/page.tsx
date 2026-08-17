// Phase 14 FIX-14-25 — workspace budget settings page (BDT-only).
//
// Server component. Reads viewer + workspace budget via the typed client and
// renders the BudgetForm. Owner-only mutation: non-owners see disabled fields
// (defence-in-depth — backend also rejects with 403).
//
// Regulatory rule: BDT-only on the customer surface. Zero USD/FX strings.

import { redirect } from "next/navigation";

import {
  EMPTY_ACCOUNT_PROFILE,
  getAccountProfile,
  getBudget,
  getViewer,
  type Viewer,
} from "@/lib/control-plane/client";
import { BudgetForm } from "@/components/billing/budget-form";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";

export default async function BudgetSettingsPage() {
  // getViewer() already retries one transient Supabase Auth hiccup
  // (lib/control-plane/client.ts). A second failure means the session
  // genuinely cannot be resolved, so this redirects to sign-in -- the same
  // destination an expired session already takes (tests/e2e/unauth.spec.ts)
  // -- instead of letting the throw reach the generic error boundary. There
  // is nothing else this page can render without a viewer: no role, no
  // workspace id, no email.
  let viewer: Viewer;
  try {
    viewer = await getViewer();
  } catch (error) {
    console.error("BudgetSettingsPage: could not load viewer", error);
    redirect("/auth/sign-in");
  }
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  // A profile-fetch failure is not a session problem -- the page still knows
  // who the viewer is and which workspace this is, so it degrades to the
  // same empty/not-yet-set-up profile a fresh account already renders
  // (EMPTY_ACCOUNT_PROFILE), rather than crashing on a field this page only
  // needs for a display name fallback.
  const profile = await getAccountProfile().catch((error: unknown) => {
    console.error("BudgetSettingsPage: could not load account profile", error);
    return EMPTY_ACCOUNT_PROFILE;
  });
  const workspaceId = viewer.current_account.id;
  const isOwner = viewer.current_account.role === "owner";

  const budget = await getBudget(workspaceId).catch((): null => null);

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
