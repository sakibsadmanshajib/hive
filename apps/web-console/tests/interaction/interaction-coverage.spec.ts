// Interaction coverage gate.
//
// Enumerates every interactive control in every rendered console route, then
// activates each one and requires observable evidence that it did something.
// A control with no proven effect fails this test. Inertness is allowed only
// through an explicit registry entry carrying a justification and an owner.
//
// Run it against whatever origin matters:
//   INTERACTION_BASE_URL=http://localhost:3000            (composed stack)
//   INTERACTION_BASE_URL=https://console-hive.scubed.co   (deployed box)

import { test, expect, type BrowserContext, type Locator, type Page } from "@playwright/test";
import { appendFileSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { join } from "node:path";

import {
  APP_DIR,
  REGISTRY_FILE,
  REPORT_DIR,
  ROUTE_FIXTURE_FILE,
  AUTH_STATE_FILE,
  interactionBaseUrl,
  routeFilter,
} from "./lib/config";
import { WEB_CONSOLE_DIR } from "./lib/config";
import { enumerateInPage, type RawControl } from "./lib/enumerate";
import { controlKey } from "./lib/key";
import { indexRegistry, parseRegistry, validateRegistry } from "./lib/registry";
import {
  DESTRUCTIVE_PATTERN,
  SESSION_ENDING_PATTERN,
  fillFormFor,
  observe,
  proveExternalLink,
  proveField,
  proveSelect,
  proveToggle,
  verdictForDisabled,
  verdictFromObservation,
  type ProofOutcome,
} from "./lib/prove";
import {
  buildReport,
  formatSummary,
  ratio,
  writeReport,
  type ControlRecord,
  type RouteRecord,
} from "./lib/report";
import {
  discoverRoutes,
  fillPattern,
  isDynamic,
  loadRouteFixtures,
  staleFixtureRoutes,
  type DiscoveredRoute,
} from "./lib/routes";

const BASE_URL = interactionBaseUrl();
const FILTER = routeFilter();

// Playwright buffers a test's stdout until the test ends, and this one runs
// for tens of minutes. Progress goes to a file so a run in flight can be
// watched, locally and from a CI artifact after a timeout.
const PROGRESS_LOG = join(REPORT_DIR, "progress.log");

function progress(line: string): void {
  process.stdout.write(`${line}\n`);
  try {
    appendFileSync(PROGRESS_LOG, `${line}\n`, "utf8");
  } catch {
    // Progress logging must never be the reason a run fails.
  }
}

interface WorkItem {
  control: RawControl;
  /** Control keys that must be activated, in order, to reveal this control. */
  revealPath: string[];
}

function pathOf(url: string): string {
  return new URL(url).pathname;
}

function authModeFor(route: DiscoveredRoute): "user" | "anon" {
  if (route.fixture.auth) {
    return route.fixture.auth;
  }
  if (route.pattern === "/" || route.pattern.startsWith("/auth/")) {
    return "anon";
  }
  return "user";
}

async function enumeratePage(page: Page): Promise<RawControl[]> {
  // `page.evaluate` throws "Execution context was destroyed" when a
  // navigation lands mid-call, which is routine on a server rendered app and
  // says nothing about any control. Retry rather than abandoning the route:
  // an aborted route reads as zero coverage, which is a far worse lie than a
  // slow one.
  let lastError: unknown = null;
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      const result = await page.evaluate(enumerateInPage);
      return result.controls;
    } catch (error) {
      lastError = error;
      await page
        .waitForLoadState("domcontentloaded", { timeout: 10000 })
        .catch(() => undefined);
      await page.waitForTimeout(500);
    }
  }
  throw lastError instanceof Error ? lastError : new Error(String(lastError));
}

/**
 * Re-locates a control whose own key may have changed.
 *
 * A toggle commonly announces its state in its accessible name ("Agent cowork
 * capability: enabled"), so flipping it renames it and a key lookup after the
 * reload finds nothing. Reporting that as "does not persist" would be exactly
 * the false accusation this gate exists to prevent, so fall back to the same
 * position in the fresh enumeration, requiring the tag and role to agree.
 */
