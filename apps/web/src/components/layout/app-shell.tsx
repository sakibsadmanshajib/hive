import type { ReactNode } from "react";

import { AppHeader } from "./app-header";
import { AppSidebar } from "./app-sidebar";

export function AppShell({ children }: { children: ReactNode }) {
  return (
    <div
      className="min-h-screen bg-[linear-gradient(145deg,hsl(var(--background))_0%,hsl(41_30%_95%)_45%,hsl(198_32%_95%)_100%)]"
      data-app-shell="editorial"
    >
      <AppHeader />
      <div className="container grid min-h-[calc(100vh-3.5rem)] grid-cols-1 gap-6 py-6 md:grid-cols-[240px_1fr] xl:gap-8 xl:py-8">
        <aside className="hidden rounded-2xl border border-white/70 bg-card/85 p-3 shadow-[0_18px_40px_-30px_rgba(15,23,42,0.6)] backdrop-blur md:sticky md:top-20 md:block md:h-fit">
          <AppSidebar />
        </aside>
        <main className="pb-8">{children}</main>
      </div>
    </div>
  );
}
