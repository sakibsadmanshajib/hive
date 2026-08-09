// Unit-level guards for the interaction coverage gate itself.
//
// These run in the ordinary web-unit job, with no stack and no browser, so a
// malformed registry entry, a stale route fixture, or an enumerator that
// stopped finding controls fails in seconds rather than surviving until
// somebody happens to run the browser gate.

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

import {
  APP_DIR,
  REGISTRY_FILE,
  ROUTE_FIXTURE_FILE,
  WEB_CONSOLE_DIR,
} from "./lib/config";
import { enumerateInPage } from "./lib/enumerate";
import { controlKey } from "./lib/key";
import { indexRegistry, parseRegistry, validateRegistry } from "./lib/registry";
import {
  discoverRoutes,
  isDynamic,
  loadRouteFixtures,
  patternForPageFile,
  staleFixtureRoutes,
} from "./lib/routes";

describe("control registry", () => {
  it("parses and every entry carries a justification, an owner and real proof", () => {
    const registry = parseRegistry(readFileSync(REGISTRY_FILE, "utf8"));
    expect(validateRegistry(registry, WEB_CONSOLE_DIR)).toEqual([]);
  });

  it("rejects an entry with an empty justification", () => {
    const registry = parseRegistry(
      JSON.stringify({
        entries: [{ route: "/console", control: "button|X", kind: "inert", reason: "", owner: "@someone" }],
      }),
    );
    expect(validateRegistry(registry, WEB_CONSOLE_DIR).join(" ")).toContain(
      "has no justification",
    );
  });

  it("rejects an entry with no owner", () => {
    const registry = parseRegistry(
      JSON.stringify({
        entries: [
          {
            route: "/console",
            control: "button|X",
            kind: "inert",
            reason: "purely decorative chrome with no behaviour at all",
            owner: "",
          },
        ],
      }),
    );
    expect(validateRegistry(registry, WEB_CONSOLE_DIR).join(" ")).toContain("has no owner");
  });

  it("rejects a covered_elsewhere claim whose spec does not mention the marker", () => {
    const registry = parseRegistry(
      JSON.stringify({
        entries: [
          {
            route: "/console",
            control: "button|X",
            kind: "covered_elsewhere",
            reason: "activating it live would revoke the demo workspace key",
            owner: "@someone",
            spec: "tests/e2e/unauth.spec.ts",
            marker: "a string that spec certainly does not contain 9f3c",
          },
        ],
      }),
    );
    expect(validateRegistry(registry, WEB_CONSOLE_DIR).join(" ")).toContain("never mentions");
  });

  it("reports entries that no run matched, so the registry cannot rot", () => {
    const registry = parseRegistry(
      JSON.stringify({
        entries: [
          {
            route: "/console",
            control: "button|Gone",
            kind: "inert",
            reason: "this control was deleted from the app months ago",
            owner: "@someone",
          },
        ],
      }),
    );
    const index = indexRegistry(registry);
    expect(index.unmatched()).toHaveLength(1);
    expect(index.lookup("/console", "button|Gone")).toBeDefined();
    expect(index.unmatched()).toHaveLength(0);
  });
});

describe("route discovery", () => {
  it("derives routes from app/**/page.tsx rather than from a list", () => {
    const fixtures = loadRouteFixtures(ROUTE_FIXTURE_FILE);
    const routes = discoverRoutes(APP_DIR, fixtures);
    expect(routes.length).toBeGreaterThan(10);
    expect(routes.map((r) => r.pattern)).toContain("/console");
    expect(routes.map((r) => r.pattern)).toContain("/console/api-keys");
    expect(routes.map((r) => r.pattern)).toContain("/auth/sign-in");
  });

  it("strips route groups and parallel slots from the URL", () => {
    expect(patternForPageFile("(marketing)/pricing/page.tsx")).toBe("/pricing");
    expect(patternForPageFile("console/api-keys/[id]/limits/page.tsx")).toBe(
      "/console/api-keys/[id]/limits",
    );
    expect(isDynamic("/console/api-keys/[id]/limits")).toBe(true);
  });

  it("has no fixture entry pointing at a route that no longer exists", () => {
    const fixtures = loadRouteFixtures(ROUTE_FIXTURE_FILE);
    const routes = discoverRoutes(APP_DIR, fixtures);
    expect(staleFixtureRoutes(fixtures, routes)).toEqual([]);
  });
});

describe("enumerator", () => {
  function render(html: string): void {
    document.body.innerHTML = html;
    // jsdom has no layout, so every rect is zero and the rendered-visibility
    // filter would drop everything. Give elements a box unless the markup
    // explicitly hides them.
    Element.prototype.getBoundingClientRect = function rect(this: Element): DOMRect {
      const hidden =
        this instanceof HTMLElement &&
        (this.style.display === "none" || this.hasAttribute("data-zero-size"));
      const size = hidden ? 0 : 20;
      return {
        x: 0,
        y: 0,
        width: size,
        height: size,
        top: 0,
        left: 0,
        right: size,
        bottom: size,
        toJSON: () => ({}),
      };
    };
  }

  it("finds buttons, links, fields, toggles and role-based controls", () => {
    render(`
      <a href="/console">Overview</a>
      <button type="button" aria-haspopup="menu">Workspace</button>
      <input id="email" name="email" type="email" />
      <div role="switch" aria-checked="false">Enable audit export</div>
      <span role="tab">Usage</span>
    `);
    const { controls } = enumerateInPage();
    const keys = controls.map(controlKey);
    expect(keys).toContain("a|Overview");
    expect(keys).toContain("button|Workspace");
    expect(keys).toContain("input|#email");
    expect(keys).toContain("div[role=switch]|Enable audit export");
    expect(keys).toContain("span[role=tab]|Usage");
    expect(controls.find((c) => c.role === "switch")?.kind).toBe("toggle");
    expect(controls.find((c) => c.tag === "input")?.kind).toBe("textfield");
  });

  it("does not count a label as a second control for the input it labels", () => {
    render(`<label for="rpm">Requests per minute</label><input id="rpm" name="rpm" />`);
    const { controls } = enumerateInPage();
    expect(controls.map((c) => c.tag)).toEqual(["input"]);
  });

  it("skips elements that are not rendered", () => {
    render(`<button style="display:none">Hidden</button><button>Shown</button>`);
    const { controls } = enumerateInPage();
    expect(controls.map((c) => c.name)).toEqual(["Shown"]);
  });

  it("stamps a stable index that can re-locate the element", () => {
    render(`<button>One</button><button>Two</button>`);
    const { controls } = enumerateInPage();
    const two = controls.find((c) => c.name === "Two");
    expect(two).toBeDefined();
    expect(
      document.querySelector(`[data-ic-idx="${String(two?.idx)}"]`)?.textContent,
    ).toBe("Two");
  });

  it("distinguishes duplicate names with an ordinal so keys stay unique", () => {
    render(`<button>Save</button><button>Save</button>`);
    const keys = enumerateInPage().controls.map(controlKey);
    expect(new Set(keys).size).toBe(2);
    expect(keys).toEqual(["button|Save", "button|Save~1"]);
  });
});
