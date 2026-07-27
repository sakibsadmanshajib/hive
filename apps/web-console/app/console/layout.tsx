import type { ReactNode } from "react";
import { cookies } from "next/headers";

import {
  getViewer,
  getBalance,
  getBudgetThreshold,
  reconcileTenantMembership,
} from "@/lib/control-plane/client";
import { readTenantIdClaim } from "@/lib/auth/tenant-claim";
import { createClient } from "@/lib/supabase/server";
import { VerificationBanner } from "@/components/verification-banner";
import { BudgetAlertBanner } from "@/components/billing/budget-alert-banner";
import { NoWorkspaceState } from "@/components/console/no-workspace-state";
import { TenantClaimRefresh } from "@/components/console/tenant-claim-refresh";
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
  // Reconcile it once, and when no tenant matches the user, render the
  // no-workspace state instead of a console with nothing behind it.
  const cookieStore = await cookies();
  const {
    data: { session },
  } = await createClient(cookieStore).auth.getSession();
  const tenantId = readTenantIdClaim(session?.access_token);

  const provisionStatus = tenantId ? null : await reconcileTenantMembership();
  if (provisionStatus === "no_tenant") {
    return <NoWorkspaceState email={viewer.user.email} />;
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
      {provisionStatus === "provisioned" ? <TenantClaimRefresh /> : null}
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
