import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

// The component reads GRAFANA_BASE_URL at module scope, so the variable has to be
// set before the module is first imported. Each case resets the module registry
// and imports fresh rather than sharing one top-level import, which is also what
// makes the "even when it is set" guard below meaningful.
async function renderTiles(grafanaBaseURL?: string): Promise<void> {
  vi.resetModules();
  if (grafanaBaseURL === undefined) {
    delete process.env.GRAFANA_BASE_URL;
  } else {
    process.env.GRAFANA_BASE_URL = grafanaBaseURL;
  }
  const { ObservabilityTiles } = await import("./observability-tiles");
  render(<ObservabilityTiles />);
}

afterEach(() => {
  cleanup();
  delete process.env.GRAFANA_BASE_URL;
});

describe("ObservabilityTiles", () => {
  it("keeps the request logs tile pointing at the console route", async () => {
    await renderTiles();

    const link = screen.getByRole("link", { name: /request logs/i });
    expect(link.getAttribute("href")).toBe("/console/logs");
  });

  // The regression guard this change exists for. Analytics carries no role gate,
  // so every member of every tenant renders this section. Grafana on this
  // deployment runs with anonymous Viewer access, its rate-limit dashboard
  // queries `topk(10, sum by (key_id, tier) (...))`, which names API key
  // identifiers across tenants, and its overview dashboard names internal jobs
  // and their memory footprint. Linking any of that from a customer page is the
  // shared-instance exposure family of issues #947, #948 and #949.
  //
  // Setting the variable is exactly what a future reader will try when they see
  // the tiles are dead, so the guard asserts against a *set* variable: this test
  // fails if the Grafana tiles are wired back up, rather than letting the leak
  // ship.
  it("links to no Grafana dashboard even when GRAFANA_BASE_URL is set", async () => {
    await renderTiles("https://grafana.example.invalid");

    for (const link of screen.getAllByRole("link")) {
      const href = link.getAttribute("href") ?? "";
      expect(href).not.toContain("grafana.example.invalid");
      expect(href).not.toContain("/d/hive-platform-overview");
      expect(href).not.toContain("/d/hive-rate-limit");
    }
  });

  it("renders no not-configured dead state", async () => {
    await renderTiles();

    expect(screen.queryByText(/not available on this deployment/i)).toBeNull();
    expect(document.querySelector('[aria-disabled="true"]')).toBeNull();
  });

  // Preserved from the assertion that previously lived in
  // tests/unit/console-mobile-nav.test.tsx: naming a server-side environment
  // variable in product copy leaks a piece of the deployment's internals to
  // every account that opens Analytics, and is not actionable by anyone reading
  // that page.
  it("names no server-side environment variable in customer copy", async () => {
    await renderTiles();

    expect(document.body.textContent ?? "").not.toContain("GRAFANA_BASE_URL");
  });
});
