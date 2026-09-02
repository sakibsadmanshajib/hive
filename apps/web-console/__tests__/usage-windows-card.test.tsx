/**
 * The console's rate-limit consumption surface (issue #1725).
 *
 * Before this card the console showed no rate limit at all: the only bar on
 * the API keys page is credit spend against a budget cap, which reads like a
 * rate limit and is not one. These assertions pin the three states that card
 * has to keep apart, because collapsing any two of them is how a surface ends
 * up claiming a state the system does not have.
 */
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";

import { UsageWindowsCard } from "@/components/usage/usage-windows-card";
import type { UsageWindows } from "@/lib/control-plane/client";

const CONFIGURED: UsageWindows = {
  windows: [
    {
      window: "session",
      configured: true,
      used_percent: 62,
      resets_at: "2026-09-02T18:30:00Z",
      window_seconds: 18000,
      anchored: false,
    },
    {
      window: "weekly",
      configured: false,
      used_percent: 0,
      resets_at: null,
      window_seconds: 604800,
      anchored: true,
    },
  ],
  read_at: "2026-09-02T13:30:00Z",
};

describe("UsageWindowsCard", () => {
  it("shows a configured window as a percentage with its reset time", () => {
    render(<UsageWindowsCard windows={CONFIGURED} />);

    expect(screen.getByTestId("usage-window-session-percent").textContent).toBe(
      "62% used",
    );
    expect(
      screen.getByTestId("usage-window-session-reset").textContent,
    ).toContain("Resets");

    const bar = screen.getByRole("progressbar");
    expect(bar.getAttribute("value")).toBe("62");
  });

  it("says an unconfigured window has no limit rather than drawing an empty bar", () => {
    render(<UsageWindowsCard windows={CONFIGURED} />);

    expect(screen.getByTestId("usage-window-weekly").textContent).toContain(
      "No limit configured",
    );
    // One bar only: the weekly window has no limit, and a bar for it would
    // claim a limit this account does not have.
    expect(screen.getAllByRole("progressbar")).toHaveLength(1);
  });

  it("reports unavailable counters as unavailable, never as zero usage", () => {
    render(<UsageWindowsCard windows={null} />);

    expect(screen.getByText(/unavailable right now/i)).toBeTruthy();
    expect(screen.queryAllByRole("progressbar")).toHaveLength(0);
  });

  it("never renders a currency figure or an absolute allowance", () => {
    const { container } = render(<UsageWindowsCard windows={CONFIGURED} />);
    const text = container.textContent ?? "";

    // The allowance is a credit score, and credits convert to dollars by a
    // constant this console publishes, so an absolute figure here would
    // disclose the confidential internal value of a plan (D-068, D-070).
    expect(text).not.toContain("$");
    expect(text.toLowerCase()).not.toContain("usd");
    expect(text.toLowerCase()).not.toContain("credit");
  });
});
