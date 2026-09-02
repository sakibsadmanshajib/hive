import { redirect } from "next/navigation";

import {
  getCatalogModels,
  type CatalogModel,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
  tolerate,
} from "@/lib/console/data";
import {
  apiBaseUrl,
  OPENAPI_ROUTE,
  type EndpointSection,
  buildEndpointSections,
  diffSpecAgainstMatrix,
  loadSpecOperations,
  loadSupportMatrix,
} from "@/lib/api-contract";
import { buildQuickstartCurl, pickQuickstartAlias } from "@/lib/quickstart-model";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { PageHeader } from "@/components/ui/page-header";

/**
 * The console's own API reference.
 *
 * Every endpoint, status and count below is read from
 * `packages/openai-contract/` at request time, never typed in here. That is
 * the whole point: a hand written endpoint list is stale the first time a
 * route changes, and a docs page that is quietly wrong is worse than no docs
 * page. The unsupported and out-of-scope endpoints are listed too, because an
 * integrator needs to know what will not work before building against it.
 */

const BADGE_TONE: Record<string, "success" | "accent" | "outline" | "neutral"> = {
  supported_now: "success",
  planned_for_launch: "accent",
  explicitly_unsupported_at_launch: "outline",
  out_of_scope: "neutral",
};

function CodeBlock({ children }: { children: string }) {
  return (
    <pre className="overflow-x-auto rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-4 py-3 text-xs leading-relaxed text-[var(--color-ink-2)]">
      <code className="font-mono">{children}</code>
    </pre>
  );
}

export default async function DocsPage() {
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const [profile, models] = await Promise.all([
    requireAccountProfile(),
    // The quickstart names a model that actually exists on this deployment.
    // If the catalog cannot be read the snippets still have to be runnable, so
    // they fall back to the alias seeded by supabase/migrations.
    tolerate(getCatalogModels()).then((rows): CatalogModel[] => rows ?? []),
  ]);

  // Name an alias that actually answers on a fresh account. See
  // pickQuickstartAlias for why "the first hive- id" was not that (issue #1372).
  const alias = pickQuickstartAlias(models);
  const matrix = loadSupportMatrix();
  const specOperations = loadSpecOperations();
  const disagreements = diffSpecAgainstMatrix(specOperations, matrix);
  const countKind = (kind: string) =>
    disagreements.filter((entry) => entry.kind === kind).length;
  const missingFromSpec = countKind("missing_from_spec");
  const missingFromMatrix = countKind("missing_from_matrix");
  const statusMismatch = countKind("status_mismatch");
  const sections = buildEndpointSections(matrix);

  // Composed by the shared builder, which the created-key panel on
  // /console/api-keys also uses. Two hand-written copies of the same example
  // drift, and a drifted first request is what issue #550 is about.
  const baseUrl = apiBaseUrl();
  const curl = buildQuickstartCurl({ baseUrl, model: alias });

  const python = `# pip install openai
import os

from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}",
    api_key=os.environ["HIVE_API_KEY"],
)

response = client.chat.completions.create(
    model="${alias}",
    messages=[{"role": "user", "content": "Say hello in one sentence."}],
)
print(response.choices[0].message.content)`;

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
      active="/console/docs"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          API documentation
        </span>
      }
    >
      <PageHeader
        eyebrow="Build"
        title="API reference"
        description="An OpenAI-compatible gateway. Point any OpenAI SDK at the base URL below and swap the key. Every endpoint on this page, supported or not, is read from the generated contract in this repository."
      />

      <div className="flex flex-col gap-8">
        <Card>
          <CardHeader>
            <CardTitle>Quickstart</CardTitle>
            <CardDescription>
              Create a key under API keys, export it as{" "}
              <code className="font-mono">HIVE_API_KEY</code>, then call the
              gateway exactly as you would call OpenAI.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-5">
            <dl className="grid gap-3 sm:grid-cols-2">
              <div className="flex flex-col gap-1">
                <dt className="text-2xs uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
                  Base URL
                </dt>
                <dd className="font-mono text-xs text-[var(--color-ink)]">
                  {baseUrl}
                </dd>
              </div>
              <div className="flex flex-col gap-1">
                <dt className="text-2xs uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
                  Authentication
                </dt>
                <dd className="font-mono text-xs text-[var(--color-ink)]">
                  Authorization: Bearer &lt;your key&gt;
                </dd>
              </div>
            </dl>

            <div className="flex flex-col gap-2">
              <h3 className="text-xs font-semibold text-[var(--color-ink-2)]">curl</h3>
              <CodeBlock>{curl}</CodeBlock>
            </div>

            <div className="flex flex-col gap-2">
              <h3 className="text-xs font-semibold text-[var(--color-ink-2)]">
                Python, official OpenAI SDK
              </h3>
              <CodeBlock>{python}</CodeBlock>
            </div>

            <p className="text-xs text-[var(--color-ink-3)] leading-relaxed">
              <span className="font-mono">{alias}</span> is a routing alias, not
              a single upstream model. See the model catalog for what each alias
              resolves to and what it costs.
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Machine-readable spec</CardTitle>
            <CardDescription>
              The OpenAPI document this page is generated from. Point your own
              tooling at it rather than reading this page.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <p className="text-xs text-[var(--color-ink-3)] leading-relaxed">
              Served unauthenticated at{" "}
              <a
                className="font-mono text-[var(--color-ink-2)] underline underline-offset-4 hover:text-[var(--color-ink)]"
                href={OPENAPI_ROUTE}
              >
                {OPENAPI_ROUTE}
              </a>{" "}
              on this console&apos;s own origin · {specOperations.length}{" "}
              operations described · support matrix version {matrix.version},
              generated {matrix.generated}
            </p>
            <p className="text-xs text-[var(--color-ink-3)] leading-relaxed">
              The generation date is printed because it is the honest measure of
              how current this page is. Nothing here is hand maintained, so if
              that date is old, the classifications below are that old too.
            </p>
          </CardContent>
        </Card>

        {disagreements.length > 0 ? (
          <Card>
            <CardHeader>
              <CardTitle>
                Where the spec and the support matrix disagree
              </CardTitle>
              <CardDescription>
                {disagreements.length} operations are described differently by
                the two generated files: {missingFromSpec} the matrix
                classifies but the spec never describes, {statusMismatch} the
                two give different support statuses, {missingFromMatrix}{" "}
                the spec declares but the matrix never classified. All of them
                are listed rather than one file being silently preferred,
                because either side can be the stale one.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <details>
                <summary className="cursor-pointer text-xs text-[var(--color-ink-2)]">
                  Show all {disagreements.length}
                </summary>
                <ul className="mt-3 flex flex-col gap-2">
                  {disagreements.map((disagreement) => (
                    <li
                      key={disagreement.operation}
                      className="text-xs leading-relaxed text-[var(--color-ink-3)]"
                    >
                      <span className="font-mono text-[var(--color-ink-2)]">
                        {disagreement.operation}
                      </span>{" "}
                      — {disagreement.detail}
                    </li>
                  ))}
                </ul>
              </details>
            </CardContent>
          </Card>
        ) : null}

        {sections.map((section) => (
          <Card key={section.meta.status}>
            <CardHeader>
              <div className="flex items-center gap-2">
                <CardTitle>{section.meta.label}</CardTitle>
                <Badge tone={BADGE_TONE[section.meta.status] ?? "neutral"}>
                  {section.count}
                </Badge>
              </div>
              <CardDescription>{section.meta.meaning}</CardDescription>
            </CardHeader>
            <CardContent>
              <EndpointFamilies
                families={section.families}
                collapsed={section.meta.status !== "supported_now"}
                count={section.count}
              />
            </CardContent>
          </Card>
        ))}
      </div>
    </ConsoleShell>
  );
}

