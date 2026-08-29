import { redirect } from "next/navigation";
import { KeyRound } from "lucide-react";

import {
  getAccountProfile,
  getApiKeys,
  getCatalogModels,
  getViewer,
  type CatalogModel,
} from "@/lib/control-plane/client";
import { apiBaseUrl } from "@/lib/api-contract";
import { pickQuickstartAlias } from "@/lib/quickstart-model";
import { can } from "@/lib/viewer-gates";
import { ApiKeyCreateForm } from "@/components/api-keys/api-key-create-form";
import { ApiKeyList } from "@/components/api-keys/api-key-list";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";

/** How long the quickstart's model lookup may hold up this page. */
const CATALOG_READ_BUDGET_MS = 2_000;

/**
 * The catalog, or an empty list if it fails or takes too long.
 *
 * `Promise.race` rather than an abort signal, because `getCatalogModels` takes
 * none: the request is left to finish into nothing. That is acceptable for one
 * bounded read whose only consumer is a string in a snippet, and it is the
 * difference between a slow catalog degrading this page and a slow catalog
 * hanging it.
 */
async function catalogModelsOrNone(): Promise<CatalogModel[]> {
  const timeout = new Promise<CatalogModel[]>((resolve) => {
    setTimeout(() => {
      console.error(
        `ApiKeysPage: model catalog did not answer within ${CATALOG_READ_BUDGET_MS}ms, using the seeded alias`,
      );
      resolve([]);
    }, CATALOG_READ_BUDGET_MS);
  });
  const read = getCatalogModels().catch((error: unknown): CatalogModel[] => {
    console.error("ApiKeysPage: could not load the model catalog", error);
    return [];
  });
  return Promise.race([read, timeout]);
}

export default async function ApiKeysPage() {
  const viewer = await getViewer();
  const canManage = can(viewer, "api_keys.write");
  if (!canManage) {
    redirect("/console/settings/profile");
  }

  const [keys, profile, models] = await Promise.all([
    getApiKeys(),
    getAccountProfile().catch(
      (): { owner_name: string } => ({ owner_name: "" }),
    ),
    // Names a model that actually answers on this deployment. Bounded, and
    // that bound matters more here than on /console/docs: this is the page an
    // operator opens to revoke a leaked key, and `getCatalogModels` sets no
    // timeout of its own, so a wedged catalog endpoint would hold the whole
    // server render open and leave that operator staring at a spinner. What
    // the read buys is a nicer model id in a snippet nobody sees until after
    // they mint a key, and `pickQuickstartAlias([])` already returns a working
    // answer without it. Revocation must never wait on that trade.
    //
    // The failure is logged rather than swallowed: the degraded result is a
    // snippet naming a different model than this deployment would recommend,
    // which is indistinguishable from working unless it leaves a trace.
    catalogModelsOrNone(),
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
      active="/console/api-keys"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">API keys</span>
      }
    >
      <PageHeader
        eyebrow="Authentication"
        title="API keys"
        description="Issue and revoke programmatic credentials. To rotate a key, create a replacement and revoke the old one. Keys are shown in full only at creation, so store them in a secret manager."
      />

      <div className="flex flex-col gap-6">
        <ApiKeyCreateForm
          apiBaseUrl={apiBaseUrl()}
          quickstartModel={pickQuickstartAlias(models)}
        />
        {keys.length === 0 ? (
          <EmptyState
            icon={<KeyRound size={20} aria-hidden="true" />}
            title="No API keys yet"
            description="Create your first key above to start authenticating requests against the Hive API."
          />
        ) : (
          <ApiKeyList keys={keys} canManage={canManage} />
        )}
      </div>
    </ConsoleShell>
  );
}
