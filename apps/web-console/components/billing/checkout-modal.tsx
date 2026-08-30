"use client";

import { useState, useEffect, type MouseEvent } from "react";
import { Minus, Plus, X } from "lucide-react";

import type {
  CheckoutOptions,
  CheckoutRail,
} from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { formatCurrency } from "@/lib/format/money";

interface CheckoutModalProps {
  accountCountryCode: string;
  onClose: () => void;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object";
}

function readRedirectUrl(value: unknown): string | null {
  if (!isRecord(value)) return null;
  return typeof value.redirect_url === "string" ? value.redirect_url : null;
}

function isCheckoutOptions(value: unknown): value is CheckoutOptions {
  if (!isRecord(value)) return false;
  // The isRecord narrowing types `value` as a structural object, so
  // `value.rails` access is type-safe without a widening cast.
  if (!Array.isArray(value.rails)) return false;
  // The three purchase bounds must be present and finite, not defaulted later.
  // This modal fetches the rails endpoint directly and never goes through
  // getCheckoutRails, so the decoder's own coherence guard does not cover it:
  // an absent min_credits used to survive as the `?? 10_000_000` fallback
  // below, which was a stale second copy of a money floor once the real one
  // moved to 1.00 USD (issue #1450). Accepting the payload and then inventing
  // the missing bound is how issue #1386 rendered a 1.00 USD ceiling against a
  // real 100.00 USD one with nothing complaining.
  //
  // Absent bounds are tolerated only where nothing is purchasable, which is the
  // control plane's documented answer for a deployment with no rail
  // credentials: there is no amount field to render, so there is no bound to be
  // wrong about.
  const purchasable = value.rails.some(
    (rail) => isRecord(rail) && rail.enabled === true,
  );
  if (!purchasable) return true;
  return (
    ["min_credits", "max_credits", "credit_increment"] as const
  ).every((field) => Number.isFinite(value[field]));
}

// computeBlockSplitAmountMinor prices `credits` at `pricePerBlockMinor`
// minor units per `creditBlockSize` credits, floor-rounded, WITHOUT forming
// the raw product: at the current unit a max-size purchase would multiply to
// ~7.5e16, past Number.MAX_SAFE_INTEGER. Whole blocks are priced as an exact
// Number product (block quotient x price stays far below 2^53 for every real
// currency rate); the sub-block remainder goes through BigInt, whose floor
// division is exact for any inputs. Exported so tests exercise THIS code
// rather than a copy of it.
export function computeBlockSplitAmountMinor(
  credits: number,
  creditBlockSize: number,
  pricePerBlockMinor: number,
): number {
  if (
    !Number.isFinite(credits) ||
    !Number.isFinite(creditBlockSize) ||
    creditBlockSize <= 0 ||
    !Number.isFinite(pricePerBlockMinor)
  ) {
    // NaN comparisons return false for `<= 0`, so a pathological upstream
    // value would otherwise reach the division and render as NaN.
    return 0;
  }
  const wholeBlocks = Math.trunc(credits / creditBlockSize);
  const remainderMinor =
    (BigInt(credits - wholeBlocks * creditBlockSize) * BigInt(pricePerBlockMinor)) /
    BigInt(creditBlockSize);
  return wholeBlocks * pricePerBlockMinor + Number(remainderMinor);
}