async function locateStable(
  page: Page,
  key: string,
  control: RawControl,
): Promise<Locator | null> {
  const byKey = await locateByKey(page, key);
  if (byKey !== null) {
    return byKey;
  }
  const controls = await enumeratePage(page);
  const atSamePosition = controls[control.idx];
  if (
    atSamePosition !== undefined &&
    atSamePosition.tag === control.tag &&
    atSamePosition.role === control.role
  ) {
    return page.locator(`[data-ic-idx="${String(atSamePosition.idx)}"]`);
  }
  return null;
}

async function locateByKey(page: Page, key: string): Promise<Locator | null> {
  const controls = await enumeratePage(page);
  const match = controls.find((control) => controlKey(control) === key);
  if (!match) {
    return null;
  }
  return page.locator(`[data-ic-idx="${String(match.idx)}"]`);
}

// The console renders every control's label through next-intl, so a run that
// activates the locale switcher (correctly, it works) continues against a page
// whose control keys are all in another language and matches none of them. Pin
// the locale before every navigation: the switcher still gets proven, and the
// rest of the surface stays addressable.
const LOCALE_COOKIE = "locale";

async function pinLocale(page: Page): Promise<void> {
  // Adding a host-scoped cookie is not enough: the app sets its own on a
  // parent domain, both then exist, and the app's wins. Drop every cookie of
  // that name whatever domain carries it, keep the rest (the session lives
  // there), then set ours.
  const context = page.context();
  try {
    const cookies = await context.cookies();
    const survivors = cookies.filter((cookie) => cookie.name !== LOCALE_COOKIE);
    if (survivors.length !== cookies.length) {
      await context.clearCookies();
      await context.addCookies(survivors);
    }
    await context.addCookies([{ name: LOCALE_COOKIE, value: "en", url: BASE_URL }]);
  } catch {
    // Never let locale pinning be the reason a run fails.
  }
}

/** True for the control that ends the session, in any language. */
function isSessionEnding(control: RawControl): boolean {
  // Matching on the form action rather than the label: the sidebar sign out
  // button is localized, and an English-only name match let a Bengali render
  // of it be clicked inline, which killed the session and turned every
  // remaining control in the run into a false failure.
  if (control.formAction.includes("sign-out") || control.formAction.includes("signout")) {
    return true;
  }
  return SESSION_ENDING_PATTERN.test(`${control.name} ${control.testid}`);
}

/** Navigates to a route and, when needed, re-opens the reveal chain. */
async function prepare(
  page: Page,
  url: string,
  revealPath: string[],
): Promise<boolean> {
  await pinLocale(page);
  await page.goto(url, { waitUntil: "domcontentloaded", timeout: 45000 });
  await page.waitForLoadState("networkidle", { timeout: 4000 }).catch(() => undefined);
  for (const step of revealPath) {
    const ancestor = await locateByKey(page, step);
    if (ancestor === null) {
      return false;
    }
    await ancestor.click({ timeout: 5000 }).catch(() => undefined);
    await page.waitForTimeout(400);
  }
  return true;
}

function isExternal(href: string): boolean {
  if (!/^https?:\/\//i.test(href)) {
    return false;
  }
  try {
    return new URL(href).origin !== new URL(BASE_URL).origin;
  } catch {
    return false;
  }
}