function EndpointFamilies({
  families,
  collapsed,
  count,
}: {
  families: EndpointSection["families"];
  collapsed: boolean;
  count: number;
}) {
  const tables = (
    <div className="flex flex-col gap-6">
      {families.map((family) => (
        <div key={family.name} className="flex flex-col gap-2">
          <h3 className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]">
            {family.name}
          </h3>
          <div className="overflow-x-auto">
            <table className="w-full min-w-[36rem] border-collapse text-xs">
              <thead>
                <tr className="text-left text-[var(--color-ink-3)]">
                  <th className="w-16 py-1 font-medium">Method</th>
                  <th className="py-1 font-medium">Path</th>
                  <th className="py-1 font-medium">Notes</th>
                </tr>
              </thead>
              <tbody>
                {family.endpoints.map((endpoint) => (
                  <tr
                    key={`${endpoint.method} ${endpoint.path}`}
                    className="border-t border-[var(--color-border)] align-top"
                  >
                    <td className="py-1.5 pr-3 font-mono text-[var(--color-ink-2)]">
                      {endpoint.method}
                    </td>
                    <td className="py-1.5 pr-3 font-mono text-[var(--color-ink)]">
                      {endpoint.path}
                    </td>
                    <td className="py-1.5 text-[var(--color-ink-3)]">
                      {endpoint.notes}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ))}
    </div>
  );

  if (!collapsed) {
    return tables;
  }

  return (
    <details>
      <summary className="cursor-pointer text-xs text-[var(--color-ink-2)]">
        Show all {count}
      </summary>
      <div className="mt-4">{tables}</div>
    </details>
  );
}
