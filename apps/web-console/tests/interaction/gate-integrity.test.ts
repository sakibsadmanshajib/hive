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
  ROUTE_FLOOR_FILE,
  WEB_CONSOLE_DIR,
} from "./lib/config";
import {
  collectExclusions,
  exclusionProblems,
  expiredExclusions,
  type Exclusion,
} from "./lib/exclusions";
import { floorProblems, loadFloors, staleFloors, type FloorFile } from "./lib/floors";
import { enumerateInPage, type RawControl } from "./lib/enumerate";
import {
  redactUrl,
  verdictForDisabled,
  verdictFromObservation,
  type Observation,
} from "./lib/prove";
import { controlKey } from "./lib/key";
import { indexRegistry, parseRegistry, validateRegistry } from "./lib/registry";
import { ratio } from "./lib/report";
import {
  discoverRoutes,
  isDynamic,
  loadRouteFixtures,
  patternForPageFile,
  staleFixtureRoutes,
} from "./lib/routes";

/** A control with every field at its empty value, to vary one at a time. */
const blankControl: RawControl = {
  idx: 0,
  tag: "button",
  role: "",
  type: "",
  name: "Apply",
  href: "",
  testid: "",
  id: "",
  fieldName: "",
  kind: "button",
  matchedBy: "selector",
  disabled: false,
  disabledReason: "",
  ariaChecked: "",
  ariaExpanded: "",
  ariaHasPopup: "",
  inForm: false,
  formAction: "",
  formMethod: "",
  ordinal: 0,
  depth: 0,
};

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

  it("counts duplicates of the key itself, not of some other tuple", () => {
    // Two links with the same name and different hrefs. The ordinal used to be
    // counted on tag/role/name/href, so both took ordinal 0, and controlKey
    // prefers the name over the href, so both came out as `a|Overview`. A
    // collided key makes one of the two unaddressable for the registry, the
    // re-locator and the report.
    render(`<a href="/console">Overview</a><a href="/console/analytics">Overview</a>`);
    const keys = enumerateInPage().controls.map(controlKey);
    expect(keys).toEqual(["a|Overview", "a|Overview~1"]);
  });
});

describe("URLs written into the ledger", () => {
  it("drops the fragment and redacts credential-shaped query parameters", () => {
    expect(redactUrl("https://console.example/auth/callback#access_token=abc.def")).toBe(
      "https://console.example/auth/callback",
    );
    expect(redactUrl("https://console.example/invitations/accept?token=live-secret")).toBe(
      "https://console.example/invitations/accept?token=REDACTED",
    );
    expect(redactUrl("https://console.example/console/billing?page=2")).toBe(
      "https://console.example/console/billing?page=2",
    );
  });
});

