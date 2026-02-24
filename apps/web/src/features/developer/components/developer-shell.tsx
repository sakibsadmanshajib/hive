import type { ReactNode } from "react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../../components/ui/card";

type DeveloperShellProps = {
  children: ReactNode;
  loading: boolean;
  status: string;
};

export function DeveloperShell({ children, loading, status }: DeveloperShellProps) {
  return (
    <section className="mx-auto flex w-full max-w-6xl flex-col gap-5">
      <Card className="border border-slate-200/80 bg-gradient-to-r from-sky-100/60 via-background to-amber-100/60 shadow-sm dark:border-slate-800/60 dark:from-sky-950/40 dark:to-amber-950/30">
        <CardHeader className="space-y-2">
          <CardTitle className="text-3xl">Developer Panel</CardTitle>
          <CardDescription>Manage API keys, inspect usage signals, and review account-level developer diagnostics.</CardDescription>
        </CardHeader>
        <CardContent>
          <p aria-live="polite" className="text-sm text-muted-foreground">
            {loading ? "Syncing developer data..." : status}
          </p>
        </CardContent>
      </Card>
      {children}
    </section>
  );
}
