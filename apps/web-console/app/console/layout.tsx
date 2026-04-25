import type { ReactNode } from "react";

import {
  getViewer,
  getBalance,
  getBudgetThreshold,
} from "@/lib/control-plane/client";
import { VerificationBanner } from "@/components/verification-banner";
import { BudgetAlertBanner } from "@/components/billing/budget-alert-banner";

interface ConsoleLayoutProps {
  children: ReactNode;
}

// Layout-level concern: the workspace verification + budget banners. The
// page-level <ConsoleShell/> takes care of sidebar + topbar + content
// composition so each page picks its own `active` route.
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
      {children}
    </>
  );
}
