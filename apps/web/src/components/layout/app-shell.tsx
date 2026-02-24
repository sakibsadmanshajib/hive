import type { ReactNode } from "react";

import { AppHeader } from "./app-header";

export function AppShell({ children }: { children: ReactNode }) {
  return (
    <div className="min-h-screen bg-[radial-gradient(circle_at_top,#0f172a_0%,#020617_55%,#020617_100%)] text-slate-100">
      <AppHeader />
      <main className="container py-4 sm:py-6">{children}</main>
    </div>
  );
}
