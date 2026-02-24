// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const pushMock = vi.fn();
const fetchMock = vi.fn();

vi.stubGlobal("fetch", fetchMock);

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: pushMock,
  }),
}));

import BillingPage from "../src/app/billing/page";

describe("billing page", () => {
  beforeEach(() => {
    pushMock.mockReset();
    fetchMock.mockReset();
    fetchMock.mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);

      if (url.includes("/v1/users/me")) {
        return {
          ok: true,
          json: async () => ({
            user: { user_id: "u_1", email: "demo@example.com" },
            credits: { availableCredits: 0, purchasedCredits: 0, promoCredits: 0 },
            api_keys: [],
          }),
        };
      }

      return {
        ok: true,
        json: async () => ({ data: [] }),
      };
    });

    window.localStorage.setItem("bdai.auth.session", JSON.stringify({ apiKey: "sk_live_session", email: "demo@example.com" }));
  });

  afterEach(() => {
    window.localStorage.removeItem("bdai.auth.session");
  });

  it("hydrates from stored session and still shows top-up controls", async () => {
    render(<BillingPage />);

    expect(await screen.findByRole("textbox", { name: /primary api key/i })).toHaveValue("sk_live_session");
    expect(screen.getByRole("button", { name: /load account/i })).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: /top-up amount/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /top up now/i })).toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: /primary api key/i }), { target: { value: "" } });
    expect(pushMock).not.toHaveBeenCalled();
  });
});
