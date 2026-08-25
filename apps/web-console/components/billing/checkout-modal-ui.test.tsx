/**
 * Behavioral UI tests for CheckoutModal (the money entry surface).
 *
 * The sibling checkout-modal.test.tsx locks the pricing math; this suite
 * drives the rendered component through jsdom: rails loading, amount
 * stepping against min/max, cancel paths that must NOT fire a checkout
 * intent, and the initiate POST payload.
 */
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";

import { CheckoutModal } from "./checkout-modal";
import type { CheckoutOptions } from "@/lib/control-plane/client";

const RAILS_URL = "/api/v1/accounts/current/checkout/rails";
const INITIATE_URL = "/api/v1/accounts/current/checkout/initiate";

function optionsFixture(overrides: Partial<CheckoutOptions> = {}): CheckoutOptions {
  return {
    rails: [
      {
        rail: "bkash",
        currency: "BDT",
        label: "bKash",
        enabled: true,
      },
    ],
    credit_increment: 500,
    min_credits: 1000,
    max_credits: 3000,
    price_per_block_minor: 11550,
    credit_block_size: 1_000_000_000,
    currency: "BDT",
    ...overrides,
  };
}

// One fetch mock for both calls the modal makes: GET rails on mount and
// POST initiate on Continue to payment. Dispatch by URL so each test only
// declares the initiate outcome it cares about.
function stubFetch(opts: {
  rails?: CheckoutOptions;
  railsOk?: boolean;
  initiate?: () => Response;
}) {
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input);
      if (url === RAILS_URL) {
        if (opts.railsOk === false) {
          return new Response("nope", { status: 500 });
        }
        return new Response(JSON.stringify(opts.rails ?? optionsFixture()), {
          status: 200,
        });
      }
      if (url === INITIATE_URL) {
        return opts.initiate ? opts.initiate() : new Response("{}", { status: 200 });
      }
      throw new Error(`Unexpected fetch: ${url}`);
    },
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function buttonDisabled(button: HTMLElement): boolean {
  return button instanceof HTMLButtonElement && button.disabled;
}

function amountInput(): HTMLInputElement {
  // The numeric credit field renders as a spinbutton. Its wrapper div
  // shares the label wiring, so role-based lookup is the reliable handle.
  const el = screen.getByRole("spinbutton");
  if (!(el instanceof HTMLInputElement)) {
    throw new Error("credit amount spinbutton not found");
  }
  return el;
}