describe("denominator floor", () => {
  const floors: FloorFile = {
    shell: { prefix: "/console", require: ["a|Overview", "button|Sign out"] },
    routes: { "/auth/sign-in": ["button|Continue"] },
  };

  it("fails a route that did not render a required control", () => {
    const problems = floorProblems(floors, [
      { route: "/console/billing", visited: true, enabled: ["a|Overview"], disabled: [] },
    ]);
    expect(problems.join(" ")).toContain('did not render required control "button|Sign out"');
  });

  it("fails a required control that rendered disabled", () => {
    const problems = floorProblems(floors, [
      {
        route: "/console/billing",
        visited: true,
        enabled: ["a|Overview"],
        disabled: ["button|Sign out"],
      },
    ]);
    expect(problems.join(" ")).toContain("disabled");
  });

  it("fails a visited route that rendered no control at all", () => {
    const problems = floorProblems(floors, [
      { route: "/console", visited: true, enabled: [], disabled: [] },
    ]);
    expect(problems.join(" ")).toContain("rendered no interactive control at all");
  });

  it("fails a visited route nothing requires anything of", () => {
    const problems = floorProblems(floors, [
      { route: "/somewhere-new", visited: true, enabled: ["button|Go"], disabled: [] },
    ]);
    expect(problems.join(" ")).toContain("has no entry in route-floors.json");
  });

  it("passes a route that rendered everything required of it", () => {
    expect(
      floorProblems(floors, [
        {
          route: "/console/billing",
          visited: true,
          enabled: ["a|Overview", "button|Sign out", "input|#threshold"],
          disabled: [],
        },
        {
          route: "/auth/sign-in",
          visited: true,
          enabled: ["button|Continue"],
          disabled: [],
        },
      ]),
    ).toEqual([]);
  });

  it("says nothing about a route the run never visited", () => {
    expect(
      floorProblems(floors, [
        { route: "/console", visited: false, enabled: [], disabled: [] },
      ]),
    ).toEqual([]);
  });

  it("reports a floor for a route that no longer exists", () => {
    expect(
      staleFloors({ shell: { prefix: "/console", require: [] }, routes: { "/gone": ["a|X"] } }, [
        "/console",
      ]),
    ).toEqual(["/gone"]);
  });

  it("the committed floors name only real routes", () => {
    const fixtures = loadRouteFixtures(ROUTE_FIXTURE_FILE);
    const patterns = discoverRoutes(APP_DIR, fixtures).map((r) => r.pattern);
    expect(staleFloors(loadFloors(ROUTE_FLOOR_FILE), patterns)).toEqual([]);
  });
});

describe("coverage arithmetic", () => {
  it("scores an empty measurement as zero, never as full marks", () => {
    // /oauth/consent shipped with a recorded floor of zero controls and scored
    // 100%: nothing had ever rendered there, so nothing could ever fail.
    expect(ratio(0, 0)).toBe(0);
    expect(ratio(3, 4)).toBe(0.75);
  });
});

describe("exclusion expiry", () => {
  const realExclusions = (): Exclusion[] =>
    collectExclusions(
      loadRouteFixtures(ROUTE_FIXTURE_FILE),
      parseRegistry(readFileSync(REGISTRY_FILE, "utf8")),
    );

  it("rejects a skip that names neither an issue nor a standing decision", () => {
    const problems = exclusionProblems([
      { what: "skip of /x", issue: null, permanent: false, owner: "@someone" },
    ]);
    expect(problems.join(" ")).toContain("names no blocker");
  });

  it("rejects an exclusion claiming to be both permanent and blocked", () => {
    const problems = exclusionProblems([
      { what: "skip of /x", issue: 42, permanent: true, owner: "@someone" },
    ]);
    expect(problems.join(" ")).toContain("one or the other");
  });

  it("expires an exclusion whose blocking issue has closed", () => {
    const expired = expiredExclusions(
      [{ what: "skip of /x", issue: 766, permanent: false, owner: "@someone" }],
      new Set([766]),
    );
    expect(expired.join(" ")).toContain("that issue is closed");
  });

  it("every committed exclusion is either permanent or names an open issue", () => {
    expect(exclusionProblems(realExclusions())).toEqual([]);
  });
});

