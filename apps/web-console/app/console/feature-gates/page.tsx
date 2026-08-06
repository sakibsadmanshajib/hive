import { redirect } from "next/navigation";
import { ShieldAlert } from "lucide-react";

import {
  getViewer,
  getAccountProfile,
  getFeatureGates,
  ControlPlaneError,
  type FeatureGates,
} from "@/lib/control-plane/client";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { FeatureGateManager } from "@/components/feature-gates/feature-gate-manager";

// Feature-gate page (issue #292, agent-subsystem blueprint Step 1.2, re-gated
// by issue #758). Lists every registered gate for the current workspace and
// lets the workspace administrator toggle the ones that belong to the
// workspace. The control-plane is the authority: it admits the OWNER of the
// tenant in scope as well as a platform admin, so this page asks it rather than
// second-guessing with a local permission check that could disagree.
//
// A caller who is neither gets 403 here, which renders one line pointing at
// their administrator instead of a wall.
export default async function FeatureGatesPage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const profile = await getAccountProfile().catch(
    (): { owner_name: string } => ({ owner_name: "" }),
  );

  let gates: FeatureGates | null = null;
  let loadFailed = false;
  let notPermitted = false;
  try {
    gates = await getFeatureGates();
  } catch (err) {
    if (err instanceof ControlPlaneError && err.status === 403) {
      notPermitted = true;
    } else {
      loadFailed = true;
    }
  }

  return (
    <ConsoleShell
      workspace={{
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/feature-gates"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Feature gates
        </span>
      }
    >
      <PageHeader
        eyebrow="Admin"
        title="Feature gates"
        description="Turn capabilities on or off for this workspace. Changes take effect across the API and apps within about a minute."
      />

      {notPermitted ? (
        <EmptyState
          icon={<ShieldAlert size={20} />}
          title="Managed by your administrator"
          description="Ask your workspace owner or administrator if you need a capability turned on."
        />
      ) : loadFailed || !gates ? (
        <EmptyState
          title="Could not load feature gates"
          description="Something went wrong loading the gate list. Refresh to try again."
        />
      ) : gates.gates.length === 0 ? (
        <EmptyState
          title="No feature gates"
          description="No gate keys are registered yet."
        />
      ) : (
        <FeatureGateManager gates={gates.gates} />
      )}
    </ConsoleShell>
  );
}
