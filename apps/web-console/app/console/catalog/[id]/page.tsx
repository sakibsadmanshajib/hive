import type { ReactElement } from "react";
import Link from "next/link";
import { ArrowRight, ArrowUpRight } from "lucide-react";
import { redirect } from "next/navigation";

import { chatModelUrl, isChatCapable } from "@/lib/chat-link";

import {
  getAccountProfile,
  getAnalyticsUsage,
  getCatalogModels,
  getViewer,
  type UsageSummaryRow,
} from "@/lib/control-plane/client";
import { ConsoleNotFound } from "@/components/app-shell/console-not-found";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { ModelDetail } from "@/components/catalog/model-detail";
import { PageHeader } from "@/components/ui/page-header";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/cn";

interface ModelDetailPageProps {
  params: Promise<{ id: string }>;
}

// The analytics window this page reports. 30d is the longest preset the
// control plane accepts without an explicit from/to pair, and a model page is
// a "what has this cost me" surface rather than a live one.
const USAGE_WINDOW = "30d";
const USAGE_WINDOW_LABEL = "last 30 days";

export default async function ModelDetailPage(
  props: ModelDetailPageProps,
): Promise<ReactElement> {
  const { id } = await props.params;
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  // There is no single-model endpoint: /api/v1/catalog/models returns the whole
  // tenant-filtered list and nothing narrower exists. Reading the list and
  // selecting from it also means an alias this tenant may not see is a 404
  // here for free, rather than a page that renders a model they cannot call.
  const [models, profile] = await Promise.all([
    getCatalogModels(),
    getAccountProfile().catch(
      (): { owner_name: string } => ({ owner_name: "" }),
    ),
  ]);

  const model = models.find((row) => row.id === id);
  if (!model) {
    // Rendered in place rather than raised through notFound(), which paints
    // nothing on first load in Next 16.3.x. Full reasoning, and why the
    // role-gated pages must keep notFound() instead, in the component (issue
    // #1652).
    return (
      <ConsoleNotFound
        viewer={viewer}
        ownerName={profile.owner_name || null}
        active="/console/catalog"
        section="Model catalog"
        eyebrow="Build"
        title="Model not found"
        description="No model on this workspace matches that address. It may have been renamed or retired, or it may not be enabled for this workspace."
        backHref="/console/catalog"
        backLabel="Back to catalog"
      />
    );
  }

  // Usage is a separate call and a separate failure: an analytics outage must
  // not take the pricing table down with it, and it must not render as zero
  // usage either. `null` row plus `unavailable: false` means genuinely no
  // requests; `unavailable: true` means we do not know.
  let usage: UsageSummaryRow | null = null;
  let usageUnavailable = false;
  try {
    const rows = await getAnalyticsUsage({
      group_by: "model",
      window: USAGE_WINDOW,
    });
    usage = rows.find((row) => row.group_key === model.id) ?? null;
  } catch {
    usageUnavailable = true;
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
      active="/console/catalog"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Model catalog
        </span>
      }
    >
      <PageHeader
        eyebrow="Build"
        title={model.display_name || model.id}
        description={model.summary || undefined}
        actions={
          <>
            {/* Same capability gate as the catalog table: an embedding, STT or
                TTS alias cannot serve a chat completion, so it gets no link
                into chat (issue #1647). */}
            {isChatCapable(model.capability_badges) ? (
              <a
                href={chatModelUrl(model.id)}
                target="_blank"
                rel="noopener noreferrer"
                title="Opens Hive Chat with this model preselected"
                className={cn(buttonVariants({ variant: "accent", size: "sm" }))}
              >
                Try in chat
                <ArrowUpRight size={14} aria-hidden="true" />
              </a>
            ) : null}
            <Link
              href="/console/catalog"
              className={cn(
                buttonVariants({ variant: "secondary", size: "sm" }),
              )}
            >
              Back to catalog
              <ArrowRight size={14} aria-hidden="true" />
            </Link>
          </>
        }
      />

      <ModelDetail
        model={model}
        usage={usage}
        usageUnavailable={usageUnavailable}
        usageWindowLabel={USAGE_WINDOW_LABEL}
      />
    </ConsoleShell>
  );
}
