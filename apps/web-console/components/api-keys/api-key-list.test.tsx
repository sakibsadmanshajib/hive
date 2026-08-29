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
