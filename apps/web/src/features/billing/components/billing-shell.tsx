import type { ReactNode } from "react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../../components/ui/card";

type BillingShellProps = {
  children: ReactNode;
  loading: boolean;
  status: string;
};

export function BillingShell({ children, loading, status }: BillingShellProps) {
  return (
    <section className="mx-auto flex w-full max-w-5xl flex-col gap-5">
      <Card className="border-none bg-gradient-to-r from-primary/15 via-background to-accent/10 shadow-none">
        <CardHeader className="space-y-2">
          <CardTitle className="text-3xl">Billing and usage</CardTitle>
          <CardDescription>Manage prepaid credits, payment intents, and keys for this account.</CardDescription>
        </CardHeader>
        <CardContent>
          <p aria-live="polite" className="text-sm text-muted-foreground">
            {loading ? "Working on your request..." : status}
          </p>
        </CardContent>
      </Card>
      {children}
    </section>
  );
}
