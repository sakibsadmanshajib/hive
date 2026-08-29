import { notFound, redirect } from "next/navigation";

import { isPlatformAdminViewer } from "@/lib/viewer-gates";
import { ShieldAlert } from "lucide-react";

import {
  getViewer,
  getAccountProfile,
  getProviders,
  ControlPlaneError,
  type CustomProvider,
} from "@/lib/control-plane/client";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { ProvidersManager } from "@/components/providers/providers-manager";

// Providers page: the platform registry of custom provider endpoints
// (public.custom_providers, PR #199 era). The control-plane gates the list
// and every mutation on the platform administrator (RequirePlatformAdmin),
// so the page itself now refuses to render for anyone else: hidden nav is
// not access control (#947/#948/#949 family), and a reachable URL returning
// 200 confirms the surface exists, so a customer gets 404 instead.
// Mirrors app/console/marketplace/page.tsx.
export default async function ProvidersPage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  // Server-side role gate: refuse the page shell before any data fetch.
  if (!isPlatformAdminViewer(viewer)) {
    notFound();
  }

  const profile = await getAccountProfile().catch(
    (): { owner_name: string } => ({ owner_name: "" }),
  );

  let providers: CustomProvider[] | null = null;
  let loadFailed = false;
  let notPermitted = false;
  try {
    providers = await getProviders();
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
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/providers"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Providers</span>
      }
    >
      <PageHeader
        eyebrow="Admin"
        title="Provider endpoints"
        description="Register OpenAI-compatible upstream endpoints and route models at them. Each entry stores a base URL and the name of the environment variable carrying its API key; keys themselves never enter Hive."
      />

      {notPermitted ? (
        <EmptyState
          icon={<ShieldAlert size={20} />}
          title="Managed by your administrator"
          description="Provider endpoint registration is a platform operation. Ask your platform administrator to add or change an endpoint."
        />
      ) : loadFailed || !providers ? (
        <EmptyState
          title="Could not load providers"
          description="Something went wrong loading the provider registry. Refresh to try again."
        />
      ) : (
        <ProvidersManager providers={providers} />
      )}
    </ConsoleShell>
  );
}
