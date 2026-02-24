// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import BillingPage from "../src/app/billing/page";

describe("billing page", () => {
  it("shows migration links to settings and developer panel", () => {
    render(<BillingPage />);

    expect(screen.getByRole("heading", { name: /billing moved to settings/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /open settings/i })).toHaveAttribute("href", "/settings");
    expect(screen.getByRole("link", { name: /open developer panel/i })).toHaveAttribute("href", "/developer");
  });
});