async function proveControl(
  page: Page,
  url: string,
  item: WorkItem,
  key: string,
): Promise<{ outcome: ProofOutcome; dirty: boolean; revealed: RawControl[] }> {
  const { control } = item;

  if (control.disabled) {
    // A save button disabled until its form is valid is not an inert control,
    // it is a control with a precondition. Satisfy the precondition and look
    // again before reporting it as doing nothing.
    const disabledLocator = await locateByKey(page, key);
    if (disabledLocator !== null) {
      const filled = await fillFormFor(disabledLocator);
      if (filled.length > 0) {
        const refreshed = await locateByKey(page, key);
        const stillDisabled =
          refreshed === null || (await refreshed.isDisabled().catch(() => true));
        if (!stillDisabled && refreshed !== null) {
          const observation = await observe(page, async () => {
            await refreshed.click({ timeout: 8000 });
          });
          const outcome = verdictFromObservation(observation);
          outcome.detail = `${outcome.detail} (enabled by filling ${filled.join(", ")})`;
          return { outcome, dirty: true, revealed: [] };
        }
      }
    }
    return { outcome: verdictForDisabled(control), dirty: true, revealed: [] };
  }

  let locator = await locateByKey(page, key);
  if (locator === null) {
    // Almost always a stale page rather than a missing control: the previous
    // activation moved the document and this key was resolved against what
    // was left. Reload the route and look once more before recording it.
    const reopened = await prepare(page, url, item.revealPath);
    locator = reopened ? await locateByKey(page, key) : null;
  }
  if (locator === null) {
    return {
      outcome: {
        proven: false,
        proofType: "unproven",
        detail: "control could not be found on a freshly loaded page, so it could not be activated",
      },
      dirty: true,
      revealed: [],
    };
  }

  if (control.kind === "toggle") {
    const outcome = await proveToggle(page, locator, async () => {
      const ok = await prepare(page, url, item.revealPath);
      return ok ? locateStable(page, key, control) : null;
    });
    return { outcome, dirty: true, revealed: [] };
  }

  if (control.kind === "textfield") {
    const outcome = await proveField(page, locator, control);
    return { outcome, dirty: outcome.proofType === "navigation", revealed: [] };
  }

  if (control.kind === "select") {
    const outcome = await proveSelect(page, locator, control);
    return { outcome, dirty: true, revealed: [] };
  }

  if (control.kind === "link" && isExternal(control.href)) {
    const outcome = await proveExternalLink(page, control.href);
    return { outcome, dirty: false, revealed: [] };
  }

  const destructive = DESTRUCTIVE_PATTERN.test(`${control.name} ${control.testid}`);

  // A submit button clicked against an empty form is stopped by the browser's
  // own required-field validation, which is indistinguishable from a button
  // with no handler. Fill the form first so the click actually reaches the
  // application.
  let filledFields: string[] = [];
  if (control.kind === "submit") {
    filledFields = await fillFormFor(locator);
  }

  // Tag the element so the reveal pass can tell "a new control appeared" from
  // "the control I just clicked relabelled itself to Sending…", which is a
  // transient state of one control, not two controls.
  await locator
    .evaluate((element: Element) => {
      element.setAttribute("data-ic-active", "1");
    })
    .catch(() => undefined);

  const observation = await observe(page, async () => {
    await locator.click({ timeout: 8000 });
  });
  const outcome = verdictFromObservation(observation);
  if (filledFields.length > 0) {
    outcome.detail = `${outcome.detail} (form pre-filled: ${filledFields.join(", ")})`;
  }

  // A control that opened something reveals controls the first enumeration
  // could not see. Those are part of the surface and must be enumerated too,
  // otherwise every dropdown and tab panel is a blind spot by construction.
  let revealed: RawControl[] = [];
  const dirty =
    observation.urlChanged ||
    observation.domChanged ||
    observation.popup ||
    observation.requests.length > 0;
  if (observation.domChanged && !observation.urlChanged) {
    const all = await enumeratePage(page);
    const activeIdx = await page.evaluate((): number => {
      const element = document.querySelector("[data-ic-active='1']");
      if (element === null) {
        return -1;
      }
      const raw = element.getAttribute("data-ic-idx");
      element.removeAttribute("data-ic-active");
      return raw === null ? -1 : Number(raw);
    });
    revealed = all.filter((candidate) => candidate.idx !== activeIdx);
  }

  if (destructive) {
    // Back out of anything a destructive control opened, so the gate never
    // leaves a live surface mid-confirmation.
    await page.keyboard.press("Escape").catch(() => undefined);
  }

  return { outcome, dirty, revealed };
}

