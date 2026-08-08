import type { ReactNode } from "react";
import Link from "next/link";
import {
  ArrowRight,
  AlertTriangle,
  BarChart3,
  Boxes,
  ChevronRight,
  KeyRound,
  Wallet,
} from "lucide-react";

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

/*
 * Next steps shown under the metric row. Every entry is a real console route,
 * deliberately: the Overview of a quiet workspace has nothing else true to
 * say, and inventing placeholder charts or fake activity to occupy the space
 * would be worse than the emptiness it replaces.
 */
const NEXT_STEPS: ReadonlyArray<{
  href: string;
  label: string;
  description: string;
  icon: ReactNode;
}> = [
  {
    href: "/console/api-keys",
    label: "Create an API key",
    description: "Mint a scoped key and start sending requests.",
    icon: <KeyRound size={15} aria-hidden="true" />,
  },
  {
    href: "/console/billing",
    label: "Top up credits",
    description: "Add prepaid credits so requests keep clearing.",
    icon: <Wallet size={15} aria-hidden="true" />,
  },
  {
    href: "/console/catalog",
    label: "Browse the model catalog",
    description: "See which models and aliases this workspace can route to.",
    icon: <Boxes size={15} aria-hidden="true" />,
  },
  {
    href: "/console/analytics",
    label: "Open analytics",
    description: "Break usage down by key, model, and day.",
    icon: <BarChart3 size={15} aria-hidden="true" />,
  },
];

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
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active="/console"
      topbar={
        <span className="font-medium text-[var(--color-ink-2)]">Overview</span>
      }
    >
      {/*
        Overview declares its own measure rather than filling the shell's
        wider table-oriented column. Three metric cards stretched across a
        1900px monitor left a stretched band of content above an unbounded
        void, which read as a page that had failed to load rather than a calm
        one (live UI/UX pass, 2026-07-26). A ~1080px column plus the closing
        bands below give the composition a start, a middle, and an end.
      */}
      <div className="mx-auto w-full max-w-[1080px]">
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
                    className="metric text-3xl text-[var(--color-ink)]"
                    data-numeric
                  >
                    {formatCredits(balance.available_credits)}
                  </p>
                  <p className="text-xs text-[var(--color-ink-3)]">
                    Posted{" "}
                    <span className="metric text-[var(--color-ink-2)]">
                      {formatCredits(balance.posted_credits)}
                    </span>{" "}
                    · Reserved{" "}
                    <span className="metric text-[var(--color-ink-2)]">
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
                  className="metric text-3xl text-[var(--color-ink)]"
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
                    className="metric text-2xl text-[var(--color-ink-3)]"
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

        <section
          aria-labelledby="next-steps-heading"
          className="mt-10 flex flex-col gap-3"
        >
          <h2
            id="next-steps-heading"
            className="text-2xs font-medium uppercase tracking-[0.14em] text-[var(--color-ink-3)]"
          >
            Next steps
          </h2>
          <div className="grid gap-3 sm:grid-cols-2">
            {NEXT_STEPS.map((step) => (
              <Link
                key={step.href}
                href={step.href}
                className={[
                  "group flex items-center gap-3.5 rounded-lg px-4 py-3.5",
                  "border border-[var(--color-border)] bg-[var(--color-surface)]",
                  "shadow-[var(--shadow-xs)]",
                  "transition-colors duration-[var(--duration-fast)]",
                  "hover:border-[var(--color-border-strong)] hover:bg-[var(--color-surface-2)]",
                ].join(" ")}
              >
                <span className="grid h-8 w-8 shrink-0 place-items-center rounded-md bg-[var(--color-surface-inset)] text-[var(--color-ink-3)] transition-colors duration-[var(--duration-fast)] group-hover:text-[var(--color-accent)]">
                  {step.icon}
                </span>
                <span className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-sm font-medium text-[var(--color-ink)]">
                    {step.label}
                  </span>
                  <span className="text-xs text-[var(--color-ink-3)]">
                    {step.description}
                  </span>
                </span>
                <ChevronRight
                  size={14}
                  aria-hidden="true"
                  className="ml-auto shrink-0 text-[var(--color-ink-4)] transition-colors duration-[var(--duration-fast)] group-hover:text-[var(--color-ink-2)]"
                />
              </Link>
            ))}
          </div>
        </section>

        {/*
          Terminal rule. A bounded end to the column is what stops the space
          below it reading as a failure to render.
        */}
        <div className="mt-10 border-t border-[var(--color-border)] pt-4 text-2xs text-[var(--color-ink-3)]">
          Need the API reference?{" "}
          <Link
            href="https://hivegpt.io"
            className="text-[var(--color-ink-2)] underline-offset-4 hover:text-[var(--color-ink)] hover:underline"
          >
            Read the docs
          </Link>
        </div>
      </div>
    </ConsoleShell>
  );
}
