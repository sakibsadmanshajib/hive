import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";

// The component this guards used to read GRAFANA_BASE_URL at module scope, so a
// re-wiring would too. Each case therefore resets the module registry and
// imports fresh, rather than sharing one top-level import, so that a
// module-scope environment read is actually re-evaluated per case and the
// "even when it is set" guard below is not vacuous.
async function renderTiles(env: Record<string, string> = {}): Promise<void> {
  vi.resetModules();
  for (const [key, value] of Object.entries(env)) {
    process.env[key] = value;
  }
  const { ObservabilityTiles } = await import("./observability-tiles");
  render(<ObservabilityTiles />);
}

// Names a re-wiring might plausibly use. The guards below are deliberately NOT
// keyed to any of them, but setting them all proves the component ignores the
// whole family rather than one literal.
const DASHBOARD_ENV_NAMES = [
  "GRAFANA_BASE_URL",
  "NEXT_PUBLIC_GRAFANA_URL",
  "GRAFANA_URL",
  "OBSERVABILITY_BASE_URL",
] as const;

const ALL_SET = Object.fromEntries(
  DASHBOARD_ENV_NAMES.map((name) => [name, "https://grafana.example.invalid"]),
);

function renderedHrefs(): string[] {
  return screen.getAllByRole("link").map((link) => link.getAttribute("href") ?? "");
}

afterEach(() => {
  cleanup();
  for (const name of DASHBOARD_ENV_NAMES) {
    delete process.env[name];
  }
});

describe("ObservabilityTiles", () => {
  // The load-bearing guard, and the reason it asserts the exact link set rather
  // than the absence of a particular substring: a substring check keyed to
  // "grafana" or to one variable name would pass a re-wiring that used a
  // different host or a different variable. An exact set fails on any added
  // link at all, whatever it points at and whatever configures it.
  //
  // Why this matters rather than being tidiness. Analytics carries no role gate,
  // so every member of every tenant renders this section. The Grafana instance
  // this repo ships runs with anonymous Viewer access, its rate-limit dashboard
  // queries `topk(10, sum by (key_id, tier) (...))`, which names API key
  // identifiers across tenants, and its overview dashboard names internal jobs
  // and their memory footprint. Captured live against the deployed console with
  // GRAFANA_BASE_URL set: the pre-change component rendered two live Grafana
  // links to an ordinary non-admin account.
  it("renders exactly one link, to the console request logs, whatever dashboard variables are set", async () => {
    await renderTiles(ALL_SET);

    expect(renderedHrefs()).toEqual(["/console/logs"]);
  });

  it("renders the same single link when no dashboard variable is set at all", async () => {
    await renderTiles();

    expect(renderedHrefs()).toEqual(["/console/logs"]);
  });

  // Independent of the set assertion above: catches a link-out added outside a
  // role gate even if it somehow kept the same count.
  it("renders no link that leaves this application", async () => {
    await renderTiles(ALL_SET);

    for (const href of renderedHrefs()) {
      expect(href.startsWith("/")).toBe(true);
    }
  });

  // The removed tiles rendered a permanent "not available on this deployment"
  // card, which is the dead state this change exists to remove from every
  // customer's Analytics page. Goes red if a disabled tile returns.
  it("renders no disabled or not-configured tile", async () => {
    await renderTiles();

    expect(screen.queryByText(/not available on this deployment/i)).toBeNull();
    expect(document.querySelector('[aria-disabled="true"]')).toBeNull();
  });

  // Carried forward from tests/unit/console-mobile-nav.test.tsx. Generalised
  // from the single GRAFANA_BASE_URL literal to any SCREAMING_SNAKE token, so it
  // still has a subject now that the old disabled branch is gone: naming a
  // server-side variable in product copy leaks a piece of the deployment's
  // internals to every account that opens Analytics, and is not actionable by
  // anyone reading that page.
  it("names no server-side environment variable in customer copy", async () => {
    await renderTiles(ALL_SET);

    const copy = document.body.textContent ?? "";
    expect(copy).not.toMatch(/\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+){2,}\b/);
  });
});
