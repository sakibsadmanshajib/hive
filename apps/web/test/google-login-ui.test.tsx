// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import ChatPage from "../src/app/chat/page";

describe("google login ui", () => {
  it("renders google login button in chat auth area", () => {
    render(<ChatPage />);

    const loginLink = screen.getByRole("link", { name: /continue with google/i });
    expect(loginLink).toBeInTheDocument();
    expect(loginLink).toHaveAttribute("href", "http://127.0.0.1:8080/v1/auth/google/start");
  });
});
