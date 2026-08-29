/**
 * Issue #543: four console routes have zero inbound links anywhere in the app,
 * so the only way to reach a per-key rate limit, a spend alert, a workspace
 * budget or the workspace invoice list is to type a URL. Per-key limits and
 * spend caps are exactly the controls a regulated buyer asks to see in a
 * demo, and they are built, they work, and they are unreachable by clicking.
 *
 * Grep-verified before this change: the only references to those four paths in
 * the whole repository were the e2e specs that navigate to them directly, and
 * `main a` on the live /console/api-keys page returned an empty href list.
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ refresh: vi.fn(), push: vi.fn() }),
}));

import { ApiKeyList } from "@/components/api-keys/api-key-list";
import { BillingLinks } from "@/components/billing/billing-links";
import type { ApiKey } from "@/lib/control-plane/client";

function key(overrides: Partial<ApiKey> = {}): ApiKey {
  return {
    id: "key-1",
    nickname: "production",
    status: "active",
    redacted_suffix: "9f2a",
    created_at: "2026-08-01T00:00:00Z",
    updated_at: "2026-08-01T00:00:00Z",
    expires_at: null,
    last_used_at: null,
    expiration_summary: { kind: "never", label: "Never" },
    budget_summary: { kind: "none", label: "Unlimited" },
    allowlist_summary: { mode: "all", group_names: [], label: "All models" },
    spend_credits: 662_000,
    budget_limit_credits: null,
    ...overrides,
  };
}

describe("api key list reaches the per-key limits page", () => {
  it("links each active key to its own limits route", () => {
    render(<ApiKeyList keys={[key()]} canManage />);

    const link = screen.getByRole("link", { name: /limits/i });
    expect(link.getAttribute("href")).toBe("/console/api-keys/key-1/limits");
  });

  it("scopes the link to the row's own key, not a shared path", () => {
    render(<ApiKeyList keys={[key(), key({ id: "key-2", nickname: "staging" })]} canManage />);

    const hrefs = screen
      .getAllByRole("link", { name: /limits/i })
      .map((el) => el.getAttribute("href"));
    expect(hrefs).toEqual([
      "/console/api-keys/key-1/limits",
      "/console/api-keys/key-2/limits",
    ]);
  });

  it("offers no limits link on a revoked key, which has nothing left to limit", () => {
    render(<ApiKeyList keys={[key({ status: "revoked" })]} canManage />);

    expect(screen.queryByRole("link", { name: /limits/i })).toBeNull();
  });

  it("still offers the limits link to a member who cannot manage keys", () => {
    // The limits page renders read-only for a viewer without api_keys.write
    // (it passes canEdit through to the form), so hiding the link would hide a
    // readable page rather than protect anything. canManage gates the actions
    // column, which is why the link cannot live there.
    render(<ApiKeyList keys={[key()]} canManage={false} />);

    expect(screen.getByRole("link", { name: /limits/i })).toBeTruthy();
  });
});

describe("billing overview reaches its sub-pages", () => {
  it("links to spend alerts and workspace budget", () => {
    render(<BillingLinks />);

    expect(screen.getByRole("link", { name: /spend alerts/i }).getAttribute("href")).toBe(
      "/console/billing/alerts",
    );
    expect(screen.getByRole("link", { name: /budget/i }).getAttribute("href")).toBe(
      "/console/billing/budget",
    );
  });

  it("links to the workspace invoice list", () => {
    render(<BillingLinks />);

    expect(screen.getByRole("link", { name: /workspace invoices/i }).getAttribute("href")).toBe(
      "/console/billing/invoices",
    );
  });
});
