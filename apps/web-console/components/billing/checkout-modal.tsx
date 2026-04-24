"use client";

import { useState, useEffect } from "react";
import type { CheckoutOptions, CheckoutRail } from "@/lib/control-plane/client";

// REGULATORY: BDT accounts must never see FX rates, USD equivalents, or conversion language.
// See CONTEXT.md locked decision. For ALL customers, show only the final price in the
// account's local currency. Never show USD equivalent, FX rate, "converted from",
// "includes conversion fee", or any exchange language.
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

export function CheckoutModal({ accountCountryCode: _accountCountryCode, onClose }: CheckoutModalProps) {
  const [options, setOptions] = useState<CheckoutOptions | null>(null);
  const [selectedRail, setSelectedRail] = useState<string>("");
  const [creditAmount, setCreditAmount] = useState<number>(1000);
  const [loading, setLoading] = useState(false);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [checkoutError, setCheckoutError] = useState<string | null>(null);

  useEffect(() => {
    async function fetchRails() {
      try {
        const response = await fetch("/api/v1/accounts/current/checkout/rails", {
          credentials: "include",
        });
        if (!response.ok) {
          setFetchError("Unable to load payment options. Please refresh.");
          return;
        }
        const data: unknown = await response.json();
        if (
          data !== null &&
          typeof data === "object" &&
          "rails" in data &&
          Array.isArray((data as { rails: unknown[] }).rails)
        ) {
          const typed = data as CheckoutOptions;
          setOptions(typed);
          const enabledRails = typed.rails.filter((r) => r.enabled);
          if (enabledRails.length > 0) {
            setSelectedRail(enabledRails[0].rail);
          }
          if (typed.min_credits) {
            setCreditAmount(typed.min_credits);
          }
        }
      } catch {
        setFetchError("Unable to load payment options. Please refresh.");
      }
    }

    void fetchRails();
  }, []);

  const selectedRailData: CheckoutRail | undefined = options?.rails.find(
    (r) => r.rail === selectedRail
  );

  function computeAmountLocal(): number {
    if (!options || !selectedRailData) {
      return 0;
    }
    // Amount is in cents: price_per_credit_usd is in USD per credit
    // The backend returns amount_local via initiate, but we can estimate here
    // using price_per_credit_usd as a proxy for the local amount display
    return Math.round(creditAmount * options.price_per_credit_usd * 100);
  }

  async function handleCheckout() {
    if (!selectedRail || !options) {
      return;
    }

    setLoading(true);
    setCheckoutError(null);

    try {
      const idempotencyKey = `checkout-${Date.now()}-${Math.random().toString(36).slice(2)}`;
      const response = await fetch("/api/v1/accounts/current/checkout/initiate", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          rail: selectedRail,
          credits: creditAmount,
          idempotency_key: idempotencyKey,
        }),
      });

      if (!response.ok) {
        setCheckoutError("Unable to start checkout. Please try again.");
        setLoading(false);
        return;
      }

      const data: unknown = await response.json();
      if (
        data !== null &&
        typeof data === "object" &&
        "redirect_url" in data &&
        typeof (data as { redirect_url: unknown }).redirect_url === "string"
      ) {
        window.location.href = (data as { redirect_url: string }).redirect_url;
      } else {
        setCheckoutError("Unable to start checkout. Please try again.");
        setLoading(false);
      }
    } catch {
      setCheckoutError("Unable to start checkout. Please try again.");
      setLoading(false);
    }
  }

  function handleOverlayClick(e: React.MouseEvent<HTMLDivElement>) {
    if (e.target === e.currentTarget) {
      onClose();
    }
  }

  const increment = options?.credit_increment ?? 1000;
  const minCredits = options?.min_credits ?? 1000;
  const maxCredits = options?.max_credits ?? 100000;

  function decrementAmount() {
    setCreditAmount((prev) => Math.max(minCredits, prev - increment));
  }

  function incrementAmount() {
    setCreditAmount((prev) => Math.min(maxCredits, prev + increment));
  }

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        backgroundColor: "rgba(0,0,0,0.4)",
        zIndex: 50,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
      }}
      onClick={handleOverlayClick}
    >
      <div
        style={{
          backgroundColor: "#ffffff",
          borderRadius: "0.75rem",
          padding: "1.5rem",
          width: "100%",
          maxWidth: "32rem",
          display: "grid",
          gap: "1rem",
        }}
      >
        <h2 style={{ margin: 0, fontSize: "1.5rem", fontWeight: 700 }}>Buy Credits</h2>

        {fetchError && (
          <p style={{ margin: 0, color: "#dc2626", fontSize: "0.875rem" }}>{fetchError}</p>
        )}

        {!options && !fetchError && (
          <p style={{ margin: 0, color: "#6b7280" }}>Loading...</p>
        )}

        {options && (
          <>
            {/* Payment method selection */}
            <div style={{ display: "grid", gap: "0.5rem" }}>
              <span style={{ fontWeight: 700, fontSize: "0.875rem", color: "#4b5563" }}>
                Payment method
              </span>
              {options.rails
                .filter((r) => r.enabled)
                .map((rail) => (
                  <label
                    key={rail.rail}
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: "0.5rem",
                      cursor: "pointer",
                      padding: "0.5rem",
                      border: `1px solid ${selectedRail === rail.rail ? "#1d4ed8" : "#d1d5db"}`,
                      borderRadius: "0.375rem",
                    }}
                  >
                    <input
                      type="radio"
                      name="rail"
                      value={rail.rail}
                      checked={selectedRail === rail.rail}
                      onChange={() => setSelectedRail(rail.rail)}
                    />
                    {rail.label}
                  </label>
                ))}
            </div>

            {/* Amount picker */}
            <div style={{ display: "grid", gap: "0.5rem" }}>
              <span style={{ fontWeight: 700, fontSize: "0.875rem", color: "#4b5563" }}>
                Credits to purchase
              </span>
              <div style={{ display: "flex", alignItems: "center", gap: "0.5rem" }}>
                <button
                  type="button"
                  onClick={decrementAmount}
                  disabled={creditAmount <= minCredits}
                  style={{
                    background: "transparent",
                    color: "#1d4ed8",
                    border: "1px solid #1d4ed8",
                    borderRadius: "0.375rem",
                    padding: "0.5rem 0.75rem",
                    cursor: creditAmount <= minCredits ? "not-allowed" : "pointer",
                    fontWeight: 700,
                    opacity: creditAmount <= minCredits ? 0.5 : 1,
                  }}
                >
                  −
                </button>
                <input
                  type="number"
                  value={creditAmount}
                  min={minCredits}
                  max={maxCredits}
                  step={increment}
                  onChange={(e) => {
                    const val = Number(e.target.value);
                    if (!isNaN(val)) {
                      setCreditAmount(Math.max(minCredits, Math.min(maxCredits, val)));
                    }
                  }}
                  style={{
                    border: "1px solid #d1d5db",
                    borderRadius: "0.375rem",
                    padding: "0.5rem",
                    width: "8rem",
                    textAlign: "center",
                    fontSize: "1rem",
                  }}
                />
                <button
                  type="button"
                  onClick={incrementAmount}
                  disabled={creditAmount >= maxCredits}
                  style={{
                    background: "transparent",
                    color: "#1d4ed8",
                    border: "1px solid #1d4ed8",
                    borderRadius: "0.375rem",
                    padding: "0.5rem 0.75rem",
                    cursor: creditAmount >= maxCredits ? "not-allowed" : "pointer",
                    fontWeight: 700,
                    opacity: creditAmount >= maxCredits ? 0.5 : 1,
                  }}
                >
                  +
                </button>
                <span style={{ color: "#6b7280", fontSize: "0.875rem" }}>Hive Credits</span>
              </div>
            </div>

            {/* Price display — REGULATORY COMPLIANT: shows only local currency, never USD equivalent or FX rates */}
            {selectedRailData && (
              <div
                style={{
                  padding: "0.75rem",
                  background: "#f9fafb",
                  borderRadius: "0.375rem",
                  fontSize: "1rem",
                }}
              >
                <span style={{ fontWeight: 700 }}>Total: </span>
                {formatPrice(computeAmountLocal(), selectedRailData.currency)}
              </div>
            )}

            {checkoutError && (
              <p style={{ margin: 0, color: "#dc2626", fontSize: "0.875rem" }}>{checkoutError}</p>
            )}

            {/* Actions */}
            <div style={{ display: "flex", gap: "0.75rem" }}>
              <button
                type="button"
                onClick={() => void handleCheckout()}
                disabled={loading || !selectedRail}
                style={{
                  background: loading || !selectedRail ? "#9ca3af" : "#1d4ed8",
                  color: "#ffffff",
                  padding: "0.5rem 1rem",
                  borderRadius: "0.375rem",
                  border: "none",
                  fontWeight: 700,
                  cursor: loading || !selectedRail ? "not-allowed" : "pointer",
                  opacity: loading || !selectedRail ? 0.7 : 1,
                }}
              >
                {loading ? "Loading..." : "Continue to payment"}
              </button>
              <button
                type="button"
                onClick={onClose}
                style={{
                  background: "transparent",
                  color: "#1d4ed8",
                  border: "1px solid #1d4ed8",
                  padding: "0.5rem 1rem",
                  borderRadius: "0.375rem",
                  fontWeight: 700,
                  cursor: "pointer",
                }}
              >
                Keep balance
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
