"use client";

import { useState, useEffect, type MouseEvent } from "react";
import { Minus, Plus, X } from "lucide-react";

import type {
  CheckoutOptions,
  CheckoutRail,
} from "@/lib/control-plane/client";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";

// REGULATORY: never display USD equivalents, FX rates, or any conversion
// language to BD accounts. The total is rendered in the rail's local
// currency only.
export function formatPrice(amountCents: number, currency: string): string {
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency,
    minimumFractionDigits: 2,
  }).format(amountCents / 100);
}

interface CheckoutModalProps {
  accountCountryCode: string;
  onClose: () => void;
}

interface CheckoutInitiateBody {
  redirect_url?: string;
}

function readRedirectUrl(value: unknown): string | null {
  if (value === null || typeof value !== "object") return null;
  const candidate = value as CheckoutInitiateBody;
  return typeof candidate.redirect_url === "string"
    ? candidate.redirect_url
    : null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object";
}

function isCheckoutOptions(value: unknown): value is CheckoutOptions {
  if (!isRecord(value)) return false;
  // The isRecord narrowing types `value` as a structural object, so
  // `value.rails` access is type-safe without a widening cast.
  return Array.isArray(value.rails);
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

  // Whether to render the pre-checkout USD estimate. BD accounts must
  // never see USD or any FX conversion language (regulatory rule); the
  // hosted checkout page (Stripe / bKash / SSLCommerz) is the only
  // place a BD user sees the BDT total, computed server-side at
  // initiate time. For non-BD accounts USD is the rail currency, so an
  // estimate is fine.
  const isBdAccount = accountCountryCode === "BD";

  function computeAmountUsdCents(): number {
    if (!options) return 0;
    return Math.round(creditAmount * options.price_per_credit_usd * 100);
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

  const increment = options?.credit_increment ?? 1000;
  const minCredits = options?.min_credits ?? 1000;
  const maxCredits = options?.max_credits ?? 100_000;

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

        {options ? (
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
                    className="font-display text-lg tabular-nums text-[var(--color-ink)]"
                    data-numeric
                  >
                    {formatPrice(computeAmountUsdCents(), "USD")}
                  </span>
                )}
              </div>
            ) : null}

            {checkoutError ? (
              <p role="alert" className="text-sm text-[var(--color-danger)]">
                {checkoutError}
              </p>
            ) : null}

            <div className="flex items-center justify-end gap-2">
              <Button
                type="button"
                variant="ghost"
                size="md"
                onClick={onClose}
              >
                Keep balance
              </Button>
              <Button
                type="button"
                variant="accent"
                size="md"
                onClick={() => void handleCheckout()}
                disabled={loading || !selectedRail}
              >
                {loading ? "Loading…" : "Continue to payment"}
              </Button>
            </div>
          </>
        ) : null}
      </div>
    </div>
  );
}
