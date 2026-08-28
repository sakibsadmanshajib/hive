import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getCatalogModels,
  getViewer,
} from "@/lib/control-plane/client";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { ModelCatalogBrowser } from "@/components/catalog/model-catalog-browser";
import { PageHeader } from "@/components/ui/page-header";

export default async function CatalogPage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const [models, profile] = await Promise.all([
    getCatalogModels(),
    getAccountProfile().catch(
      (): { owner_name: string } => ({ owner_name: "" }),
    ),
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
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
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

      <ModelCatalogBrowser models={models} />
    </ConsoleShell>
  );
}
