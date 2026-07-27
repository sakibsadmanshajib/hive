import type { ReactNode } from "react";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import {
  getViewer,
  getBalance,
  getBudgetThreshold,
} from "@/lib/control-plane/client";
import { readTenantIdClaim } from "@/lib/auth/tenant-claim";
import { createClient } from "@/lib/supabase/server";
import { VerificationBanner } from "@/components/verification-banner";
import { BudgetAlertBanner } from "@/components/billing/budget-alert-banner";
import { WorkspaceSwitcher } from "@/components/workspace-switcher";

interface ConsoleLayoutProps {
  children: ReactNode;
}

// Layout-level concern: the workspace verification + budget banners. The
// page-level <ConsoleShell/> takes care of sidebar + topbar + content
// composition so each page picks its own `active` route. The legacy
// <WorkspaceSwitcher/> is rendered visually hidden so the existing
// /console/account-switch POST flow (and the e2e suite that drives it
// via `select[name='account_id']`) keeps working while the redesigned
// shell carries its own visible switcher button.
export default async function ConsoleLayout({ children }: ConsoleLayoutProps) {
  const viewer = await getViewer();

  // A signed-in user can legitimately hold no tenant membership: the Supabase
  // access-token hook issues a valid token with no tenant_id claim rather than
  // failing sign-in. Billing accounts are auto-provisioned on first login, so
  // getViewer() above still succeeds; only the tenant scope can be missing.
  //
  // Hand that case to /console/provision rather than settling it here. This is
  // a layout, so it re-renders on every navigation, and a provisioning write
  // must not sit on a render path. The handler also needs to write cookies to
  // refresh the session, which a Server Component cannot do. Reading the claim
  // is free, so a tenanted user takes exactly the path they took before with no
  // extra request.
  const cookieStore = await cookies();
  const {
    data: { session },
  } = await createClient(cookieStore).auth.getSession();
  if (!readTenantIdClaim(session?.access_token)) {
    redirect("/console/provision");
  }

  const isUnverified = viewer.user.email_verified === false;

  const [balanceSummary, budgetThreshold] = isUnverified
    ? [null, null]
    : await Promise.allSettled([getBalance(), getBudgetThreshold()]);

  const currentBalance =
    balanceSummary?.status === "fulfilled"
      ? balanceSummary.value.available_credits
      : 0;
  const threshold =
    budgetThreshold?.status === "fulfilled" ? budgetThreshold.value : null;

  return (
    <>
      <VerificationBanner show={isUnverified} />
      {!isUnverified && (
        <BudgetAlertBanner
          threshold={threshold}
          currentBalance={currentBalance}
        />
      )}
      <div className="sr-only">
        <WorkspaceSwitcher
          memberships={viewer.memberships}
          currentAccount={viewer.current_account}
        />
      </div>
      {children}
    </>
  );
}
