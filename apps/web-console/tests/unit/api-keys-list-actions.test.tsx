/**
 * Issue #1331: every active key linked to /console/api-keys/[id]/rotate, a
 * route that does not exist, so the one action a credentials page most needs
 * to be trustworthy answered "This page could not be found". A dead action is
 * worse than an absent one: the user believes rotation is available and stops
 * looking for the path that does work (create a replacement, revoke the old).
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";

vi.mock("next/navigation", () => ({
  useRouter: () => ({ refresh: vi.fn(), push: vi.fn() }),
}));

import { ApiKeyList } from "@/components/api-keys/api-key-list";
import type { ApiKey } from "@/lib/control-plane/client";

const activeKey: ApiKey = {
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
  budget_spend_credits: null,
};

describe("ApiKeyList actions", () => {
  it("offers no rotate link, because no rotate route exists", () => {
    render(<ApiKeyList keys={[activeKey]} canManage />);
    expect(screen.queryByRole("link", { name: /rotate/i })).toBeNull();
    expect(
      document.querySelector(`a[href*="/rotate"]`),
    ).toBeNull();
  });

  it("still offers the revoke action a manager can actually complete", () => {
    render(<ApiKeyList keys={[activeKey]} canManage />);
    expect(screen.getByRole("button", { name: /revoke/i })).toBeTruthy();
  });
});
