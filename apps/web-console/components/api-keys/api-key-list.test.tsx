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
