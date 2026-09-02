import Link from "next/link";
import { redirect } from "next/navigation";

import {
  getCatalogModels,
  type CatalogModel,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
} from "@/lib/console/data";
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

type CatalogList = CatalogModel[] | null;

/**
 * Console privacy and data-policy surface (/console/privacy).
 *
 * Every statement on this page is either something verified true and
 * enforced in code today, or explicitly labeled as a gap. Nothing here is
 * decorative, and nothing here claims a guarantee the system does not keep.
 * Specifically:
 *
 *   - The metering claim is scoped to the per-request usage record and is
 *     phrased as behaviour, not as a structural impossibility. The real
 *     table (supabase/migrations/20260330_02_usage_accounting.sql) carries
 *     internal_metadata jsonb and customer_tags jsonb, and what keeps
 *     message content out of them is usage.RedactMetadata
 *     (apps/control-plane/internal/usage/service.go), a key-name denylist.
 *     UsageEventRow in lib/control-plane/client.ts is a console-side
 *     projection of that table, not the table, so it cannot carry the claim.
 *     customer_tags gets no redaction at all, which is safe only because
 *     nothing writes it today (no call site in edge-api sets CustomerTags).
 *     If a customer-supplied tag ever reaches that column, this card needs
 *     a sentence about it.
 *   - Content that IS stored is named rather than omitted: batch input and
 *     output files (apps/control-plane/internal/batchstore/executor), file
 *     uploads (apps/edge-api/internal/files), and RAG documents and chunks
 *     (public.rag_chunks.content). A blanket "no content stored" sentence
 *     would be false for any customer using one of those three.
 *   - Third-party routing is disclosed without naming which provider serves
 *     which model. The page does not claim provider identity is absent from
 *     every customer-facing surface, because catalogue summaries still name
 *     vendors today (issue #1284, fix in flight as PR #1300). It claims only
 *     what is enforced: provider identity is stripped from error responses
 *     (apps/edge-api/internal/errors/provider_blind_test.go) and is not
 *     shown here.
 *   - Data-collection posture is disclosed because it is enforced for a
 *     subset of routes: deploy/litellm/config.yaml sets
 *     provider.data_collection deny with allow_fallbacks false on the free
 *     routes and on nothing else.
 *   - The provider allow/block section says plainly that no tenant control
 *     persists a choice into the routing layer's AllowedProviders input
 *     today, rather than rendering a toggle that would not do anything. The
 *     alias-to-route relationship is described as one route per request,
 *     not as a permanent 1:1 pinning, because routing.SelectRoute returns
 *     FallbackRouteIDs and nothing constrains a fallback to the primary's
 *     provider.
 *   - A closing card names what this page does not cover, so it reads as
 *     scoped rather than as a complete privacy statement.
 */
export default async function PrivacyPage() {
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const [profile, models] = await Promise.all([
    requireAccountProfile(),
    getCatalogModels().catch((): CatalogList => null),
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
              The metering record Hive keeps per request carries token
              counts, cost, status, model alias, endpoint, error codes and
              operational metadata. Message content is stripped from that
              metadata before the record is written, so your prompts and
              completions are not part of it.
            </p>
            <p>
              No synchronous request through this gateway has its request or
              response body stored: chat completion, completion, embedding
              and audio requests are relayed and not retained. One
              exception, named because it is a real one: when an upstream
              provider fails a request, the error text it returned is
              written to this deployment's server logs for diagnosis, and a
              provider that echoes part of a request in its error text would
              have that fragment logged with it.
            </p>
            <p>
              Three endpoints on this same gateway do store content, because
              storing it is what they are for. If you use them, the content
              below is held on your account until you delete it:
            </p>
            <ul className="flex flex-col gap-2 pl-5 list-disc">
              <li>
                <strong className="font-medium">Batch jobs.</strong> The
                input file you upload holds your request bodies verbatim,
                and the job writes an output file holding the upstream
                responses verbatim. Both are stored as files on your
                account.
              </li>
              <li>
                <strong className="font-medium">File uploads.</strong> Anything
                uploaded through the files API is stored until you
                delete it through that same API.
              </li>
              <li>
                <strong className="font-medium">RAG documents.</strong> Indexed
                documents are stored in full, both as the original
                upload and as the text chunks the retrieval index searches.
                Deleting a document deletes its chunks with it.
              </li>
            </ul>
            <p>
              There is no automatic deletion schedule for any of this. The
              per-request metering record is retained indefinitely for
              billing and audit unless an operator removes it at the
              database level, and stored content stays until you delete it.
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
              This deployment&apos;s current model catalog, and whether a
              request stays inside this deployment&apos;s boundary.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-4 text-sm text-[var(--color-ink-2)] leading-relaxed flex flex-col gap-4">
            <p>
              Every model in the catalog below is served by a third-party
              model provider&apos;s infrastructure, not hosted inside this
              deployment. When a request routes to one of them, its content
              leaves this deployment&apos;s infrastructure boundary to reach
              that provider, regardless of whether you are on Hive Cloud or
              a customer-hosted Hive Enterprise deployment. A Hive
              Enterprise deployment configured for self-hosted inference
              only would not be subject to this.
            </p>
            <p>
              Which specific provider serves which model is not named in
              error responses and is not shown on this page. That is the
              scope of it: provider names can still appear in model
              descriptions elsewhere in the console, so read this as a
              property of those two surfaces rather than a product-wide
              guarantee.
            </p>
            <p>
              For a subset of models, routing is configured to refuse
              upstream providers that collect user data, and to fail the
              request rather than fall back to one that does. That
              preference is not set on every model. Beyond it, Hive does not
              control and does not represent whether a third-party provider
              retains the content it receives or trains on it. Where that
              distinction matters for a workload, confirm the posture for
              the specific model before sending data through it.
            </p>
            {models === null ? (
              <p className="text-xs text-[var(--color-ink-3)]">
                The model catalog could not be loaded right now. That is a
                display failure on this page and does not change anything
                stated above.
              </p>
            ) : models.length > 0 ? (
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
                This deployment currently exposes no models in its catalog.
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
              call the model aliases it is entitled to, and each request
              resolves to a single upstream route. An alias can have more
              than one eligible route, and which one serves a given request
              can vary, so entitlement to an alias is not by itself a
              statement about which provider serves it.
            </p>
            <Link
              href="/console/api-keys"
              className="text-sm font-medium text-[var(--color-accent)] hover:underline w-fit"
            >
              Manage API key model access
            </Link>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>What this page does not cover</CardTitle>
            <CardDescription>
              A scoped disclosure of gateway behaviour, not a complete
              privacy statement. The questions below are not answered here.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-4 text-sm text-[var(--color-ink-2)] leading-relaxed flex flex-col gap-3">
            <ul className="flex flex-col gap-2 pl-5 list-disc">
              <li>
                Where this deployment and its data stores are physically
                located.
              </li>
              <li>
                Whether Hive personnel can read stored content, and under
                what controls.
              </li>
              <li>
                Incident and breach notification: what you would be told,
                and when.
              </li>
              <li>
                Hive Chat&apos;s own conversation storage, which is a
                separate system from the API gateway described here.
              </li>
              <li>
                Any language other than English. This page is published in
                English only today, including for Bengali-locale readers,
                while the console navigation around it is translated.
              </li>
            </ul>
            <p>
              For any of the above, ask the team that operates this
              deployment rather than inferring an answer from this page.
            </p>
          </CardContent>
        </Card>
      </div>
    </ConsoleShell>
  );
}
