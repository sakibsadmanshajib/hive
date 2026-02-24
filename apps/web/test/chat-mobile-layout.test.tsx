// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: vi.fn(),
  }),
}));

import ChatPage from "../src/app/chat/page";

describe("chat page", () => {
  beforeEach(() => {
    window.localStorage.setItem(
      "bdai.auth.session",
      JSON.stringify({ apiKey: "sk_test", email: "demo@example.com" }),
    );
  });

  it("renders new chat action and message input", () => {
    render(<ChatPage />);

    expect(screen.getAllByRole("button", { name: /new chat/i }).length).toBeGreaterThan(0);
    expect(screen.getByPlaceholderText(/ask something/i)).toBeInTheDocument();
  });
});
