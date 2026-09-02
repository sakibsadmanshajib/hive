import Link from "next/link";
import { redirect } from "next/navigation";

import {
  getApiKeys,
  getCatalogModels,
  getUsageEvents,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
} from "@/lib/console/data";
import type { UsageEventRow } from "@/lib/control-plane/client";
import { UsageLogsCsv } from "@/components/logs/usage-logs-csv";
import { LogsFilters } from "@/components/logs/logs-filters";
import { UsageLogsHistogram } from "@/components/logs/usage-logs-histogram";
import { UsageLogsTable } from "@/components/logs/usage-logs-table";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/cn";

interface LogsPageProps {
  searchParams: Promise<{
    window?: string;
    model?: string;
    status?: string;
    key?: string;
    errors?: string;
    cursor?: string;
  }>;
}

const WINDOW_PRESETS = ["1h", "24h", "7d", "30d"] as const;

// Type predicate so the page can narrow searchParams.window to a defined
// string without a cast.
function isValidWindow(value: string | undefined): value is string {
  return WINDOW_PRESETS.some((preset) => preset === value);
}

export default async function LogsPage({ searchParams }: LogsPageProps) {
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const params = await searchParams;
  const state = {
    window: isValidWindow(params.window) ? params.window : null,
    model: params.model?.trim() || null,
    status: params.status?.trim() || null,
    key: params.key?.trim() || null,
    errors: params.errors === "true",
  };
  const cursor = params.cursor ?? null;

  const profile = await requireAccountProfile();

  let page: Awaited<ReturnType<typeof getUsageEvents>> | null = null;
  let fetchError = false;
  const [keys, models] = await Promise.all([
    getApiKeys().catch((): [] => []),
    getCatalogModels()
      .then((rows): string[] => rows.map((row) => row.id))
      .catch((): string[] => []),
  ]);

  try {
    page = await getUsageEvents({
      limit: 50,
      window: state.window ?? undefined,
      modelAlias: state.model ?? undefined,
      status: state.status ?? undefined,
      apiKeyId: state.key ?? undefined,
      errorsOnly: state.errors,
      cursor: cursor ?? undefined,
    });
  } catch {
    fetchError = true;
  }
  const events = page?.events ?? [];
  const nextCursor = page?.next_cursor ?? null;

  const keyNames: Record<string, string> = {};
  for (const key of keys) {
    keyNames[key.id] = key.nickname || `…${key.redacted_suffix}`;
  }

  // First-run guidance shows only when the account genuinely has nothing to
  // show yet: no rows, no filters narrowing the view, and no pagination
  // cursor. An exhausted trailing page (cursor set, zero rows) is a filtered
  // view, not an empty account, and must keep its Reset link instead of the
  // create-key pitch.
  const hasActiveFilters =
    Boolean(state.window || state.model || state.status || state.key || state.errors);
  const firstRun =
    events.length === 0 && !hasActiveFilters && !cursor && !fetchError;

  function buildPageUrl(nextCursorValue: string | null): string {
    const qs = new URLSearchParams();
    if (state.window) qs.set("window", state.window);
    if (state.model) qs.set("model", state.model);
    if (state.status) qs.set("status", state.status);
    if (state.key) qs.set("key", state.key);
    if (state.errors) qs.set("errors", "true");
    if (nextCursorValue) qs.set("cursor", nextCursorValue);
    const query = qs.toString();
    return `/console/logs${query ? `?${query}` : ""}`;
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
      user={{ email: viewer.user.email, name: profile?.owner_name || null }}
      active="/console/logs"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Logs</span>
      }
    >
      <PageHeader
        eyebrow="Workspace"
        title="Request logs"
        description="Every request through your workspace: tokens, credits, status, and the reservation lifecycle behind each charge."
      />

      <div className="mb-4 flex flex-col gap-4">
        <LogsFilters state={state} models={models} keys={keys} />
      </div>

      <div className="mb-4 flex items-center justify-between gap-3">
        <p className="text-xs text-[var(--color-ink-3)]">
          Showing {events.length} most recent{state.window ? ` within ${state.window}` : ""}
        </p>
        <UsageLogsCsv rows={events} keyNames={keyNames} />
      </div>

      {fetchError ? (
        <div
          role="alert"
          className="rounded-md border border-[var(--color-danger)]/30 bg-[var(--color-danger-soft)] px-4 py-3 text-sm text-[var(--color-danger)]"
        >
          Unable to load request logs. Refresh to try again.
        </div>
      ) : firstRun ? (
        <EmptyFirstRun />
      ) : (
        <>
          <UsageLogsHistogram rows={events} />
          <UsageLogsTable rows={events} keyNames={keyNames} />
          {(cursor || nextCursor) ? (
            <div className="mt-4 flex items-center gap-2">
              {cursor ? (
                <Link href={buildPageUrl(null)} className={buttonVariants({ variant: "secondary", size: "sm" })}>
                  Reset
                </Link>
              ) : null}
              {nextCursor ? (
                <Link href={buildPageUrl(nextCursor)} className={buttonVariants({ variant: "secondary", size: "sm" })}>
                  Next page
                </Link>
              ) : null}
            </div>
          ) : null}
        </>
      )}
    </ConsoleShell>
  );
}

function EmptyFirstRun() {
  return (
    <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-[var(--color-border)] px-6 py-12 text-center">
      <p className="text-sm font-semibold text-[var(--color-ink)]">No requests yet</p>
      <p className="max-w-sm text-xs leading-relaxed text-[var(--color-ink-3)]">
        Requests appear here once traffic flows through your workspace. Create an API key under Build, point your app at the gateway, and each request lands in this log with its token counts and credit charges.
      </p>
      <Link href="/console/api-keys" className={cn(buttonVariants({ variant: "secondary", size: "sm" }), "mt-3")}>
        Create an API key
      </Link>
    </div>
  );
}
