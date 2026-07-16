import { redirect } from "next/navigation";
import { ShieldAlert } from "lucide-react";

import {
  getViewer,
  getAccountProfile,
  getMarketplaceEntries,
  type MarketplaceEntries,
} from "@/lib/control-plane/client";
import { can } from "@/lib/viewer-gates";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { MarketplaceManager } from "@/components/marketplace/marketplace-manager";

// Admin marketplace page (issue #309, agent-subsystem blueprint Step 2.3).
// Lists the admin-curated MCP and skills catalog for the current workspace
// and lets a platform admin curate new entries and enable/disable each one;
// the control-plane enforces platform-admin, so this is defence in depth:
// non-admins get an access state without an upstream call. Mirrors
// app/console/feature-gates/page.tsx.
export default async function MarketplacePage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const profile = await getAccountProfile().catch(
    (): { owner_name: string } => ({ owner_name: "" }),
  );

  const isAdmin = can(viewer, "platform.admin");

  let entries: MarketplaceEntries | null = null;
  let loadFailed = false;
  if (isAdmin) {
    try {
      entries = await getMarketplaceEntries();
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
      active="/console/marketplace"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Marketplace</span>
      }
    >
      <PageHeader
        eyebrow="Admin"
        title="MCP and skills marketplace"
        description="Curate MCP servers, rules, skills, and prompt templates, and choose which ones this workspace's agents can use."
      />

      {!isAdmin ? (
        <EmptyState
          icon={<ShieldAlert size={20} />}
          title="Admin access required"
          description="Only platform admins can view and curate the marketplace. Ask an admin if you need a connector enabled."
        />
      ) : loadFailed || !entries ? (
        <EmptyState
          title="Could not load the marketplace catalog"
          description="Something went wrong loading the catalog. Refresh to try again."
        />
      ) : entries.entries.length === 0 ? (
        <MarketplaceManager entries={[]} />
      ) : (
        <MarketplaceManager entries={entries.entries} />
      )}
    </ConsoleShell>
  );
}
