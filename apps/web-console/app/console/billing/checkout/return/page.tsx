import Link from "next/link";
import { redirect, unstable_rethrow } from "next/navigation";

import {
  getCheckoutIntent,
  ControlPlaneError,
  type CheckoutIntent,
} from "@/lib/control-plane/client";
import {
  requireViewer,
  requireAccountProfile,
  tolerate,
} from "@/lib/console/data";
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
  const viewer = await requireViewer();
  if (viewer.user.email_verified === false) {
    redirect("/console/settings/profile");
  }

  const params = await searchParams;
  const intentId = parseIntentId(params.intent);
  const hint = parseReturnHint(params.hint);

  const profile = await requireAccountProfile();

  // Three outcomes, and only two of them are "we could not find that
  // purchase". Someone reading this page has just paid, so telling them their
  // purchase does not exist because the payment service was unreachable is
  // the worst answer this console can give (issue #494). 404 and 403 stay
  // deliberately indistinguishable from each other, so the page still cannot
  // be used to probe for intent ids belonging to other accounts.
  let intent: CheckoutIntent | null = null;
  let statusUnreadable = false;
  if (intentId) {
    try {
      intent = await getCheckoutIntent(intentId);
    } catch (error) {
      unstable_rethrow(error);
      const refused =
        error instanceof ControlPlaneError &&
        (error.status === 404 || error.status === 403);
      statusUnreadable = !refused;
      console.error(
        "CheckoutReturnPage: could not load the checkout intent",
        error,
      );
    }
  }

  return (
    <ConsoleShell
      workspace={{
        id: viewer.current_account.id,
        name: viewer.current_account.display_name,
        slug: viewer.current_account.slug,
      }}
      memberships={viewer.memberships}
      viewer={viewer}
      user={{ email: viewer.user.email, name: profile?.owner_name || null }}
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
      ) : statusUnreadable ? (
        <Card>
          <CardContent className="flex flex-col gap-4 px-6 py-7">
            <h1 className="font-display text-xl text-[var(--color-ink)]">
              We could not check your purchase
            </h1>
            <p className="text-sm text-[var(--color-ink-2)]">
              This is a problem reading the payment status, not a statement
              about your payment. If it went through, the credits appear on the
              billing page once it is confirmed. Refresh to try again.
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
