import Link from "next/link";
import type { ReactNode } from "react";

interface NavShellProps {
  children: ReactNode;
}

export function NavShell({ children }: NavShellProps) {
  return (
    <div style={{ display: "flex", minHeight: "100vh" }}>
      <nav
        style={{
          width: "240px",
          borderRight: "1px solid #e5e7eb",
          padding: "1.5rem 1rem",
          display: "flex",
          flexDirection: "column",
          gap: "0.5rem",
        }}
      >
        <div style={{ fontWeight: 700, fontSize: "1.125rem", marginBottom: "1rem" }}>
          Hive
        </div>
        <Link href="/console" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
          Dashboard
        </Link>
        <Link href="/console/members" style={{ padding: "0.5rem", textDecoration: "none", color: "inherit" }}>
          Members
        </Link>
      </nav>
      <main style={{ flex: 1, padding: "2rem" }}>{children}</main>
    </div>
  );
}
