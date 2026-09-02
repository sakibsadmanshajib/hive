/**
 * Behavioral tests for the API keys list's budget columns.
 *
 * Both figures are credit integers off the wire (ApiKey.spend_credits,
 * ApiKey.budget_limit_credits) and both render as Hive credits, with no
 * currency anywhere (owner ruling, .wolf/decisions.md D-070, issue #1694).
 * They used to go through the console's USD price formatter, which put a
 * dollar cap on the same row as a credit-denominated spend and handed the
 * reader the credit peg between them.
 */
import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

import type { ApiKey } from "@/lib/control-plane/client";
import { ApiKeyList } from "./api-key-list";
import { CURRENCY_MARK } from "@/tests/support/currency-mark";

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
  it("a capless key with real spend shows the credit spend and Unlimited", () => {
    render(
      <ApiKeyList
        keys={[baseKey({ spend_credits: 360_000_000 })]}
        canManage={false}
      />,
    );
    expect(screen.getByText("360,000,000 credits")).toBeTruthy();
    expect(screen.getByText("Unlimited")).toBeTruthy();
    // Grouped, so the ungrouped integer is still absent from the cell.
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
    expect(screen.getByText("0 credits")).toBeTruthy();
    expect(screen.getByText("5,000,000,000 credits/mo")).toBeTruthy();
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
    expect(screen.getByText("1,000,000,000 credits total")).toBeTruthy();
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
    expect(screen.getByText("360,000,000 credits")).toBeTruthy();
  });

  it("fills the bar to the enforced share and shows the percentage beside both credit figures", () => {
    render(
      <ApiKeyList keys={[lifetimeKey(1_000_000_000, 5_000_000_000)]} canManage={false} />,
    );

    const bar = screen.getByRole("progressbar");
    expect(bar.getAttribute("value")).toBe("20");
    expect(bar.getAttribute("max")).toBe("100");
    expect(bar.getAttribute("aria-label")).toContain("1,000,000,000");
    expect(bar.getAttribute("aria-label")).toContain("5,000,000,000 credits total");
    expect(screen.getByText("20.0%")).toBeTruthy();
    expect(screen.getByText("1,000,000,000")).toBeTruthy();
    expect(screen.getByText("5,000,000,000 credits total")).toBeTruthy();
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
    // The defect this guards: 2,970,000,000 credits of lifetime spend against
    // a 2,000,000,000 credit cap set afterwards is 148.5% and a red "Limit
    // reached" if the lifetime figure is the numerator, while edge-api reads an
    // empty budget window and serves the key's next request.
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
    expect(screen.getByText("2,970,000,000 credits lifetime")).toBeTruthy();
  });

  it("keeps the lifetime note off a key whose enforced figure is the larger of the two", () => {
    // The enforced counter includes reserved credits and the lifetime rollup
    // does not, so a key holding a stranded reservation has the smaller
    // lifetime figure. Printing "2,970,000,000 credits lifetime" beneath
    // "3,200,000,000 of 5,000,000,000 credits total" explains nothing and
    // reads as a contradiction.
    render(
      <ApiKeyList
        keys={[
          lifetimeKey(3_200_000_000, 5_000_000_000, {
            spend_credits: 2_970_000_000,
          }),
        ]}
        canManage={false}
      />,
    );

    expect(screen.getByText("3,200,000,000")).toBeTruthy();
    expect(screen.queryByText("2,970,000,000 credits lifetime")).toBeNull();
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
    expect(bar.getAttribute("aria-label")).toBe(
      "Budget used: 2,500,000,000 of 10,000,000,000 credits/mo",
    );
    expect(screen.getByText("25.0%")).toBeTruthy();
    // A twelve billion credit lifetime total against a ten billion credit
    // monthly cap is the ratio this column must never state; it is present as
    // its own figure.
    expect(screen.getByText("12,000,000,000 credits lifetime")).toBeTruthy();
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
    const cell = screen.getByText("360,000,000 credits").parentElement;
    expect(cell?.textContent).toBe(
      "360,000,000 creditslifetime\u00b710,000,000,000 credits/mocap",
    );
  });
});

/**
 * The guard for issue #1694, at the component that most recently shipped the
 * leak.
 *
 * The budget column landed in PR #1685 rendering both of its figures through
 * the console's USD price formatter. Nothing in this cell may carry a currency
 * mark: not the visible figures, not the badge, not the accessible name, and
 * not in any of the states the cell has. All of them are rendered in one
 * table, because a per-state render would leave the over-limit branch, which
 * has its own strings, unchecked if only the partial case were covered.
 */
describe("ApiKeyList budget usage bar renders credits, never currency", () => {
  const everyState: ApiKey[] = [
    // Partial.
    baseKey({
      id: "state-partial",
      spend_credits: 1_000_000_000,
      budget_spend_credits: 1_000_000_000,
      budget_limit_credits: 5_000_000_000,
      budget_summary: { kind: "lifetime", label: "Lifetime budget cap" },
    }),
    // Over limit, clamped.
    baseKey({
      id: "state-over",
      spend_credits: 7_500_000_000,
      budget_spend_credits: 7_500_000_000,
      budget_limit_credits: 5_000_000_000,
      budget_summary: { kind: "monthly", label: "Monthly budget cap" },
    }),
    // Zero cap, exhausted by definition.
    baseKey({
      id: "state-zero",
      spend_credits: 1_000_000,
      budget_spend_credits: 1_000_000,
      budget_limit_credits: 0,
      budget_summary: { kind: "lifetime", label: "Lifetime budget cap" },
    }),
    // Cap with no enforced counter: two figures, no bar.
    baseKey({
      id: "state-null-counter",
      spend_credits: 360_000_000,
      budget_spend_credits: null,
      budget_limit_credits: 10_000_000_000,
      budget_summary: { kind: "monthly", label: "Monthly budget cap" },
    }),
    // Uncapped.
    baseKey({ id: "state-uncapped", spend_credits: 360_000_000 }),
  ];

  it("carries no currency mark in any state, visible or accessible", () => {
    const { container } = render(
      <ApiKeyList keys={everyState} canManage={false} />,
    );

    expect(container.textContent ?? "").not.toMatch(CURRENCY_MARK);
    // textContent reads text nodes only, so the accessible half of this
    // assertion has to read attributes separately. Every element carrying one
    // of the three, rather than the two attributes on the progress element:
    // a currency figure added to a badge's title or a button's label is a
    // leak this test's own name promises to catch.
    for (const el of container.querySelectorAll(
      "[aria-label],[aria-valuetext],[title]",
    )) {
      for (const attr of ["aria-label", "aria-valuetext", "title"]) {
        expect(el.getAttribute(attr) ?? "").not.toMatch(CURRENCY_MARK);
      }
    }
    const bars = container.querySelectorAll("progress");
    // Three of the five states draw a bar. The uncapped key has no limit to
    // divide by and the absent-counter key has no numerator, so both
    // deliberately draw none. Asserted so an empty NodeList cannot pass this
    // loop silently.
    expect(bars.length).toBe(3);
    // And the credit unit is still stated, so the integers are not left
    // unlabelled by a fix that only removed the dollar sign.
    expect(screen.getByText("5,000,000,000 credits total")).toBeTruthy();
    expect(screen.getAllByText("360,000,000 credits").length).toBe(2);
  });
});
