// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ThemeProvider, useTheme } from "../src/components/theme/theme-provider";

function ThemeProbe() {
  const { resolvedTheme, setTheme } = useTheme();

  return (
    <div>
      <p>theme:{resolvedTheme}</p>
      <button onClick={() => setTheme("dark")} type="button">
        Set dark
      </button>
    </div>
  );
}

describe("ThemeProvider", () => {
  beforeEach(() => {
    window.localStorage.clear();
    Object.defineProperty(window, "matchMedia", {
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: query.includes("dark"),
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
  });

  it("applies and persists selected theme", () => {
    render(
      <ThemeProvider>
        <ThemeProbe />
      </ThemeProvider>,
    );

    expect(document.documentElement.classList.contains("light") || document.documentElement.classList.contains("dark")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: /set dark/i }));
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(window.localStorage.getItem("bd-ai-theme")).toBe("dark");
  });
});
