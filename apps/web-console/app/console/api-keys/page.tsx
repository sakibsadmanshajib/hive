import { redirect } from "next/navigation";
import { KeyRound } from "lucide-react";

import {
  getAccountProfile,
  getApiKeys,
  getViewer,
} from "@/lib/control-plane/client";
import { ApiKeyCreateForm } from "@/components/api-keys/api-key-create-form";
import { ApiKeyList } from "@/components/api-keys/api-key-list";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";

export default async function ApiKeysPage() {
  const viewer = await getViewer();
  const canManage = viewer.gates.can_manage_api_keys;
  if (!canManage) {
    redirect("/console/settings/profile");
  }

  const [keys, profile] = await Promise.all([
    getApiKeys(),
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
      active="/console/api-keys"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">API keys</span>
      }
    >
      <PageHeader
        eyebrow="Authentication"
        title="API keys"
        description="Issue, rotate, and revoke programmatic credentials. Keys are shown in full only at creation — store them in a secret manager."
      />

      <div className="flex flex-col gap-6">
        <ApiKeyCreateForm />
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