test.describe("interaction coverage", () => {
  test.describe.configure({ mode: "serial" });

  test("every rendered control has a proven effect", async ({ browser }) => {
    test.setTimeout(3 * 60 * 60 * 1000);

    mkdirSync(REPORT_DIR, { recursive: true });
    rmSync(PROGRESS_LOG, { force: true });

    const fixtures = loadRouteFixtures(ROUTE_FIXTURE_FILE);
    const discovered = discoverRoutes(APP_DIR, fixtures);
    const registry = parseRegistry(readFileSync(REGISTRY_FILE, "utf8"));
    const registryIndex = indexRegistry(registry);

    const problems: string[] = validateRegistry(registry, WEB_CONSOLE_DIR);
    for (const stale of staleFixtureRoutes(fixtures, discovered)) {
      problems.push(
        `route-fixtures.json declares "${stale}", which is not a route in app/; delete the entry or restore the route`,
      );
    }

    const authed: BrowserContext = await browser.newContext({
      storageState: AUTH_STATE_FILE,
      viewport: { width: 1440, height: 900 },
    });
    const anon: BrowserContext = await browser.newContext({
      viewport: { width: 1440, height: 900 },
    });

    const routeRecords: RouteRecord[] = [];
    const controlRecords: ControlRecord[] = [];
    const deferred: Array<{ url: string; route: string; key: string; control: RawControl }> = [];

    // Dynamic routes are resolved from links the app itself renders, so a new
    // dynamic route needs no fixture as long as one instance is reachable.
    const discoveredHrefs = new Set<string>();

    const selected = discovered.filter(
      (route) => FILTER.length === 0 || FILTER.some((f) => route.pattern.includes(f)),
    );

    for (const route of selected) {
      if (route.fixture.skip) {
        routeRecords.push({
          route: route.pattern,
          url: "",
          visited: false,
          note: `declared skip: ${route.fixture.skip} (owner ${route.fixture.owner ?? "UNOWNED"})`,
          enumerated: 0,
          proven: 0,
          declared: 0,
          unproven: 0,
          coverage: 1,
        });
        if (!route.fixture.owner) {
          problems.push(`route ${route.pattern} is skipped with no owner`);
        }
        continue;
      }

      let concretePath = route.path;
      // `discoverRoutes` already substituted declared params, so a pattern
      // that still carries brackets is one nobody declared: resolve it from a
      // link the app itself rendered, or fail loudly.
      if (isDynamic(concretePath)) {
        const instance = findInstance(route.pattern, discoveredHrefs);
        if (instance === null) {
          problems.push(
            `dynamic route ${route.pattern} has no discoverable instance and no declared params; the gate cannot enumerate it and will not pretend it is covered`,
          );
          routeRecords.push({
            route: route.pattern,
            url: "",
            visited: false,
            note: "no instance discoverable",
            enumerated: 0,
            proven: 0,
            declared: 0,
            unproven: 0,
            coverage: 0,
          });
          continue;
        }
        concretePath = instance;
      }

      const context = authModeFor(route) === "anon" ? anon : authed;
      const page = await context.newPage();
      const url = new URL(concretePath, BASE_URL).toString();

      const record: RouteRecord = {
        route: route.pattern,
        url,
        visited: false,
        note: "",
        enumerated: 0,
        proven: 0,
        declared: 0,
        unproven: 0,
        coverage: 0,
      };

      try {
        await pinLocale(page);
        // A deployed origin can drop for a couple of minutes and come back on
        // its own (issue #815). Retry once before recording anything, so a
        // transient outage is never written down as a dead surface.
        let response = await page
          .goto(url, { waitUntil: "domcontentloaded", timeout: 45000 })
          .catch(() => null);
        for (let attempt = 0; attempt < 3; attempt += 1) {
          const transient =
            response === null || response.status() === 502 || response.status() >= 503;
          if (!transient) {
            break;
          }
          progress(
            `[retry] ${route.pattern} answered ${response === null ? "nothing" : String(response.status())}, retrying`,
          );
          await page.waitForTimeout(20000);
          response = await page
            .goto(url, { waitUntil: "domcontentloaded", timeout: 45000 })
            .catch(() => null);
        }
        if (response === null) {
          throw new Error("origin never answered after three attempts");
        }
        await page.waitForLoadState("networkidle", { timeout: 8000 }).catch(() => undefined);
        const landed = pathOf(page.url());
        const requested = pathOf(url);
        if (landed !== requested) {
          record.note = `redirected to ${landed}`;
          if (
            !route.fixture.expectRedirect ||
            !landed.startsWith(route.fixture.expectRedirect)
          ) {
            problems.push(
              `route ${route.pattern} redirected to ${landed} with no declared expectRedirect; its controls are enumerated by nobody`,
            );
          }
          routeRecords.push(record);
          await page.close();
          continue;
        }
        if (response && response.status() >= 400) {
          record.note = `answered ${String(response.status())}`;
          problems.push(`route ${route.pattern} answered ${String(response.status())}`);
          routeRecords.push(record);
          await page.close();
          continue;
        }
        record.visited = true;

        const top = await enumeratePage(page);
        for (const control of top) {
          if (control.href.startsWith("/")) {
            discoveredHrefs.add(control.href);
          }
        }

        progress(`[route] ${route.pattern} -> ${String(top.length)} controls enumerated`);
        const queue: WorkItem[] = top.map((control) => ({ control, revealPath: [] }));
        const seen = new Set<string>(top.map((control) => `|${controlKey(control)}`));
        let dirty = false;

        while (queue.length > 0) {
          const item = queue.shift();
          if (!item) {
            break;
          }
          const key = controlKey(item.control);
          const declared = registryIndex.lookup(route.pattern, key);
          const base: ControlRecord = {
            route: route.pattern,
            url,
            key,
            kind: item.control.kind,
            name: item.control.name,
            matchedBy: item.control.matchedBy,
            revealPath: item.revealPath,
            status: "unproven",
            proofType: "unproven",
            detail: "",
          };

          if (declared) {
            controlRecords.push({
              ...base,
              status: "declared",
              proofType: `declared:${declared.kind}`,
              detail: declared.reason,
              declaredKind: declared.kind,
              owner: declared.owner,
            });
            continue;
          }

          if (isSessionEnding(item.control)) {
            deferred.push({ url, route: route.pattern, key, control: item.control });
            continue;
          }

          if (dirty || item.revealPath.length > 0) {
            const ok = await prepare(page, url, item.revealPath);
            if (!ok) {
              controlRecords.push({
                ...base,
                detail: "its reveal chain could not be reopened, so it could not be activated",
              });
              dirty = true;
              continue;
            }
            dirty = false;
          }

          const result = await proveControl(page, url, item, key);
          progress(
            `${result.outcome.proven ? "  ok  " : "  XX  "}${route.pattern}  ${key}  ${result.outcome.proofType}`,
          );
          controlRecords.push({
            ...base,
            status: result.outcome.proven ? "proven" : "unproven",
            proofType: result.outcome.proofType,
            detail: result.outcome.detail,
          });
          dirty = result.dirty;

          // Proving the locale switcher works (it does) changes the language
          // of every remaining control's label, while this route's
          // enumeration snapshot still holds the English keys. Detect the
          // drift from the document itself rather than guessing which control
          // caused it, and force a reload so the next lookup runs against a
          // pinned page.
          const lang = await page
            .evaluate(() => document.documentElement.lang)
            .catch(() => "en");
          if (lang !== "" && lang !== "en") {
            await pinLocale(page);
            dirty = true;
          }

          if (item.revealPath.length < 2) {
            for (const candidate of result.revealed) {
              const candidateKey = controlKey(candidate);
              const path = [...item.revealPath, key];
              const id = `${path.join(">")}|${candidateKey}`;
              if (seen.has(`|${candidateKey}`) || seen.has(id)) {
                continue;
              }
              seen.add(id);
              queue.push({ control: candidate, revealPath: path });
            }
          }
        }
      } catch (error) {
        record.note = `error: ${String(error).split("\n")[0].slice(0, 200)}`;
        problems.push(`route ${route.pattern} could not be enumerated: ${record.note}`);
      } finally {
        await page.close().catch(() => undefined);
      }

      const mine = controlRecords.filter((c) => c.route === route.pattern);
      record.enumerated = mine.length;
      record.proven = mine.filter((c) => c.status === "proven").length;
      record.declared = mine.filter((c) => c.status === "declared").length;
      record.unproven = mine.filter((c) => c.status === "unproven").length;
      record.coverage = ratio(record.proven, record.enumerated);
      routeRecords.push(record);

      // Checkpoint after every route. A long run against a deployed origin
      // gets killed sooner or later (a CI timeout, a reaped process), and a
      // ledger that only exists at the end means a killed run measured
      // nothing.
      writeReport(
        REPORT_DIR,
        buildReport({
          baseUrl: BASE_URL,
          partial: true,
          routesDiscovered: discovered.length,
          routes: routeRecords,
          controls: controlRecords,
          problems,
        }),
      );
    }

    // Session ending controls run last, in a context that can be thrown away.
    // Sign out appears in the shared shell on every console route, but there is
    // only one of it: proving it a second time runs against the session the
    // first proof destroyed, which reports six false failures for one control
    // that works. Prove it once and attribute that verdict everywhere it
    // appears.
    const provenDeferred = new Map<string, ProofOutcome>();
    for (const item of deferred) {
      const cached = provenDeferred.get(item.key);
      if (cached) {
        recordDeferred(item, cached);
        continue;
      }
      const context = await browser.newContext({
        storageState: AUTH_STATE_FILE,
        viewport: { width: 1440, height: 900 },
      });
      const page = await context.newPage();
      let outcome: ProofOutcome = {
        proven: false,
        proofType: "unproven",
        detail: "could not be located in a fresh session",
      };
      try {
        // A brand new context has never had the locale pinned, so the shell
        // renders in whatever locale the account last chose and a key lookup
        // finds nothing. That reported a working sign out as unlocatable.
        await pinLocale(page);
        await page.goto(item.url, { waitUntil: "domcontentloaded", timeout: 45000 });
        const locator = await locateByKey(page, item.key);
        if (locator !== null) {
          const observation = await observe(page, async () => {
            await locator.click({ timeout: 8000 });
          });
          outcome = verdictFromObservation(observation);
        }
      } catch (error) {
        outcome = {
          proven: false,
          proofType: "unproven",
          detail: `activation threw: ${String(error).split("\n")[0].slice(0, 160)}`,
        };
      } finally {
        await context.close().catch(() => undefined);
      }
      provenDeferred.set(item.key, outcome);
      recordDeferred(item, outcome);
    }

    function recordDeferred(
      item: { url: string; route: string; key: string; control: RawControl },
      outcome: ProofOutcome,
    ): void {
      progress(
        `${outcome.proven ? "  ok  " : "  XX  "}${item.route}  ${item.key}  ${outcome.proofType}`,
      );
      controlRecords.push({
        route: item.route,
        url: item.url,
        key: item.key,
        kind: item.control.kind,
        name: item.control.name,
        matchedBy: item.control.matchedBy,
        revealPath: [],
        status: outcome.proven ? "proven" : "unproven",
        proofType: outcome.proofType,
        detail: outcome.detail,
      });
      const record = routeRecords.find((r) => r.route === item.route);
      if (record) {
        record.enumerated += 1;
        if (outcome.proven) {
          record.proven += 1;
        } else {
          record.unproven += 1;
        }
        record.coverage = ratio(record.proven, record.enumerated);
      }
    }

    for (const entry of registryIndex.unmatched()) {
      problems.push(
        `control registry declares "${entry.route} :: ${entry.control}" (owner ${entry.owner}), but no run enumerated that control; delete the stale entry`,
      );
    }

    await authed.close();
    await anon.close();

    const report = buildReport({
      baseUrl: BASE_URL,
      partial: FILTER.length > 0,
      routesDiscovered: discovered.length,
      routes: routeRecords,
      controls: controlRecords,
      problems,
    });
    const file = writeReport(REPORT_DIR, report);
    const summary = formatSummary(report);
    process.stdout.write(`${summary}\n  report written to ${file}\n`);
    test.info().attach("interaction-coverage.json", {
      body: JSON.stringify(report, null, 2),
      contentType: "application/json",
    });

    expect(
      report.problems,
      `interaction coverage integrity problems:\n${report.problems.join("\n")}`,
    ).toEqual([]);
    expect(
      report.unprovenControls.map((c) => `${c.route} ${c.key} -> ${c.proofType}: ${c.detail}`),
      `controls with no proven effect (control surface coverage ${(report.totals.coverage * 100).toFixed(1)}%)`,
    ).toEqual([]);
  });
});

/** Finds a concrete instance of a dynamic route among hrefs the app rendered. */
function findInstance(pattern: string, hrefs: Set<string>): string | null {
  const regex = new RegExp(
    `^${pattern.replace(/\[(\.\.\.)?[^\]]+\]/g, "([^/]+)").replace(/\//g, "\\/")}$`,
  );
  for (const href of hrefs) {
    const path = href.split("?")[0];
    if (regex.test(path)) {
      return href;
    }
  }
  // Fall back to declared params, which `discoverRoutes` already applied.
  try {
    return fillPattern(pattern, {});
  } catch {
    return null;
  }
}
