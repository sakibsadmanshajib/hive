import type { ReactNode } from "react";
import { getViewer, getBalance, getBudgetThreshold } from "@/lib/control-plane/client";
import { WorkspaceSwitcher } from "@/components/workspace-switcher";
import { VerificationBanner } from "@/components/verification-banner";
import { BudgetAlertBanner } from "@/components/billing/budget-alert-banner";

interface ConsoleLayoutProps {
  children: ReactNode;
}

export default async function ConsoleLayout({ children }: ConsoleLayoutProps) {
  const viewer = await getViewer();
  const isUnverified = viewer.user.email_verified === false;

  const [balanceSummary, budgetThreshold] = isUnverified
    ? [null, null]
    : await Promise.allSettled([
        getBalance(),
        getBudgetThreshold(),
      ]);

  const currentBalance =
    balanceSummary?.status === "fulfilled" ? balanceSummary.value.available_credits : 0;
  const threshold =
    budgetThreshold?.status === "fulfilled" ? budgetThreshold.value : null;

  return (
    <div>
      <VerificationBanner show={isUnverified} />
      {!isUnverified && (
        <BudgetAlertBanner threshold={threshold} currentBalance={currentBalance} />
      )}
      <div style={{ display: "flex", minHeight: "100vh" }}>
        <nav
          style={{
            width: "240px",
            borderRight: "1px solid #e5e7eb",
            padding: "1.5rem 0",
            display: "flex",
            flexDirection: "column",
          }}
        >
          <WorkspaceSwitcher
            memberships={viewer.memberships}
            currentAccount={viewer.current_account}
          />
          <div style={{ padding: "1rem", display: "flex", flexDirection: "column", gap: "0.25rem" }}>
            <a href="/console" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
              Dashboard
            </a>
            <a href="/console/settings/profile" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
              Profile Settings
            </a>
            {!isUnverified && (
              <>
                <a href="/console/members" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
                  Members
                </a>
                <a href="/console/billing" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
                  Billing
                </a>
                <a href="/console/api-keys" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
                  API Keys
                </a>
                <a href="/console/analytics" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
                  Analytics
                </a>
                <a href="/console/catalog" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
                  Model Catalog
                </a>
              </>
            )}
          </div>
        </nav>
        <main style={{ flex: 1, padding: "2rem" }}>{children}</main>
      </div>
    </div>
  );
}
