import type { ReactNode } from "react";

import { AppHeader } from "./app-header";
import { AppSidebar } from "./app-sidebar";

export function AppShell({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,#fef6e4_0%,#f8fafc_40%,#f1f5f9_100%)]">
      <AppHeader />
      <div className="container grid min-h-[calc(100vh-3.5rem)] grid-cols-1 gap-6 py-6 md:grid-cols-[220px_1fr]">
        <aside className="hidden rounded-xl border bg-card p-3 md:block md:h-fit md:sticky md:top-20">
          <AppSidebar />
        </aside>
        <main>{children}</main>
      </div>
    </div>
  );
}
