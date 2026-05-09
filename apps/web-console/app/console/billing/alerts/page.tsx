// Phase 14 FIX-14-26 — workspace spend-alerts page (BDT-only).
//
// Server component. Lists active spend alerts for the current workspace and
// renders the SpendAlertForm for owner-only creation. The form posts to the
// proxy /api/spend-alerts/{workspace_id}; backend rejects non-owners with 403.

import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getViewer,
  listSpendAlerts,
  type SpendAlert,
} from "@/lib/control-plane/client";
import { SpendAlertForm } from "@/components/billing/spend-alert-form";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

function formatTimestamp(value: string | null): string {
  if (!value) return "—";
  try {
    return new Date(value).toLocaleString("en-BD");
  } catch {
    return value;
  }
}

export default async function SpendAlertsPage() {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const profile = await getAccountProfile();
  const workspaceId = viewer.current_account.id;
  const isOwner = viewer.current_account.role === "owner";

  const alerts: SpendAlert[] = await listSpendAlerts(workspaceId).catch(
    (): SpendAlert[] => [],
  );

  const existingThresholds = alerts.map((a) => a.threshold_pct);

  return (
    <ConsoleShell
      workspace={{
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console/billing"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">
          Spend alerts
        </span>
      }
    >
      <PageHeader
        eyebrow="Workspace"
        title="Spend alerts"
        description="Configure email and webhook notifications when month-to-date spend reaches a percentage of your soft cap."
      />

      <div className="flex flex-col gap-6">
        <Card>
          <CardHeader>
            <CardTitle>Active alerts</CardTitle>
            <CardDescription>
              {alerts.length === 0
                ? "No spend alerts configured yet."
                : `${alerts.length} alert${alerts.length === 1 ? "" : "s"} active.`}
            </CardDescription>
          </CardHeader>
          {alerts.length > 0 ? (
            <CardContent className="px-5 py-5">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-[var(--color-border)] text-left text-xs uppercase text-[var(--color-ink-3)]">
                    <th className="px-3 py-2">Threshold</th>
                    <th className="px-3 py-2">Email</th>
                    <th className="px-3 py-2">Webhook</th>
                    <th className="px-3 py-2">Last fired</th>
                  </tr>
                </thead>
                <tbody>
                  {alerts.map((alert) => (
                    <tr
                      key={alert.id}
                      className="border-b border-[var(--color-border)]"
                    >
                      <td className="px-3 py-2 tabular-nums text-[var(--color-ink)]">
                        {alert.threshold_pct}%
                      </td>
                      <td className="px-3 py-2 text-[var(--color-ink)]">
                        {alert.email ?? "—"}
                      </td>
                      <td className="px-3 py-2 text-[var(--color-ink)]">
                        {alert.webhook_url ? "Configured" : "—"}
                      </td>
                      <td className="px-3 py-2 text-[var(--color-ink-3)]">
                        {formatTimestamp(alert.last_fired_at)}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </CardContent>
          ) : null}
        </Card>

        <SpendAlertForm
          workspaceId={workspaceId}
          readOnly={!isOwner}
          existingThresholds={existingThresholds}
        />
      </div>
    </ConsoleShell>
  );
}
