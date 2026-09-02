import { redirect } from "next/navigation";

import { getCatalogModels } from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
  tolerate,
} from "@/lib/console/data";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { ModelCatalogBrowser } from "@/components/catalog/model-catalog-browser";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";

export default async function CatalogPage() {
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const [models, profile] = await Promise.all([
    // A catalog that failed to load must not browse as a catalog with no
    // models in it: that reads as "this deployment routes to nothing"
    // (issue #494).
    tolerate(getCatalogModels()),
    requireAccountProfile(),
  ]);

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
      active="/console/catalog"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Model catalog
        </span>
      }
    >
      <PageHeader
        eyebrow="Build"
        title="Model catalog"
        description="Available models with per-million-token input, output and prompt-cache pricing. Search, filter by capability, or sort by price."
      />

      {models ? (
        <ModelCatalogBrowser models={models} />
      ) : (
        <EmptyState
          title="Could not load the model catalog"
          description="We could not reach the catalog service. Refresh to try again."
        />
      )}
    </ConsoleShell>
  );
}
