import Link from "next/link";
import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getCatalogModels,
  getViewer,
  type CatalogModel,
} from "@/lib/control-plane/client";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

const LIFECYCLE_TONE: Record<string, "success" | "accent" | "neutral"> = {
  stable: "success",
  preview: "accent",
  hidden: "neutral",
};

/**
 * Console privacy and data-policy surface (/console/privacy).
 *
 * Every statement on this page is either something verified true and
 * enforced in code today, or explicitly labeled as a gap. Nothing here is
 * decorative. Specifically:
 *
 *   - "No content stored" is a property of the usage_events schema itself
 *     (UsageEventRow in lib/control-plane/client.ts carries no request or
 *     response body field), not a policy promise layered on top of storage
 *     that could hold content.
 *   - The provider names (OpenRouter, Groq) are named deliberately here,
 *     departing from the rest of the console, which never exposes a
 *     provider identity anywhere else (PublicCatalogModel and CatalogModel
 *     both omit a provider field; provider-blind error handling strips
 *     provider strings from every failure response). A page whose entire
 *     purpose is disclosing where data goes cannot honestly omit that,
 *     without implying data never leaves the deployment when it does.
 *   - The provider allow/block section says plainly that no tenant control
 *     persists a choice into the routing layer's AllowedProviders input
 *     today (verified: no call site in apps/edge-api sets it), rather than
 *     rendering a toggle that would not do anything.
 */
export default async function PrivacyPage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const [profile, models] = await Promise.all([
    getAccountProfile().catch(
      (): { owner_name: string } => ({ owner_name: "" }),
    ),
    getCatalogModels().catch((): CatalogModel[] => []),
  ]);

  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/privacy"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Privacy
        </span>
      }
    >
      <PageHeader
        eyebrow="Workspace"
        title="Privacy and data policy"
        description="What Hive's API gateway stores, where a request goes once it leaves the gateway, and what is not yet a real control. Every statement below is backed by something verified in code, not a policy promise layered on top."
      />

      <div className="flex flex-col gap-8">
        <Card>
          <CardHeader>
            <CardTitle>Request and response content</CardTitle>
            <CardDescription>
              Scoped to requests made with your API keys through Hive&apos;s
              gateway. This page does not describe Hive Chat&apos;s own
              conversation storage, which is a separate system.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-4 text-sm text-[var(--color-ink-2)] leading-relaxed flex flex-col gap-3">
            <p>
              The gateway does not store the content of your requests or
              responses. The usage record it keeps per request contains
              token counts, cost, status, model alias, and error codes only,
              never message content, and there is no field in that record a
              body could land in.
            </p>
            <p>
              There is currently no automatic deletion schedule for usage
              records. They are retained indefinitely for billing and audit
              unless removed at the database level by an operator.
            </p>
            <p>
              Error messages returned by the API are sanitized before they
              reach you: which upstream provider served or failed a request
              is never named in an error response, by design and enforced
              in tests.
            </p>
            <Link
              href="/console/logs"
              className="text-sm font-medium text-[var(--color-accent)] hover:underline w-fit"
            >
              View your usage log
            </Link>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Where a request goes</CardTitle>
            <CardDescription>
              This deployment&apos;s current model catalog, and the upstream
              infrastructure providers behind it.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-4 text-sm text-[var(--color-ink-2)] leading-relaxed flex flex-col gap-4">
            <p>
              Every model in the catalog is served by one of two upstream
              infrastructure providers: OpenRouter, for the DeepSeek models,
              or Groq, for Hive&apos;s own fast, default, and auto aliases.
              No other providers are configured on this deployment. When a
              request routes to either of them, its content leaves this
              deployment&apos;s infrastructure boundary to reach that
              provider, regardless of whether you are on Hive Cloud or a
              customer-hosted Hive Enterprise deployment. A Hive Enterprise
              deployment configured for self-hosted inference only would not
              be subject to this.
            </p>
            {models.length > 0 ? (
              <ul className="flex flex-col gap-2">
                {models.map((model) => (
                  <li
                    key={model.id}
                    className="flex items-center justify-between gap-3 rounded-md border border-[var(--color-border)] px-3 py-2"
                  >
                    <div className="flex flex-col gap-0.5 min-w-0">
                      <span className="text-sm font-medium text-[var(--color-ink)] truncate">
                        {model.display_name}
                      </span>
                      <span className="text-xs text-[var(--color-ink-3)] font-mono truncate">
                        {model.id}
                      </span>
                    </div>
                    <Badge tone={LIFECYCLE_TONE[model.lifecycle] ?? "neutral"}>
                      {model.lifecycle}
                    </Badge>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-xs text-[var(--color-ink-3)]">
                Catalog unavailable right now.
              </p>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Provider allow/block</CardTitle>
            <CardDescription>
              What is and is not a real, enforced control today.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-4 text-sm text-[var(--color-ink-2)] leading-relaxed flex flex-col gap-3">
            <p>
              Hive&apos;s routing layer accepts a per-request provider
              allow-list, but no tenant-facing control persists a choice into
              it today, so there is currently no way to restrict your
              account to one provider over another. This page will not
              render a toggle for it until it does something.
            </p>
            <p>
              Model access itself is enforced per API key: a key can only
              call the model aliases it is entitled to, and every alias
              resolves to exactly one upstream route, so which alias a key
              can reach determines which provider serves it.
            </p>
            <Link
              href="/console/api-keys"
              className="text-sm font-medium text-[var(--color-accent)] hover:underline w-fit"
            >
              Manage API key model access
            </Link>
          </CardContent>
        </Card>
      </div>
    </ConsoleShell>
  );
}
