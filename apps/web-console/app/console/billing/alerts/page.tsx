// Phase 14 FIX-14-26 — workspace spend-alerts page (BDT-only).
//
// Server component. Lists active spend alerts for the current workspace and
// renders the SpendAlertForm for owner-only creation. The form posts to the
// proxy /api/spend-alerts/{workspace_id}; backend rejects non-owners with 403.

import { redirect } from "next/navigation";

import {
  listSpendAlerts,
  type SpendAlert,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
  tolerate,
} from "@/lib/console/data";
import { SpendAlertForm } from "@/components/billing/spend-alert-form";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { formatDateTime } from "@/lib/format/datetime";
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
    return formatDateTime(value);
  } catch {
    return value;
  }
}

export default async function SpendAlertsPage() {
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const profile = await requireAccountProfile();
  const workspaceId = viewer.current_account.id;
  const isOwner = viewer.current_account.role === "owner";

  // null when the list could not be read, which is not the same as an
  // account with no alerts. Collapsing the two claimed "No spend alerts
  // configured yet" about an account that may well have several, and it also
  // emptied existingThresholds, which is the only thing stopping the form
  // below from accepting a duplicate threshold. A validation that silently
  // stops validating is worse than one that says it cannot run (issue #494).
  const alerts: SpendAlert[] | null = await tolerate(
    listSpendAlerts(workspaceId),
  );

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
              {alerts === null
                ? "We could not read your spend alerts just now. Refresh to try again."
                : alerts.length === 0
                  ? "No spend alerts configured yet."
                  : `${alerts.length} alert${alerts.length === 1 ? "" : "s"} active.`}
            </CardDescription>
          </CardHeader>
          {alerts !== null && alerts.length > 0 ? (
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
                      <td className="metric px-3 py-2 text-[var(--color-ink)]">
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

        {/*
          Withheld while the existing alerts are unknown. The form's only
          duplicate check is existingThresholds, so offering it against an
          empty list we do not believe would let a customer create a second
          alert at a threshold they already have, under a UI that says it
          prevents exactly that.
        */}
        {alerts !== null ? (
          <SpendAlertForm
            workspaceId={workspaceId}
            readOnly={!isOwner}
            existingThresholds={alerts.map((a) => a.threshold_pct)}
          />
        ) : null}
      </div>
    </ConsoleShell>
  );
}
