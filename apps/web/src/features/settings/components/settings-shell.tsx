import type { ReactNode } from "react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../../components/ui/card";

type SettingsShellProps = {
  children: ReactNode;
  loading: boolean;
  status: string;
};

export function SettingsShell({ children, loading, status }: SettingsShellProps) {
  return (
    <section className="mx-auto flex w-full max-w-6xl flex-col gap-5">
      <Card className="border border-slate-200/80 bg-gradient-to-r from-amber-100/60 via-background to-teal-100/60 shadow-sm dark:border-slate-800/60 dark:from-amber-950/30 dark:to-teal-950/30">
        <CardHeader className="space-y-2">
          <CardTitle className="text-3xl">Settings</CardTitle>
          <CardDescription>Manage profile details, billing workflows, and account-level access preferences.</CardDescription>
        </CardHeader>
        <CardContent>
          <p aria-live="polite" className="text-sm text-muted-foreground">
            {loading ? "Applying changes..." : status}
          </p>
        </CardContent>
      </Card>
      {children}
    </section>
  );
}
