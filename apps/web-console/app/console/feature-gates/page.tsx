import { notFound, redirect } from "next/navigation";

import { isWorkspaceAdminViewer } from "@/lib/viewer-gates";
import { ShieldAlert } from "lucide-react";

import {
  getFeatureGates,
  ControlPlaneError,
  type FeatureGates,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
} from "@/lib/console/data";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { FeatureGateManager } from "@/components/feature-gates/feature-gate-manager";

// Feature-gate page (issue #292, agent-subsystem blueprint Step 1.2, re-gated
// by issue #758). Lists every registered gate for the current workspace and
// lets the workspace administrator toggle the ones that belong to the
// workspace. The control-plane is the authority on the data; the page mirrors
// its WorkspaceAdminGate locally so the URL itself refuses non-administrators
// server-side (hidden nav is not access control, #947/#948/#949 family):
// OWNER of the selected workspace or platform admin may render, anyone else
// gets a 404 that does not confirm the surface exists.
export default async function FeatureGatesPage() {
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  // Server-side role gate: refuse the page shell before any data fetch.
  if (!isWorkspaceAdminViewer(viewer)) {
    notFound();
  }

  const profile = await requireAccountProfile();

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
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      viewer={viewer}
      user={{ email: viewer.user.email, name: profile?.owner_name || null }}
      active="/console/feature-gates"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Feature gates
        </span>
      }
    >
      {/*
        The old description promised that every change "takes effect across the
        API and apps within about a minute". That is true of three of the
        nineteen registered gates and false of the rest, which persist
        correctly and are read by nothing (issue #762). A page cannot state a
        guarantee the system does not keep, so the promise moved onto the rows
        that earn it: each unenforced row says so itself, and this description
        says only what is true of all of them.
      */}
      <PageHeader
        eyebrow="Admin"
        title="Feature gates"
        description="Capability settings for this workspace. Each row says whether it is enforced at runtime today; an enforced change reaches the API and apps within about a minute."
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
