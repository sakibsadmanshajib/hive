// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import BillingPage from "../src/app/billing/page";

describe("billing page", () => {
  it("shows account loading and top-up controls", () => {
    render(<BillingPage />);

    expect(screen.getByRole("textbox", { name: /primary api key/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /load account/i })).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: /top-up amount/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /top up now/i })).toBeInTheDocument();
  });
});
