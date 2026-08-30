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
  // The deployed box serves exactly this: one rail, disabled, and the server's
  // "nothing is selectable" ceiling of 0 alongside a minimum of 10,000,000.
  // The modal used to render an empty rail fieldset, a permanently disabled
  // Continue button with no explanation, and `min="10000000" max="0"` on the
  // amount input.
  const noRailFixture = () =>
    optionsFixture({
      rails: [{ rail: "stripe", currency: "USD", label: "Card", enabled: false }],
      min_credits: 10_000_000,
      max_credits: 0,
    });

  it("no selectable rail explains itself instead of offering a dead button", async () => {
    stubFetch({ rails: noRailFixture() });
    render(<CheckoutModal accountCountryCode="US" onClose={vi.fn()} />);

    const status = await screen.findByRole("status");
    expect(status.textContent).toMatch(/no payment method is available/i);
    // Nothing to select, so nothing pretends to be selectable.
    expect(screen.queryByText("Payment method")).toBeNull();
    expect(screen.queryByRole("button", { name: /continue to payment/i })).toBeNull();
    // The user can still leave the modal.
    expect(screen.getByRole("button", { name: /keep balance/i })).toBeTruthy();
  });

  // A payload the type guard rejects used to set neither the options nor an
  // error, so the modal sat on "Loading payment options…" forever with nothing
  // to click and nothing in the console. Fail closed is right on a money
  // surface; fail silent is not, because a permanent spinner is
  // indistinguishable from a slow network and gives the payer nothing to do.
  //
  // Two payloads, so this cannot pass by the guard happening to accept one of
  // them: bounds missing entirely, and a bound present but not a number.
  for (const [name, payload] of [
    ["bounds missing while a rail is selectable", { rails: [{ rail: "bkash", currency: "BDT", label: "bKash", enabled: true }] }],
    ["a bound that is not a number", { ...optionsFixture(), max_credits: "lots" }],
  ] as const) {
    it(`says so instead of spinning forever: ${name}`, async () => {
      stubFetch({ rails: payload as unknown as CheckoutOptions });
      render(<CheckoutModal accountCountryCode="BD" onClose={vi.fn()} />);

      const alert = await screen.findByRole("alert");
      expect(alert.textContent).toMatch(/came back unusable/i);
      expect(screen.queryByText(/loading payment options/i)).toBeNull();
      // And nothing invents a bound to render an amount field against.
      expect(screen.queryByRole("spinbutton")).toBeNull();
    });
  }

  it("an inverted or zero purchase range never reaches the DOM", async () => {
    // The healthy case is in this list on purpose. Without it the attribute
    // assertion below would only ever iterate an empty NodeList, which is a
    // green that cannot go red: it would pass just as happily against a modal
    // that renders no input for any payload at all. With it, the loop runs
    // against a real node on one case and stays the tripwire for a regression
    // that starts rendering the field again on the broken ones.
    const cases = [
      { name: "nothing selectable, zero ceiling", rails: noRailFixture(), purchasable: false },
      {
        name: "selectable rail, ceiling below floor",
        rails: optionsFixture({ min_credits: 10_000_000, max_credits: 0 }),
        purchasable: false,
      },
      {
        name: "selectable rail, zero floor and ceiling",
        rails: optionsFixture({ min_credits: 0, max_credits: 0 }),
        purchasable: false,
      },
      {
        // step="0" freezes both stepper buttons on an amount the payer cannot
        // change, which is the same dead control in a different disguise.
        name: "selectable rail, zero increment",
        rails: optionsFixture({ credit_increment: 0 }),
        purchasable: false,
      },
      {
        // A NEGATIVE step is the same fault with a sign: the decrement button
        // would raise the amount. An ABSENT one is deliberately not here,
        // because the `??` fallback substitutes a real one-cent step and the
        // decoder already refuses an absent increment upstream whenever a rail
        // is selectable, so the modal never sees that case from the real path.
        name: "selectable rail, negative increment",
        rails: optionsFixture({ credit_increment: -500 }),
        purchasable: false,
      },
      { name: "healthy range", rails: optionsFixture(), purchasable: true },
    ];

    for (const { name, rails, purchasable } of cases) {
      stubFetch({ rails });
      const { unmount } = render(
        <CheckoutModal accountCountryCode="US" onClose={vi.fn()} />,
      );
      if (purchasable) {
        await screen.findByText("Payment method");
      } else {
        await screen.findByRole("status");
      }

      const numberInputs = Array.from(
        document.querySelectorAll<HTMLInputElement>("input[type=number]"),
      );
      for (const input of numberInputs) {
        const min = Number(input.min);
        const max = Number(input.max);
        expect(
          Number.isFinite(min) && Number.isFinite(max) && max >= min && min > 0,
        ).toBe(true);
      }
      // The purchasable case must actually have produced a field, or the
      // attribute loop above proves nothing about it.
      expect({ case: name, inputs: numberInputs.length > 0 }).toEqual({
        case: name,
        inputs: purchasable,
      });
      expect(screen.queryByRole("spinbutton") !== null).toBe(purchasable);

      unmount();
      cleanup();
    }
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
