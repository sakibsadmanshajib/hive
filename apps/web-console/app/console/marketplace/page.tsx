import { redirect } from "next/navigation";
import { ShieldAlert } from "lucide-react";

import {
  getViewer,
  getAccountProfile,
  getMarketplaceEntries,
  ControlPlaneError,
  type MarketplaceEntries,
} from "@/lib/control-plane/client";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { MarketplaceManager } from "@/components/marketplace/marketplace-manager";

// Marketplace page (issue #309, agent-subsystem blueprint Step 2.3, re-gated by
// issue #758). Lists the curated MCP and skills catalog for the current
// workspace and lets the workspace administrator choose which entries this
// workspace uses. Curating the catalog itself stays a platform operation, and
// the control-plane says which of the two this caller is through can_curate.
// Mirrors app/console/feature-gates/page.tsx.
export default async function MarketplacePage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const profile = await getAccountProfile().catch(
    (): { owner_name: string } => ({ owner_name: "" }),
  );

  let entries: MarketplaceEntries | null = null;
  let loadFailed = false;
  let notPermitted = false;
  try {
    entries = await getMarketplaceEntries();
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
