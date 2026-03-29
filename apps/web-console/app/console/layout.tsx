import type { ReactNode } from "react";
import { getViewer } from "@/lib/control-plane/client";
import { WorkspaceSwitcher } from "@/components/workspace-switcher";
import { VerificationBanner } from "@/components/verification-banner";

interface ConsoleLayoutProps {
  children: ReactNode;
}

export default async function ConsoleLayout({ children }: ConsoleLayoutProps) {
  const viewer = await getViewer();
  const isUnverified = viewer.user.email_verified === false;

  return (
    <div>
      <VerificationBanner show={isUnverified} />
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
            <a href="/console/members" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
              Members
            </a>
          </div>
        </nav>
        <main style={{ flex: 1, padding: "2rem" }}>{children}</main>
      </div>
    </div>
  );
}
