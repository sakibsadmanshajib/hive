"use client";

import { useRouter } from "next/navigation";

import { CheckoutModal } from "@/components/billing/checkout-modal";

/**
 * Mounts the checkout modal for `/console/billing?action=buy`.
 *
 * "Buy credits" has always been a `Link` to that URL
 * (`components/billing/billing-overview.tsx`), but nothing on the page read the
 * parameter and `CheckoutModal` had no import anywhere in the app, so the link
 * navigated to the same page and the modal never rendered. Issue #1386.
 *
 * Closing returns to the plain billing URL rather than calling `router.back()`,
 * so a customer who arrived by pasting the link, or who reloaded while the modal
 * was open, still lands on the billing page instead of leaving the console.
 */
export function CheckoutLauncher() {
  const router = useRouter();

  return (
    <CheckoutModal
      onClose={() => {
        router.replace("/console/billing");
      }}
    />
  );
}
