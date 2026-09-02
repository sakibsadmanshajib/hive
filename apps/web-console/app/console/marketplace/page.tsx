import { notFound, redirect, unstable_rethrow } from "next/navigation";

import { isWorkspaceAdminViewer } from "@/lib/viewer-gates";
import { ShieldAlert } from "lucide-react";

import {
  getMarketplaceEntries,
  ControlPlaneError,
  type MarketplaceEntries,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
} from "@/lib/console/data";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { MarketplaceManager } from "@/components/marketplace/marketplace-manager";

// Marketplace page (issue #309, agent-subsystem blueprint Step 2.3, re-gated by
// issue #758). Lists the curated MCP and skills catalog for the current
// workspace and lets the workspace administrator choose which entries this
// workspace uses. Curating the catalog itself stays a platform operation
// (can_curate), and the control-plane is the authority on the data. The page
// mirrors its WorkspaceAdminGate locally so the URL itself refuses
// non-administrators server-side (hidden nav is not access control,
// #947/#948/#949 family): OWNER of the selected workspace or platform admin
// may render, anyone else gets a 404 that does not confirm the surface exists.
export default async function MarketplacePage() {
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  // Server-side role gate: refuse the page shell before any data fetch.
  if (!isWorkspaceAdminViewer(viewer)) {
    notFound();
  }

  const profile = await requireAccountProfile();

  let entries: MarketplaceEntries | null = null;
  let loadFailed = false;
  let notPermitted = false;
  try {
    entries = await getMarketplaceEntries();
  } catch (err) {
    // Framework control flow first: a DynamicServerError or a redirect is
    // not a permission problem and must not be classified as one
    // (issue #494).
    unstable_rethrow(err);
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
      active="/console/marketplace"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Marketplace</span>
      }
    >
      <PageHeader
        eyebrow="Admin"
        title="MCP and skills marketplace"
        description="Choose which MCP servers, rules, skills, and prompt templates this workspace agents can use."
      />

      {notPermitted ? (
        <EmptyState
          icon={<ShieldAlert size={20} />}
          title="Managed by your administrator"
          description="Ask your workspace owner or administrator if you need a connector enabled."
        />
      ) : loadFailed || !entries ? (
        <EmptyState
          title="Could not load the marketplace catalog"
          description="Something went wrong loading the catalog. Refresh to try again."
        />
      ) : (
        <MarketplaceManager entries={entries.entries} canCurate={entries.canCurate} />
      )}
    </ConsoleShell>
  );
}