// The only check here that reaches the network. An exclusion expires when the
// thing blocking it is fixed, and the only authority on that is the issue
// tracker. Without a token (a local run, a fork PR) the state cannot be read,
// so the check reports that it could not run rather than passing silently.
describe("exclusion blockers are still open", () => {
  const token = process.env.GITHUB_TOKEN ?? process.env.GH_TOKEN ?? "";
  const repo = process.env.GITHUB_REPOSITORY ?? "sakibsadmanshajib/hive";

  it(
    "no exclusion cites an issue that has already closed",
    async () => {
      if (token === "") {
        // In CI a missing token is a broken check, not an absent one: the job
        // is supposed to supply it, and `it.runIf` used to turn that into a
        // silent skip, so this never ran anywhere. Locally there is nothing to
        // supply it, and requiring a personal token to run the unit suite is
        // not a trade worth making.
        if (process.env.CI) {
          throw new Error(
            "GITHUB_TOKEN is not set, so exclusion expiry cannot be checked; the workflow must pass it (see the web-unit job)",
          );
        }
        return;
      }
      const exclusions = collectExclusions(
        loadRouteFixtures(ROUTE_FIXTURE_FILE),
        parseRegistry(readFileSync(REGISTRY_FILE, "utf8")),
      );
      const cited = [
        ...new Set(
          exclusions
            .map((exclusion) => exclusion.issue)
            .filter((issue): issue is number => issue !== null),
        ),
      ];
      const closed = new Set<number>();
      for (const issue of cited) {
        const response = await fetch(
          `https://api.github.com/repos/${repo}/issues/${String(issue)}`,
          { headers: { authorization: `Bearer ${token}`, accept: "application/vnd.github+json" } },
        );
        if (!response.ok) {
          // A tracker that will not answer leaves the question unanswered, and
          // an unanswered question must not read as a clean bill of health.
          throw new Error(
            `could not read issue ${String(issue)} from ${repo} (HTTP ${String(response.status)}), so whether this exclusion has expired is unknown`,
          );
        }
        const body: unknown = await response.json();
        if (
          typeof body === "object" &&
          body !== null &&
          "state" in body &&
          body.state === "closed"
        ) {
          closed.add(issue);
        }
      }
      expect(expiredExclusions(exclusions, closed)).toEqual([]);
    },
    30_000,
  );
});

// Rot the browser sweep cannot be trusted to find, because whether a declared
// control renders at all depends on the account's data. This asks the only
// question that is environment independent, and it asks it in the required
// unit job.
describe("declared controls name routes that exist", () => {
  it("every registry entry points at a real route, or at the shell", () => {
    const registry = parseRegistry(readFileSync(REGISTRY_FILE, "utf8"));
    const patterns = new Set(
      discoverRoutes(APP_DIR, loadRouteFixtures(ROUTE_FIXTURE_FILE)).map((r) => r.pattern),
    );
    const orphans = registry.entries
      .filter((entry) => entry.route !== "*" && !patterns.has(entry.route))
      .map((entry) => `${entry.route} :: ${entry.control}`);
    expect(orphans).toEqual([]);
  });
});

// The predicate that decides every verdict in the ledger. A live run can go a
// full sweep without meeting a single failing control, so these are the checks
// that keep the failure branches from quietly rotting into unreachable code.
describe("proof predicate", () => {
  const base: Observation = {
    urlBefore: "https://console.example/console",
    urlAfter: "https://console.example/console",
    urlChanged: false,
    requests: [],
    domChanged: false,
    domStableBaseline: true,
    popup: false,
    download: "",
    dialog: "",
    consoleErrors: [],
    actError: "",
    navStatus: null,
    failedRequests: [],
    errorSurfaces: [],
  };

  it("proves an activation that issues a request", () => {
    const verdict = verdictFromObservation({ ...base, requests: ["POST /api/keys"] });
    expect(verdict.proven).toBe(true);
    expect(verdict.proofType).toBe("network");
  });

  it("refuses to prove a 500 that also changed the render", () => {
    const verdict = verdictFromObservation({
      ...base,
      requests: ["POST /api/keys"],
      domChanged: true,
      failedRequests: ["500 POST /api/keys"],
    });
    expect(verdict.proven).toBe(false);
    expect(verdict.proofType).toBe("broken-endpoint");
  });

  it("refuses to prove an activation whose only visible result is an error", () => {
    const verdict = verdictFromObservation({
      ...base,
      requests: ["POST /api/keys"],
      domChanged: true,
      errorSurfaces: ["Something went wrong, please try again"],
    });
    expect(verdict.proven).toBe(false);
    expect(verdict.proofType).toBe("error-surface");
  });
});

describe("markup is not behaviour", () => {
  it("never proves a disabled control, whatever its markup says", () => {
    // A title attribute used to be enough. That made "disable it" the cheapest
    // way to pass this gate, and a permission regression that greys out a page
    // would have kept it green.
    const withTitle = verdictForDisabled({
      ...blankControl,
      disabled: true,
      disabledReason: "You do not have permission",
    });
    expect(withTitle.proven).toBe(false);
    expect(withTitle.proofType).toBe("disabled");

    const bare = verdictForDisabled({ ...blankControl, disabled: true });
    expect(bare.proven).toBe(false);
    expect(bare.proofType).toBe("disabled");
    expect(bare.detail).toContain("no reason exposed");
  });
});