async function renderLoadedModal(props: {
  accountCountryCode?: string;
  onClose?: () => void;
} = {}) {
  const onClose = props.onClose ?? vi.fn();
  render(
    <CheckoutModal accountCountryCode={props.accountCountryCode ?? "US"} onClose={onClose} />,
  );
  // Wait past the mount-time rails fetch.
  await screen.findByText("Payment method");
  return { onClose };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("CheckoutModal behavior", () => {
  it("pay button is gated until a rail is selectable (no enabled rails renders it disabled)", async () => {
    stubFetch({ rails: optionsFixture({ rails: [{ rail: "bkash", currency: "BDT", label: "bKash", enabled: false }] }) });
    render(<CheckoutModal accountCountryCode="US" onClose={vi.fn()} />);
    await screen.findByText("Payment method");
    const pay = screen.getByRole("button", { name: /continue to payment/i });
    expect(buttonDisabled(pay)).toBe(true);
  });

  it("increase and decrease step by credit_increment and clamp to min/max", async () => {
    stubFetch({});
    await renderLoadedModal();
    const increase = screen.getByRole("button", { name: "Increase" });
    const decrease = screen.getByRole("button", { name: "Decrease" });

    // Starts at min_credits; decrease is pinned at the floor.
    expect(amountInput().value).toBe("1000");
    expect(buttonDisabled(decrease)).toBe(true);

    fireEvent.click(increase);
    expect(amountInput().value).toBe("1500");
    fireEvent.click(increase);
    expect(amountInput().value).toBe("2000");

    fireEvent.click(decrease);
    expect(amountInput().value).toBe("1500");
    expect(buttonDisabled(decrease)).toBe(false);

    // Walk back down to the floor; decrease disables again.
    fireEvent.click(decrease);
    fireEvent.click(decrease);
    expect(amountInput().value).toBe("1000");
    expect(buttonDisabled(decrease)).toBe(true);

    // Typing past the ceiling clamps instead of storing an invalid amount.
    fireEvent.change(amountInput(), { target: { value: "99999" } });
    expect(amountInput().value).toBe("3000");

    // At the ceiling the increase button pins too.
    expect(buttonDisabled(increase)).toBe(true);
  });

  it("cancel (Keep balance) closes without firing a checkout intent", async () => {
    const fetchMock = stubFetch({});
    const { onClose } = await renderLoadedModal();

    fireEvent.click(screen.getByRole("button", { name: /keep balance/i }));

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(1); // rails GET only
    expect(fetchMock.mock.calls.every(([u]) => String(u) === RAILS_URL)).toBe(true);
  });

  it("overlay click equals cancel: backdrop closes, content inside does not", async () => {
    stubFetch({});
    const { onClose } = await renderLoadedModal();
    const dialog = screen.getByRole("dialog");

    // Click on inner content bubbles but target != currentTarget: no close.
    fireEvent.click(screen.getByText("Buy credits"));
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(dialog); // target IS currentTarget here
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("close (X) button closes the modal without any network call", async () => {
    const fetchMock = stubFetch({});
    const { onClose } = await renderLoadedModal();
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(1); // rails GET only
  });

  it("continue to payment fires the initiate intent exactly once with the selected rail and stepped credits", async () => {
    const fetchMock = stubFetch({
      initiate: () =>
        new Response(JSON.stringify({ redirect_url: "https://pay.example.test/redirect" }), {
          status: 200,
        }),
    });
    await renderLoadedModal();

    fireEvent.click(screen.getByRole("button", { name: /increase/i }));
    fireEvent.click(screen.getByRole("button", { name: /continue to payment/i }));

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(2);
    });
    const [url, init] = fetchMock.mock.calls[1];
    expect(String(url)).toBe(INITIATE_URL);
    expect(init?.method).toBe("POST");
    const body = JSON.parse(String(init?.body));
    expect(body).toEqual({
      rail: "bkash",
      credits: 1500,
      idempotency_key: expect.stringMatching(/^checkout-\d+-[a-z0-9]+$/),
    });
  });

  it("failed initiate surfaces an error and hands control back to the user", async () => {
    stubFetch({ initiate: () => new Response("declined", { status: 402 }) });
    await renderLoadedModal();

    fireEvent.click(screen.getByRole("button", { name: /continue to payment/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Unable to start checkout");
    const pay = screen.getByRole("button", { name: /continue to payment/i });
    expect(buttonDisabled(pay)).toBe(false);
  });

  it("rails load failure shows the refresh error instead of the form", async () => {
    stubFetch({ railsOk: false });
    render(<CheckoutModal accountCountryCode="US" onClose={vi.fn()} />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Unable to load payment options");
    expect(screen.queryByText(/continue to payment/i)).toBeNull();
  });

  it("non-BD accounts see the computed local total; BD accounts never do", async () => {
    stubFetch({});
    const { unmount } = render(
      <CheckoutModal accountCountryCode="US" onClose={vi.fn()} />,
    );
    await screen.findByText("Payment method");
    expect(screen.getByText("Total")).toBeTruthy();
    expect(document.querySelector("[data-numeric]")).toBeTruthy();
    unmount();
    cleanup();

    stubFetch({});
    render(<CheckoutModal accountCountryCode="BD" onClose={vi.fn()} />);
    await screen.findByText("Payment method");
    expect(screen.getByText("Final amount")).toBeTruthy();
    // Regulatory invariant: no numeric FX-style total on the BD surface.
    expect(document.querySelector("[data-numeric]")).toBeNull();
    expect(screen.getByText(/shown on the bKash payment page/i)).toBeTruthy();
  });
});
