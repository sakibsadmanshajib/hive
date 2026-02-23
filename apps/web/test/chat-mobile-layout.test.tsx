// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import ChatPage from "../src/app/chat/page";

describe("chat page", () => {
  it("renders new chat action and message input", () => {
    render(<ChatPage />);

    expect(screen.getAllByRole("button", { name: /new chat/i }).length).toBeGreaterThan(0);
    expect(screen.getByPlaceholderText(/ask something/i)).toBeInTheDocument();
  });
});
