import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getCatalogModels,
  getViewer,
} from "@/lib/control-plane/client";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { ModelCatalogTable } from "@/components/catalog/model-catalog-table";
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
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
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
        description="Available models and per-million-token pricing across providers. Capabilities are surfaced as tags."
      />

      <ModelCatalogTable models={models} />
    </ConsoleShell>
  );
}
