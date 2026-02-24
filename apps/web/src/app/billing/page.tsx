import Link from "next/link";

import { Button } from "../../components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";

export default function BillingPage() {
  return (
    <section className="mx-auto w-full max-w-3xl">
      <Card>
        <CardHeader>
          <CardTitle>Billing moved to Settings</CardTitle>
          <CardDescription>
            Billing and payment flows now live under Settings, while API keys and usage moved to Developer Panel.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-3">
          <Button asChild>
            <Link href="/settings">Open Settings</Link>
          </Button>
          <Button asChild variant="outline">
            <Link href="/developer">Open Developer Panel</Link>
          </Button>
        </CardContent>
      </Card>
    </section>
  );
}
