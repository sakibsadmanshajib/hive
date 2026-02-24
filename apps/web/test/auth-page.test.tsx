// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import AuthPage from "../src/app/auth/page";

describe("AuthPage", () => {
  it("renders login and register forms with google sign in", () => {
    render(<AuthPage />);

    expect(screen.getByRole("heading", { name: /welcome back/i })).toBeInTheDocument();
    expect(screen.getByText("Register")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /continue with google/i })).toBeInTheDocument();
  });
});
