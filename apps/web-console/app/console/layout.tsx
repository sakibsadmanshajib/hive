import type { ReactNode } from "react";

import {
  getViewer,
  getBalance,
  getBudgetThreshold,
} from "@/lib/control-plane/client";
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
