// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import AuthPage from "../src/app/auth/page";

describe("google login ui", () => {
  it("renders google login button on auth page", () => {
    render(<AuthPage />);

    const loginBtn = screen.getByRole("button", { name: /continue with google/i });
    expect(loginBtn).toBeInTheDocument();
  });
});
