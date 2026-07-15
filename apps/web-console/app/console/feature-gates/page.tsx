import { redirect } from "next/navigation";
import { ShieldAlert } from "lucide-react";

import {
  getViewer,
  getAccountProfile,
  getFeatureGates,
  type FeatureGates,
} from "@/lib/control-plane/client";
import { can } from "@/lib/viewer-gates";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { FeatureGateManager } from "@/components/feature-gates/feature-gate-manager";

// Admin feature-gate page (issue #292, agent-subsystem blueprint Step 1.2).
// Lists every registered gate for the current workspace and lets a platform
// admin toggle each one; the control-plane enforces platform-admin, so this is
// defence in depth: non-admins get an access state without an upstream call.
//
// ponytail: the nav link is shown to everyone (the shared shell has no viewer
// permissions); gating it per-item is a follow-up. Access itself is enforced
// here and again at the control-plane.
export default async function FeatureGatesPage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const profile = await getAccountProfile().catch(
    (): { owner_name: string } => ({ owner_name: "" }),
  );

  const isAdmin = can(viewer, "platform.admin");

  let gates: FeatureGates | null = null;
  let loadFailed = false;
  if (isAdmin) {
    try {
      gates = await getFeatureGates();
    } catch {
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

      {!isAdmin ? (
        <EmptyState
          icon={<ShieldAlert size={20} />}
          title="Admin access required"
          description="Only platform admins can view and change feature gates. Ask an admin if you need a capability turned on."
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
