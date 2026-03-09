// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const pushMock = vi.fn();
const routerMock = { push: pushMock };

vi.mock("next/navigation", () => ({
  useRouter: () => routerMock,
}));

vi.mock("../src/features/settings/user-settings-panel", () => ({
  UserSettingsPanel: () => <div>Mock settings panel</div>,
}));

import DeveloperPage from "../src/app/developer/page";
import SettingsPage from "../src/app/settings/page";

describe("review feedback pages", () => {
  afterEach(() => {
    cleanup();
  });

  beforeEach(() => {
    vi.restoreAllMocks();
    pushMock.mockReset();
    window.localStorage.setItem("bdai.auth.session", JSON.stringify({ accessToken: "sk_test", email: "demo@example.com" }));
  });


  it("shows a status message when developer usage loading throws", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({ user: { user_id: "u1", email: "demo@example.com" }, credits: { availableCredits: 1, purchasedCredits: 1, promoCredits: 0 }, api_keys: [] }) })
      .mockRejectedValueOnce(new Error("usage failed"));
    vi.stubGlobal("fetch", fetchMock);

    render(<DeveloperPage />);

    const loadUsageButtons = await screen.findAllByRole("button", { name: /load usage/i });
    fireEvent.click(loadUsageButtons[0]);

    await waitFor(() => {
      expect(screen.getByText(/usage failed/i)).toBeInTheDocument();
    });
  });

  it("shows a status message when top-up request fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("top up failed")));

    render(<SettingsPage />);

    fireEvent.click(await screen.findByRole("button", { name: /top up now/i }));

    await waitFor(() => {
      expect(screen.getByText(/top up failed/i)).toBeInTheDocument();
    });
  });

  it("falls back to zero credits when top-up response omits minted credits", async () => {
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce({ ok: true, json: async () => ({ intent_id: "intent_1" }) })
        .mockResolvedValueOnce({ ok: true, json: async () => ({}) }),
    );

    render(<SettingsPage />);

    fireEvent.click(await screen.findByRole("button", { name: /top up now/i }));

    await waitFor(() => {
      expect(screen.getByText(/top-up successful \(\+0 credits\)/i)).toBeInTheDocument();
    });
  });

  it("shows a user-facing read-only profile message", async () => {
    render(<SettingsPage />);

    expect(await screen.findByText(/profile fields are read-only in this release\./i)).toBeInTheDocument();
  });
});
