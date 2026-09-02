/**
 * Behavioral tests for the API keys list's Spend and Credit limit columns.
 * Both read raw credit integers off the wire (ApiKey.spend_credits,
 * ApiKey.budget_limit_credits) and must never render one directly -- this
 * pins that they go through formatUsdFromCredits, and that a null limit
 * reads as "Unlimited" rather than "$0" or a blank cell.
 */
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

import type { ApiKey } from "@/lib/control-plane/client";
import { ApiKeyList } from "./api-key-list";

afterEach(() => {
  cleanup();
});

function baseKey(overrides: Partial<ApiKey> = {}): ApiKey {
  return {
    id: "key-1",
    nickname: "prod-server",
    status: "active",
    redacted_suffix: "ab12",
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    expires_at: null,
    last_used_at: null,
    expiration_summary: { kind: "never", label: "Never expires" },
    budget_summary: { kind: "none", label: "No budget cap" },
    allowlist_summary: { mode: "all", group_names: [], label: "All models" },
    spend_credits: 0,
    budget_limit_credits: null,
    budget_spend_credits: null,
    ...overrides,
  };
}

describe("ApiKeyList spend and credit-limit columns", () => {
  it("a capless key with real spend shows the dollar spend and Unlimited, never a raw integer", () => {
    render(
      <ApiKeyList
        keys={[baseKey({ spend_credits: 360_000_000 })]}
        canManage={false}
      />,
    );
    // 360,000,000 credits / 1e9 credits-per-USD = $0.36.
    expect(screen.getByText("$0.36")).toBeTruthy();
    expect(screen.getByText("Unlimited")).toBeTruthy();
    expect(screen.queryByText("360000000")).toBeNull();
  });

  it("a capped key shows the limit with its reset cadence suffix", () => {
    render(
      <ApiKeyList
        keys={[
          baseKey({
            id: "key-2",
            spend_credits: 0,
            budget_limit_credits: 5_000_000_000,
            budget_summary: {
              kind: "monthly",
              label: "Monthly budget cap: 5000000000 credits",
            },
          }),
        ]}
        canManage={false}
      />,
    );
    expect(screen.getByText("$0")).toBeTruthy();
    expect(screen.getByText("$5.00/mo")).toBeTruthy();
    expect(screen.queryByText("5000000000")).toBeNull();
  });

  it("a lifetime cap renders the total-not-monthly suffix", () => {
    render(
      <ApiKeyList
        keys={[
          baseKey({
            id: "key-3",
            budget_limit_credits: 1_000_000_000,
            budget_summary: {
              kind: "lifetime",
              label: "Lifetime budget cap: 1000000000 credits",
            },
          }),
        ]}
        canManage={false}
      />,
    );
    expect(screen.getByText("$1.00 total")).toBeTruthy();
  });
});

describe("ApiKeyList name column", () => {
  // Issue #1400: a 5000-character nickname made the row 50,000 pixels wide,
  // pushing every later column, Revoke included, out of reach for every key
  // in the workspace. A cap on new nicknames does nothing for a row that is
  // already stored, so the cell has to bound itself. jsdom cannot measure
  // layout; what is pinned here is that the constraint and the full value
  // are both present, and the screenshot on the pull request carries the
  // rendered proof.
  it("bounds the name cell and keeps the full value reachable", () => {
    const long = "A".repeat(5000);
    render(
      <ApiKeyList keys={[baseKey({ nickname: long })]} canManage={false} />,
    );

    const cell = screen.getByTitle(long);
    expect(cell.className).toContain("truncate");
    expect(cell.className).toMatch(/max-w-/);
    expect(cell.textContent).toBe(long);
  });

  it("leaves an ordinary nickname readable", () => {
    render(
      <ApiKeyList
        keys={[baseKey({ nickname: "prod-server" })]}
        canManage={false}
      />,
    );
    expect(screen.getByTitle("prod-server").textContent).toBe("prod-server");
  });
});

/**
 * Issue #1683: the spend-against-limit surface was two plain dollar cells and
 * the reader had to divide them in their head.
 *
 * The numerator these pin is budget_spend_credits, the counter edge-api
 * enforces against, not spend_credits. The lifetime rollup takes every settled
 * request while the budget window only starts once a cap exists, so a bar
 * drawn from the lifetime figure reports a refusal the gateway is not making.
 */
