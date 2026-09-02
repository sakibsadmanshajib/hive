import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { NextIntlClientProvider } from "next-intl";

import enMessages from "@/messages/en.json";

vi.mock("@/components/locale-switcher", () => ({
  LocaleSwitcher: () => null,
}));

const viewerMock = vi.fn();
const profileMock = vi.fn();
const balanceMock = vi.fn();
const usageMock = vi.fn();
const errorsMock = vi.fn();

vi.mock("@/lib/control-plane/client", () => ({
  getViewer: () => viewerMock(),
  getAccountProfile: () => profileMock(),
  getBalance: () => balanceMock(),
  getAnalyticsUsage: () => usageMock(),
  getAnalyticsErrors: () => errorsMock(),
}));

// ConsolePage is an async Server Component; awaiting the call resolves the
// full tree so plain RTL queries work with no request-scoped plumbing.
import ConsolePage from "@/app/console/page";
import type { AccountProfile, Viewer } from "@/lib/control-plane/client";

const viewer: Viewer = {
  user: { id: "u1", email: "demo@hive.invalid", email_verified: true },
  current_account: {
    id: "acct_1",
    display_name: "Hive Demo",
    slug: "hive-demo",
    account_type: "team",
    role: "owner",
  },
  memberships: [],
  permissions: [],
  workspace_admin: false,
};

const profile: AccountProfile = {
  owner_name: "Demo Owner",
  login_email: "demo@hive.invalid",
  display_name: "Hive Demo",
  account_type: "team",
  country_code: "BD",
  state_region: "",
  profile_setup_complete: true,
};

async function renderOverview() {
  viewerMock.mockResolvedValue(viewer);
  profileMock.mockResolvedValue(profile);
  balanceMock.mockResolvedValue({
    available_credits: 0,
    posted_credits: 0,
    reserved_credits: 0,
  });
  const element = await ConsolePage();
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      {element}
    </NextIntlClientProvider>,
  );
}

describe("Console overview empty states", () => {
  it("shows a real empty state for Today's activity when there is no usage in the window", async () => {
    usageMock.mockResolvedValue([]);
    errorsMock.mockResolvedValue([]);
    await renderOverview();
    expect(
      screen.getByText("No requests in the last 24 hours."),
    ).toBeTruthy();
  });

  it("shows a real empty state for Recent errors with a link into the request log", async () => {
    usageMock.mockResolvedValue([]);
    errorsMock.mockResolvedValue([]);
    await renderOverview();
    expect(screen.getByText("No errors recorded.")).toBeTruthy();
    const cta = screen.getByRole("link", { name: /the request log/i });
    expect(cta.getAttribute("href")).toBe("/console/logs");
  });

  it("keeps the Analytics CTA on the activity card", async () => {
    usageMock.mockResolvedValue([]);
    errorsMock.mockResolvedValue([]);
    await renderOverview();
    // The sidebar also carries an "Analytics" nav link; scope to the card's
    // own sentence so the assertion cannot pass off navigation chrome alone.
    const note = screen.getByText(/Detailed counts available in/);
    const cta = note.closest("p")?.querySelector('a[href="/console/analytics"]');
    expect(cta).toBeTruthy();
  });

  it("renders the real request and token counts when usage exists, proving the card is wired to live data rather than a static empty string", async () => {
    usageMock.mockResolvedValue([
      {
        group_key: "hive-default",
        total_input_tokens: 1200,
        total_output_tokens: 300,
        total_credits_spent: 45,
        request_count: 7,
      },
      {
        group_key: "hive-fast",
        total_input_tokens: 500,
        total_output_tokens: 100,
        total_credits_spent: 10,
        request_count: 3,
      },
    ]);
    errorsMock.mockResolvedValue([]);
    await renderOverview();
    expect(screen.getByText("10")).toBeTruthy(); // 7 + 3 requests
    expect(screen.getByText("2,100")).toBeTruthy(); // 1200+300+500+100 tokens
    expect(screen.queryByText("No requests in the last 24 hours.")).toBeNull();
  });

  it("renders the real error count and a filtered log link when errors exist, proving the card is wired to live data rather than a static empty string", async () => {
    usageMock.mockResolvedValue([]);
    errorsMock.mockResolvedValue([
      {
        group_key: "key_1",
        error_count: 4,
        total_requests: 20,
        error_rate: 0.2,
      },
    ]);
    await renderOverview();
    expect(screen.getByText("4")).toBeTruthy();
    const cta = screen.getByRole("link", { name: /view in the request log/i });
    expect(cta.getAttribute("href")).toBe("/console/logs?errors=true&window=24h");
    expect(screen.queryByText("No errors recorded.")).toBeNull();
  });

  it("shows a distinct unavailable state for Today's activity on a fetch failure, never the empty-state copy", async () => {
    usageMock.mockRejectedValue(new Error("upstream 500"));
    errorsMock.mockResolvedValue([]);
    await renderOverview();
    expect(screen.getByText("Couldn’t load activity.")).toBeTruthy();
    expect(screen.queryByText("No requests in the last 24 hours.")).toBeNull();
  });

  it("shows a distinct unavailable state for Recent errors on a fetch failure, never the empty-state copy", async () => {
    usageMock.mockResolvedValue([]);
    errorsMock.mockRejectedValue(new Error("upstream 500"));
    await renderOverview();
    expect(screen.getByText("Couldn’t load error data.")).toBeTruthy();
    expect(screen.queryByText("No errors recorded.")).toBeNull();
  });
});
