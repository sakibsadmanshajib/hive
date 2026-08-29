import Link from "next/link";
import { redirect } from "next/navigation";

import {
  getAccountProfile,
  getCheckoutIntent,
  getViewer,
} from "@/lib/control-plane/client";
import { CheckoutReturnStatus } from "@/components/billing/checkout-return-status";
import { ConsoleShell } from "@/components/app-shell/console-shell";
import { PageHeader } from "@/components/ui/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { buttonVariants } from "@/components/ui/button";
import {
  BILLING_PATH,
  parseIntentId,
  parseReturnHint,
} from "@/lib/payments/checkout-return";

interface CheckoutReturnPageProps {
  searchParams: Promise<{ intent?: string; rail?: string; hint?: string }>;
}

// The single page every payment rail returns a customer's browser to (issue
// #538). It exists because a paying customer used to land on a control-plane
// webhook that answered raw JSON, or on a Stripe route that existed nowhere.
//
// The outcome shown here comes from the payment intent record, read server-side
// through the account-scoped control-plane endpoint. Everything in the query
// string is attacker-controlled: `intent` only selects which of the caller's own
// intents to read, and `hint` only selects wording. Neither can produce a
// success, a failure, or a credit. Settlement stays with the provider webhook.
export default async function CheckoutReturnPage({ searchParams }: CheckoutReturnPageProps) {
  const viewer = await getViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const params = await searchParams;
  const intentId = parseIntentId(params.intent);
  const hint = parseReturnHint(params.hint);

  const profile = await getAccountProfile();
  const intent = intentId
    ? await getCheckoutIntent(intentId).catch((): null => null)
    : null;

  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      viewer={viewer}
      user={{ email: viewer.user.email, name: profile.owner_name || null }}
      active={BILLING_PATH}
      topbar={<span className="font-medium text-[var(--color-ink-2)]">Billing</span>}
    >
      <PageHeader
        eyebrow="Billing"
        title="Purchase"
        description="The result of your most recent credit purchase."
      />

      {intent ? (
        <CheckoutReturnStatus initial={intent} hint={hint} />
      ) : (
        // No readable intent: either the return carried no id, or the id does
        // not belong to this account. The control-plane reports both the same
        // way on purpose, so this page cannot be used to probe for intent ids.
        <Card>
          <CardContent className="flex flex-col gap-4 px-6 py-7">
            <h1 className="font-display text-xl text-[var(--color-ink)]">
              We could not find that purchase
            </h1>
            <p className="text-sm text-[var(--color-ink-2)]">
              Your balance and ledger on the billing page are the record of what
              actually happened. If a payment went through, the credits appear
              there once it is confirmed.
            </p>
            <div>
              <Link
                href={BILLING_PATH}
                className={buttonVariants({ variant: "accent", size: "md" })}
              >
                Back to billing
              </Link>
            </div>
          </CardContent>
        </Card>
      )}
    </ConsoleShell>
  );
}
