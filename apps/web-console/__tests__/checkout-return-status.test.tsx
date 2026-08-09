import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { CheckoutReturnStatus } from "../components/billing/checkout-return-status";
import type { CheckoutIntent } from "../lib/control-plane/client";

const INTENT = "123e4567-e89b-12d3-a456-426614174000";

function intent(overrides: Partial<CheckoutIntent> = {}): CheckoutIntent {
  return {
    payment_intent_id: INTENT,
    rail: "sslcommerz",
    status: "completed",
    state: "success",
    credits: 5000,
    ...overrides,
  };
}

describe("CheckoutReturnStatus", () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("renders the settled outcome for a completed payment", () => {
    render(<CheckoutReturnStatus initial={intent()} hint={null} />);

    expect(screen.getByRole("heading", { name: /payment complete/i })).toBeDefined();
    expect(screen.getByText(/5,000 credits/i)).toBeDefined();
  });

  it("renders a pending state that explains the balance updates on confirmation", () => {
    render(
      <CheckoutReturnStatus
        initial={intent({ status: "confirming", state: "pending" })}
        hint={null}
      />,
    );

    expect(screen.getByRole("heading", { name: /confirming your payment/i })).toBeDefined();
  });

  it("renders a failure state", () => {
    render(
      <CheckoutReturnStatus initial={intent({ status: "failed", state: "failed" })} hint={null} />,
    );

    expect(screen.getByRole("heading", { name: /payment did not go through/i })).toBeDefined();
  });

  it("renders a cancelled state", () => {
    render(
      <CheckoutReturnStatus
        initial={intent({ status: "cancelled", state: "cancelled" })}
        hint={null}
      />,
    );

    expect(screen.getByRole("heading", { name: /payment cancelled/i })).toBeDefined();
  });

  it("uses the cancelled hint only to soften pending copy, never to claim an outcome", () => {
    render(
      <CheckoutReturnStatus
        initial={intent({ status: "pending_redirect", state: "pending" })}
        hint="cancelled"
      />,
    );

    // Still the pending surface: the hint is a copy selector, not a state.
    expect(screen.getByText(/you left the payment page/i)).toBeDefined();
    expect(screen.queryByRole("heading", { name: /payment complete/i })).toBeNull();
  });

  it("never claims success when a crafted hint says success but the ledger does not", () => {
    render(
      <CheckoutReturnStatus
        initial={intent({ status: "failed", state: "failed" })}
        hint="success"
      />,
    );

    expect(screen.queryByRole("heading", { name: /payment complete/i })).toBeNull();
    expect(screen.getByRole("heading", { name: /payment did not go through/i })).toBeDefined();
  });

  it("polls the account-scoped status endpoint while pending and resolves to success", async () => {
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      void input;
      return new Response(JSON.stringify(intent({ status: "completed", state: "success" })), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    render(
      <CheckoutReturnStatus
        initial={intent({ status: "confirming", state: "pending" })}
        hint={null}
      />,
    );

    await vi.advanceTimersByTimeAsync(5000);

    await waitFor(() => {
      expect(screen.getByRole("heading", { name: /payment complete/i })).toBeDefined();
    });
    const requestedUrl = String(fetchMock.mock.calls[0]?.[0] ?? "");
    expect(requestedUrl).toContain("/api/console/checkout/intent");
    expect(requestedUrl).toContain(`payment_intent_id=${INTENT}`);
  });

  it("stops polling once the state is terminal", async () => {
    const fetchMock = vi.fn(async () => new Response("{}", { status: 500 }));
    vi.stubGlobal("fetch", fetchMock);

    render(<CheckoutReturnStatus initial={intent()} hint={null} />);
    await vi.advanceTimersByTimeAsync(20000);

    expect(fetchMock).not.toHaveBeenCalled();
  });
});
