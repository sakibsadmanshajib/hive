// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const pushMock = vi.fn();

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: pushMock,
  }),
}));

import BillingPage from "../src/app/billing/page";

describe("billing page", () => {
  beforeEach(() => {
    pushMock.mockReset();
    window.localStorage.setItem("bdai.auth.session", JSON.stringify({ apiKey: "sk_live_session", email: "demo@example.com" }));
  });

  it("hydrates from stored session and still shows top-up controls", () => {
    render(<BillingPage />);

    expect(screen.getByRole("textbox", { name: /primary api key/i })).toHaveValue("sk_live_session");
    expect(screen.getByRole("button", { name: /load account/i })).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: /top-up amount/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /top up now/i })).toBeInTheDocument();

    fireEvent.change(screen.getByRole("textbox", { name: /primary api key/i }), { target: { value: "" } });
    expect(pushMock).not.toHaveBeenCalled();
  });
});
