// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
  }),
}));

import HomePage from "../src/app/page";

describe("chat workspace layout", () => {
  beforeEach(() => {
    window.localStorage.setItem(
      "bdai.auth.session",
      JSON.stringify({ apiKey: "sk_test", email: "demo@example.com" }),
    );
  });

  it("renders left-rail trigger, composer, and profile menu trigger", () => {
    render(<HomePage />);

    expect(screen.getAllByRole("button", { name: /new chat/i }).length).toBeGreaterThan(0);
    expect(screen.getByPlaceholderText(/ask something/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /open profile menu/i })).toHaveAttribute("aria-haspopup", "menu");
  });
});
