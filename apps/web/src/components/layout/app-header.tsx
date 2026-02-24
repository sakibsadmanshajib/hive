"use client";

import { ThemeToggle } from "./theme-toggle";

export function AppHeader() {
  return (
    <header className="sticky top-0 z-30 border-b border-slate-800 bg-slate-950/85 backdrop-blur">
      <div className="container flex h-14 items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <p className="text-sm font-semibold tracking-wide text-slate-400">BD AI Gateway</p>
        </div>
        <ThemeToggle />
      </div>
    </header>
  );
}