export function CheckoutModal({
  accountCountryCode,
  onClose,
}: CheckoutModalProps) {
  const [options, setOptions] = useState<CheckoutOptions | null>(null);
  const [selectedRail, setSelectedRail] = useState<string>("");
  const [creditAmount, setCreditAmount] = useState<number>(1000);
  const [loading, setLoading] = useState(false);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [checkoutError, setCheckoutError] = useState<string | null>(null);

  useEffect(() => {
    async function fetchRails() {
      try {
        const response = await fetch(
          "/api/v1/accounts/current/checkout/rails",
          { credentials: "include" },
        );
        if (!response.ok) {
          setFetchError("Unable to load payment options. Please refresh.");
          return;
        }
        const data: unknown = await response.json();
        if (isCheckoutOptions(data)) {
          setOptions(data);
          const enabledRails = data.rails.filter((r) => r.enabled);
          if (enabledRails.length > 0) {
            setSelectedRail(enabledRails[0].rail);
          }
          if (data.min_credits) {
            setCreditAmount(data.min_credits);
          }
        }
      } catch (err: unknown) {
        const message =
          err instanceof Error
            ? err.message
            : "Unable to load payment options. Please refresh.";
        setFetchError(message);
      }
    }

    void fetchRails();
  }, []);

  const selectedRailData: CheckoutRail | undefined = options?.rails.find(
    (r) => r.rail === selectedRail,
  );

  // Whether to render a pre-checkout estimate. BD accounts must never
  // see any FX conversion language or non-local currency total
  // (regulatory rule); the hosted checkout page (Stripe / bKash /
  // SSLCommerz) is the only place a BD user sees the BDT total,
  // computed server-side at initiate time. For non-BD accounts the
  // estimate is rendered in the resolved options.currency.
  const isBdAccount = accountCountryCode === "BD";

  // FX-17-04 (post-review): the server prices in minor units per
  // `credit_block_size` credits (= CreditsPerUSD = 1,000,000,000 since the
  // 2026-08-23 credit unit rescale). To get the localised total for an
  // arbitrary credit count we integer-divide by the block size, matching the
  // server-side math/big truncation.
  //
  function computeAmountMinor(): number {
    if (
      !options ||
      !Number.isFinite(options.credit_block_size) ||
      options.credit_block_size <= 0 ||
      !Number.isFinite(options.price_per_block_minor)
    ) {
      return 0;
    }
    return computeBlockSplitAmountMinor(
      creditAmount,
      options.credit_block_size,
      options.price_per_block_minor,
    );
  }

  async function handleCheckout() {
    if (!selectedRail || !options) return;

    setLoading(true);
    setCheckoutError(null);

    try {
      const idempotencyKey = `checkout-${Date.now()}-${Math.random()
        .toString(36)
        .slice(2)}`;
      const response = await fetch(
        "/api/v1/accounts/current/checkout/initiate",
        {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            rail: selectedRail,
            credits: creditAmount,
            idempotency_key: idempotencyKey,
          }),
        },
      );

      if (!response.ok) {
        setCheckoutError("Unable to start checkout. Please try again.");
        setLoading(false);
        return;
      }

      const data: unknown = await response.json();
      const redirect = readRedirectUrl(data);
      if (redirect) {
        window.location.href = redirect;
      } else {
        setCheckoutError("Unable to start checkout. Please try again.");
        setLoading(false);
      }
    } catch (err: unknown) {
      const message =
        err instanceof Error
          ? err.message
          : "Unable to start checkout. Please try again.";
      setCheckoutError(message);
      setLoading(false);
    }
  }

  function handleOverlayClick(e: MouseEvent<HTMLDivElement>) {
    if (e.target === e.currentTarget) {
      onClose();
    }
  }

  // Fallbacks are whole one-cent steps at the current credit unit
  // (1 USD = 1e9 credits since the 2026-08-23 rescale); the server normally
  // supplies all three.
  const increment = options?.credit_increment ?? 10_000_000;
  const minCredits = options?.min_credits ?? 10_000_000;
  const maxCredits = options?.max_credits ?? 1_000_000_000;

  // A deployment with no rail credentials registered returns every rail
  // disabled, and with it a purchase ceiling of 0 (the control plane's
  // documented "nothing is selectable" answer). Rendering the purchase form
  // anyway produced the live defect: an empty Payment method fieldset, a
  // permanently disabled Continue button that explained nothing, and
  // min="10000000" max="0" on the amount input.
  //
  // The range check is defence in depth. getCheckoutRails already refuses an
  // incoherent range whenever a rail is selectable, so this branch is what
  // keeps an invalid range out of the DOM if the modal is ever fed one
  // directly.
  // The step is part of the same predicate, not a separate concern: a zero or
  // absent `credit_increment` survives the `??` above as a literal 0, renders
  // as step="0", and freezes both stepper buttons on an amount the payer
  // cannot change. A field nobody can move is the same dead control this
  // change exists to remove.
  const selectableRails = options?.rails.filter((rail) => rail.enabled) ?? [];
  const boundsAreCoherent =
    Number.isFinite(minCredits) &&
    minCredits > 0 &&
    Number.isFinite(maxCredits) &&
    maxCredits >= minCredits &&
    Number.isFinite(increment) &&
    increment > 0;
  const canPurchase = selectableRails.length > 0 && boundsAreCoherent;
  // Both messages name the state and then the next move. A dead end that only
  // says it is a dead end still leaves the user guessing, which is the
  // complaint this change exists to answer.
  const unavailableReason =
    selectableRails.length === 0
      ? "No payment method is available for this account yet, so credits cannot be bought here. Your balance and your existing API keys are unaffected. Contact support to have a payment method enabled for this account."
      : "The payment options for this account came back unusable, so credits cannot be bought here right now. Your balance and your existing API keys are unaffected. Refresh in a moment, and contact support if it keeps happening.";

  function decrementAmount() {
    setCreditAmount((prev) => Math.max(minCredits, prev - increment));
  }

  function incrementAmount() {
    setCreditAmount((prev) => Math.min(maxCredits, prev + increment));
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-[var(--color-ink)]/40 px-4"
      onClick={handleOverlayClick}
      role="dialog"
      aria-modal="true"
      aria-labelledby="checkout-title"
    >
      <div className="flex w-full max-w-md flex-col gap-5 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface)] p-6 shadow-[var(--shadow-md)]">
        <div className="flex items-center justify-between">
          <h2
            id="checkout-title"
            className="font-display text-xl text-[var(--color-ink)]"
          >
            Buy credits
          </h2>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Close"
            onClick={onClose}
          >
            <X size={16} aria-hidden="true" />
          </Button>
        </div>

        {fetchError ? (
          <p role="alert" className="text-sm text-[var(--color-danger)]">
            {fetchError}
          </p>
        ) : null}

        {!options && !fetchError ? (
          <p className="text-sm text-[var(--color-ink-3)]">
            Loading payment options…
          </p>
        ) : null}

        {options && !canPurchase ? (
          <p
            role="status"
            className="rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-4 py-3 text-sm text-[var(--color-ink-2)]"
          >
            {unavailableReason}
          </p>
        ) : null}

        {options && canPurchase ? (
          <>
            <fieldset className="flex flex-col gap-2">
              <legend className="mb-1 text-xs font-medium text-[var(--color-ink-2)]">
                Payment method
              </legend>
              <div className="flex flex-col gap-2">
                {options.rails
                  .filter((r) => r.enabled)
                  .map((rail) => {
                    const isActive = selectedRail === rail.rail;
                    return (
                      <label
                        key={rail.rail}
                        className={`flex cursor-pointer items-center gap-3 rounded-md border px-3 py-2 transition-colors ${
                          isActive
                            ? "border-[var(--color-accent)] bg-[var(--color-accent-soft)]"
                            : "border-[var(--color-border)] bg-[var(--color-surface)] hover:border-[var(--color-border-strong)]"
                        }`}
                      >
                        <input
                          type="radio"
                          name="rail"
                          value={rail.rail}
                          checked={isActive}
                          onChange={() => setSelectedRail(rail.rail)}
                          className="accent-[var(--color-accent)]"
                        />
                        <span className="text-sm text-[var(--color-ink)]">
                          {rail.label}
                        </span>
                      </label>
                    );
                  })}
              </div>
            </fieldset>

            <Field label="Credits to purchase" htmlFor="credit-amount">
              <div className="flex items-center gap-2">
                <Button
                  type="button"
                  variant="secondary"
                  size="icon"
                  onClick={decrementAmount}
                  disabled={creditAmount <= minCredits}
                  aria-label="Decrease"
                >
                  <Minus size={14} aria-hidden="true" />
                </Button>
                <Input
                  id="credit-amount"
                  type="number"
                  value={creditAmount}
                  min={minCredits}
                  max={maxCredits}
                  step={increment}
                  onChange={(e) => {
                    const val = Number(e.target.value);
                    if (!Number.isNaN(val)) {
                      setCreditAmount(
                        Math.max(minCredits, Math.min(maxCredits, val)),
                      );
                    }
                  }}
                  className="w-32 text-center tabular-nums"
                />
                <Button
                  type="button"
                  variant="secondary"
                  size="icon"
                  onClick={incrementAmount}
                  disabled={creditAmount >= maxCredits}
                  aria-label="Increase"
                >
                  <Plus size={14} aria-hidden="true" />
                </Button>
                <span className="text-xs text-[var(--color-ink-3)]">
                  credits
                </span>
              </div>
            </Field>

            {selectedRailData ? (
              <div className="flex items-center justify-between rounded-md border border-[var(--color-border)] bg-[var(--color-surface-inset)] px-4 py-3">
                <span className="text-xs text-[var(--color-ink-3)]">
                  {isBdAccount ? "Final amount" : "Total"}
                </span>
                {isBdAccount ? (
                  <span className="text-xs text-[var(--color-ink-3)]">
                    Shown on the {selectedRailData.label} payment page.
                  </span>
                ) : (
                  <span
                    className="metric text-lg text-[var(--color-ink)]"
                    data-numeric
                  >
                    {formatCurrency(computeAmountMinor(), options.currency)}
                  </span>
                )}
              </div>
            ) : null}

            {checkoutError ? (
              <p role="alert" className="text-sm text-[var(--color-danger)]">
                {checkoutError}
              </p>
            ) : null}
          </>
        ) : null}

        {options ? (
          <div className="flex items-center justify-end gap-2">
            <Button type="button" variant="ghost" size="md" onClick={onClose}>
              Keep balance
            </Button>
            {canPurchase ? (
              <Button
                type="button"
                variant="accent"
                size="md"
                onClick={() => void handleCheckout()}
                disabled={loading || !selectedRail}
              >
                {loading ? "Loading…" : "Continue to payment"}
              </Button>
            ) : null}
          </div>
        ) : null}
      </div>
    </div>
  );
}