describe("ApiKeyList budget usage bar", () => {
  function lifetimeKey(
    budgetSpend: number | null,
    limit: number | null,
    overrides: Partial<ApiKey> = {},
  ): ApiKey {
    return baseKey({
      // Equal by default so the lifetime note stays out of the way of the
      // cases that are not about it.
      spend_credits: budgetSpend ?? 0,
      budget_spend_credits: budgetSpend,
      budget_limit_credits: limit,
      budget_summary: { kind: "lifetime", label: "Lifetime budget cap" },
      ...overrides,
    });
  }

  it("renders no bar for an uncapped key, so nothing divides by a limit that is absent", () => {
    render(
      <ApiKeyList keys={[baseKey({ spend_credits: 360_000_000 })]} canManage={false} />,
    );

    expect(screen.queryByRole("progressbar")).toBeNull();
    expect(screen.getByText("Unlimited")).toBeTruthy();
    expect(screen.getByText("$0.36")).toBeTruthy();
  });

  it("fills the bar to the enforced share and shows the percentage beside both dollar figures", () => {
    render(
      <ApiKeyList keys={[lifetimeKey(1_000_000_000, 5_000_000_000)]} canManage={false} />,
    );

    const bar = screen.getByRole("progressbar");
    expect(bar.getAttribute("value")).toBe("20");
    expect(bar.getAttribute("max")).toBe("100");
    expect(bar.getAttribute("aria-label")).toContain("$1.00");
    expect(bar.getAttribute("aria-label")).toContain("$5.00 total");
    expect(screen.getByText("20.0%")).toBeTruthy();
    expect(screen.getByText("$1.00")).toBeTruthy();
    expect(screen.getByText("$5.00 total")).toBeTruthy();
    expect(screen.queryByText("Limit reached")).toBeNull();
  });

  it("marks a key that has spent exactly its limit as reached, with a full bar", () => {
    render(
      <ApiKeyList keys={[lifetimeKey(5_000_000_000, 5_000_000_000)]} canManage={false} />,
    );

    expect(screen.getByRole("progressbar").getAttribute("value")).toBe("100");
    expect(screen.getByText("100.0%")).toBeTruthy();
    expect(screen.getByText("Limit reached")).toBeTruthy();
  });

  it("clamps an over-limit key to a full track while still reporting the true percentage", () => {
    render(
      <ApiKeyList keys={[lifetimeKey(7_500_000_000, 5_000_000_000)]} canManage={false} />,
    );

    // The fill is capped at the track; only the number carries the overshoot,
    // so the bar cannot render wider than the column that holds it.
    expect(screen.getByRole("progressbar").getAttribute("value")).toBe("100");
    expect(screen.getByText("150.0%")).toBeTruthy();
    expect(screen.getByText("Limit reached")).toBeTruthy();
  });

  it("treats a zero limit as exhausted rather than dividing by zero", () => {
    render(<ApiKeyList keys={[lifetimeKey(1_000_000, 0)]} canManage={false} />);

    expect(screen.getByRole("progressbar").getAttribute("value")).toBe("100");
    expect(screen.getByText("Limit reached")).toBeTruthy();
    expect(screen.queryByText(/NaN|Infinity/)).toBeNull();
  });

  it("treats a zero limit with nothing spent as exhausted too, since it refuses the first request", () => {
    // handleUpdatePolicy accepts a zero budget_limit_credits with no
    // positivity check, and enforcement is consumed + reserved + estimated >
    // limit, so a zero cap rejects every call. An empty bar reading 0.0% would
    // be the opposite of what the gateway does.
    render(<ApiKeyList keys={[lifetimeKey(0, 0)]} canManage={false} />);

    expect(screen.getByRole("progressbar").getAttribute("value")).toBe("100");
    expect(screen.getByText("Limit reached")).toBeTruthy();
  });

  it("renders a zero-spend capped key as an empty bar, not a missing one", () => {
    render(<ApiKeyList keys={[lifetimeKey(0, 5_000_000_000)]} canManage={false} />);

    expect(screen.getByRole("progressbar").getAttribute("value")).toBe("0");
    expect(screen.getByText("0.0%")).toBeTruthy();
  });

  it("divides the enforced window and not the lifetime spend, so a key capped after it spent is not painted as refused", () => {
    // The defect this guards: $2.97 of lifetime spend against a $2.00 cap set
    // afterwards is 148.5% and a red "Limit reached" if the lifetime figure is
    // the numerator, while edge-api reads an empty budget window and serves
    // the key's next request.
    render(
      <ApiKeyList
        keys={[
          lifetimeKey(0, 2_000_000_000, { spend_credits: 2_970_000_000 }),
        ]}
        canManage={false}
      />,
    );

    expect(screen.getByRole("progressbar").getAttribute("value")).toBe("0");
    expect(screen.getByText("0.0%")).toBeTruthy();
    expect(screen.queryByText("Limit reached")).toBeNull();
    expect(screen.queryByText("148.5%")).toBeNull();
    // The lifetime total is not hidden, it is just kept away from the ratio.
    expect(screen.getByText("$2.97 lifetime")).toBeTruthy();
  });

  it("draws the bar for a monthly cap too, because the enforced counter is that month's window", () => {
    render(
      <ApiKeyList
        keys={[
          baseKey({
            spend_credits: 12_000_000_000,
            budget_spend_credits: 2_500_000_000,
            budget_limit_credits: 10_000_000_000,
            budget_summary: { kind: "monthly", label: "Monthly budget cap" },
          }),
        ]}
        canManage={false}
      />,
    );

    const bar = screen.getByRole("progressbar");
    expect(bar.getAttribute("value")).toBe("25");
    expect(bar.getAttribute("aria-label")).toBe("Budget used: $2.50 of $10.00/mo");
    expect(screen.getByText("25.0%")).toBeTruthy();
    // A twelve dollar lifetime total against a ten dollar monthly cap is the
    // ratio this column must never state; it is present as its own figure.
    expect(screen.getByText("$12.00 lifetime")).toBeTruthy();
  });

  it("states both figures with no part-of-whole connective when the enforced counter is absent", () => {
    // An older control-plane sends no budget_spend_credits. The proportion is
    // exactly what is unknown then, so the cell draws no bar and does not join
    // the two numbers with "of", which would state the ratio in prose.
    render(
      <ApiKeyList
        keys={[
          baseKey({
            spend_credits: 360_000_000,
            budget_spend_credits: null,
            budget_limit_credits: 10_000_000_000,
            budget_summary: { kind: "monthly", label: "Monthly budget cap" },
          }),
        ]}
        canManage={false}
      />,
    );

    expect(screen.queryByRole("progressbar")).toBeNull();
    const cell = screen.getByText("$0.36").parentElement;
    expect(cell?.textContent).toBe("$0.36lifetime\u00b7$10.00/mocap");
  });
});
