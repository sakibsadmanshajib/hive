// @vitest-environment jsdom

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { AppHeader } from "../src/components/layout/app-header";
import { AppSidebar } from "../src/components/layout/app-sidebar";
import { ThemeProvider } from "../src/components/theme/theme-provider";

describe("AppHeader", () => {
  it("shows Developer Panel and Settings actions", () => {
    render(
      <ThemeProvider>
        <AppHeader />
      </ThemeProvider>,
    );

    expect(screen.getByRole("link", { name: /developer panel/i })).toHaveAttribute("href", "/developer");
    expect(screen.getByRole("link", { name: /settings/i })).toHaveAttribute("href", "/settings");
  });

  it("keeps chat as primary nav and removes old top-level Billing/Auth links", () => {
    render(<AppSidebar />);

    expect(screen.getByRole("link", { name: /chat/i })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /^billing$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /^auth$/i })).not.toBeInTheDocument();
  });

  it("keeps dedicated modules for root workspace and legacy chat redirect", async () => {
    const [homePageModule, chatPageModule] = await Promise.all([import("../src/app/page"), import("../src/app/chat/page")]);

    expect(homePageModule.default).toBeTypeOf("function");
    expect(chatPageModule.default).toBeTypeOf("function");
    expect(homePageModule.default).not.toBe(chatPageModule.default);
  });

  it("provides a developer route module", async () => {
    const developerPageModule = await import("../src/app/developer/page");
    expect(developerPageModule.default).toBeTypeOf("function");
  });

  it("provides a settings route module", async () => {
    const settingsPageModule = await import("../src/app/settings/page");
    expect(settingsPageModule.default).toBeTypeOf("function");
  });
});
