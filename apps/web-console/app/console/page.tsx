import Link from "next/link";
import { ArrowRight, AlertTriangle } from "lucide-react";

import {
  getAccountProfile,
  getBalance,
  getViewer,
} from "@/lib/control-plane/client";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { buttonVariants } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { PageHeader } from "@/components/ui/page-header";
import { formatCredits } from "@/lib/format/credits";

export default async function ConsolePage() {
  const viewer = await getViewer();
  const profile = await getAccountProfile();
  const isUnverified = viewer.user.email_verified === false;
  const needsSetup = profile.profile_setup_complete === false;

  const balance = isUnverified
    ? null
    : await getBalance().catch((): null => null);

  return (
    <ConsoleShell
      workspace={{
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Overview</span>
      }
    >
      <PageHeader
        eyebrow="Workspace:"
        title={viewer.current_account.display_name}
        description="Track credits, recent usage, and the health of your workspace at a glance."
      />

      {needsSetup ? (
        <div
          role="status"
          className="mb-6 flex flex-col gap-3 rounded-lg border border-[var(--color-accent)]/30 bg-[var(--color-accent-soft)] px-5 py-4 sm:flex-row sm:items-center sm:justify-between"
        >
          <div className="flex flex-col gap-1">
            <p className="text-sm font-semibold text-[var(--color-ink)]">
              Complete your workspace profile
            </p>
            <p className="text-xs text-[var(--color-ink-3)]">
              Finish the owner, account and location basics to remove this
              reminder. Billing details stay optional until later.
            </p>
          </div>
          <Link
            href="/console/setup"
            className={buttonVariants({ variant: "accent", size: "sm" })}
          >
            Complete setup
            <ArrowRight size={14} aria-hidden="true" />
          </Link>
        </div>
      ) : null}

      <section className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Card>
          <CardHeader>
            <CardTitle>Available credits</CardTitle>
            <CardDescription>
              Updated continuously across all running requests.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-5">
            {balance ? (
              <div className="flex flex-col gap-1">
                <p
                  className="font-display text-3xl tabular-nums text-[var(--color-ink)]"
                  data-numeric
                >
                  {formatCredits(balance.available_credits)}
                </p>
                <p className="text-xs text-[var(--color-ink-3)] tabular-nums">
                  Posted{" "}
                  <span className="text-[var(--color-ink-2)]">
                    {formatCredits(balance.posted_credits)}
                  </span>{" "}
                  · Reserved{" "}
                  <span className="text-[var(--color-ink-2)]">
                    {formatCredits(balance.reserved_credits)}
                  </span>
                </p>
              </div>
            ) : (
              <p className="text-sm text-[var(--color-ink-3)]">
                Verify your email to view balances.
              </p>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Today&rsquo;s activity</CardTitle>
            <CardDescription>
              Snapshot of requests served and tokens processed.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-5">
            <div className="flex flex-col gap-1">
              <p
                className="font-display text-3xl tabular-nums text-[var(--color-ink)]"
                data-numeric
              >
                —
              </p>
              <p className="text-xs text-[var(--color-ink-3)]">
                Detailed counts available in{" "}
                <Link
                  href="/console/analytics"
                  className="text-[var(--color-accent)] underline-offset-4 hover:underline"
                >
                  Analytics
                </Link>
                .
              </p>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recent errors</CardTitle>
            <CardDescription>
              Failures across keys in the last 24 hours.
            </CardDescription>
          </CardHeader>
          <CardContent className="px-5 py-5">
            <div className="flex items-center gap-3">
              <span className="grid h-9 w-9 place-items-center rounded-full bg-[var(--color-surface-inset)] text-[var(--color-ink-3)]">
                <AlertTriangle size={16} aria-hidden="true" />
              </span>
              <div className="flex flex-col">
                <p
                  className="font-display text-2xl tabular-nums text-[var(--color-ink-3)]"
                  data-numeric
                >
                  —
                </p>
                <p className="text-xs text-[var(--color-ink-3)]">
                  Error telemetry not yet wired up. View detailed failures in
                  Analytics.
                </p>
              </div>
            </div>
          </CardContent>
        </Card>
      </section>
    </ConsoleShell>
  );
}