describe("blocked writes still prove the control", () => {
  const base: Observation = {
    urlBefore: "https://console.example/console/api-keys",
    urlAfter: "https://console.example/console/api-keys",
    urlChanged: false,
    requests: ["POST /api/v1/accounts/current/api-keys"],
    domChanged: false,
    domStableBaseline: true,
    popup: false,
    download: "",
    dialog: "",
    consoleErrors: [],
    actError: "",
    navStatus: null,
    failedRequests: [],
    errorSurfaces: [],
  };

  it("counts a request the guard stopped as proof that the control is wired", () => {
    const verdict = verdictFromObservation(base, {
      blockedMutations: ["POST https://console.example/api/v1/accounts/current/api-keys"],
    });
    expect(verdict.proven).toBe(true);
    expect(verdict.proofType).toBe("wired-write-blocked");
  });

  it("does not let the error the abort caused veto that proof", () => {
    const verdict = verdictFromObservation(
      { ...base, errorSurfaces: ["Network error. Please try again."] },
      { blockedMutations: ["POST https://console.example/api/keys"] },
    );
    expect(verdict.proven).toBe(true);
  });

  it("still fails an activation that produced nothing to block", () => {
    const verdict = verdictFromObservation(
      { ...base, requests: [] },
      { blockedMutations: [] },
    );
    expect(verdict.proven).toBe(false);
  });
});

describe("expected versus unexpected rejections", () => {
  const base: Observation = {
    urlBefore: "https://console.example/console",
    urlAfter: "https://console.example/console",
    urlChanged: false,
    requests: ["POST /api/settings"],
    domChanged: false,
    domStableBaseline: true,
    popup: false,
    download: "",
    dialog: "",
    consoleErrors: [],
    actError: "",
    navStatus: null,
    failedRequests: [],
    errorSurfaces: [],
  };

  it("counts a 422 as a failure: the gate no longer feeds any request that lands", () => {
    // This used to be excused whenever the harness had typed the values, which
    // was the only way to prove a submit while still letting it through. The
    // guard stops those requests inside the browser now, so a 4xx that does
    // come back was the application's own, and an application whose server
    // refuses its own request is a defect.
    const verdict = verdictFromObservation({
      ...base,
      failedRequests: ["422 POST /api/settings"],
    });
    expect(verdict.proven).toBe(false);
    expect(verdict.proofType).toBe("failed-request");
  });

  it("never excuses a 403", () => {
    const verdict = verdictFromObservation({
      ...base,
      failedRequests: ["403 POST /api/settings"],
    });
    expect(verdict.proven).toBe(false);
    expect(verdict.proofType).toBe("failed-request");
  });

  it("reports a rate limited activation as having no verdict at all", () => {
    const verdict = verdictFromObservation({
      ...base,
      failedRequests: ["429 POST /api/settings"],
    });
    expect(verdict.proven).toBe(false);
    expect(verdict.proofType).toBe("rate-limited");
  });

  it("does not blame a link for the traffic its destination page fetches", () => {
    const verdict = verdictFromObservation({
      ...base,
      urlChanged: true,
      urlAfter: "https://console.example/console/billing",
      navStatus: 200,
      failedRequests: ["500 GET /api/usage-widget"],
    });
    expect(verdict.proven).toBe(true);
    expect(verdict.proofType).toBe("navigation");
  });

  it("does blame it when the destination itself answered 500", () => {
    const verdict = verdictFromObservation({
      ...base,
      urlChanged: true,
      urlAfter: "https://console.example/console/billing",
      navStatus: 500,
    });
    expect(verdict.proven).toBe(false);
    expect(verdict.proofType).toBe("broken-endpoint");
  });
});
