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

import {
  test,
  expect,
  type BrowserContext,
  type Locator,
  type Page,
  type Response,
} from "@playwright/test";
import { appendFileSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { join } from "node:path";

import {
  APP_DIR,
  REGISTRY_FILE,
  REPORT_DIR,
  ROUTE_FIXTURE_FILE,
  ROUTE_FLOOR_FILE,
  AUTH_STATE_FILE,
  interactionBaseUrl,
  routeFilter,
} from "./lib/config";
import { WEB_CONSOLE_DIR } from "./lib/config";
import { enumerateInPage, type RawControl } from "./lib/enumerate";
import { collectExclusions, exclusionProblems } from "./lib/exclusions";
import { floorProblems, loadFloors, staleFloors, type VisitedRoute } from "./lib/floors";
import { controlKey } from "./lib/key";
import { indexRegistry, parseRegistry, validateRegistry } from "./lib/registry";
import {
  DESTRUCTIVE_PATTERN,
  DISABLED_PROOF_TYPE,
  SESSION_ENDING_PATTERN,
  externalDestinationReport,
  fillFormFor,
  mutationGuard,
  observe,
  proveExternalLink,
  proveField,
  proveSelect,
  proveToggle,
  verdictForDisabled,
  verdictFromObservation,
  type MutationGuard,
  type Observation,
  type ProofOutcome,
} from "./lib/prove";
import {
  buildReport,
  formatSummary,
  ratio,
  writeReport,
  type ControlRecord,
  type ControlStatus,
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

/**
 * Which bucket a verdict is recorded under.
 *
 * "disabled" is neither a pass nor a failure: it is not proof of anything, so
 * it never joins the numerator, and it is not the control's fault, so it never
 * fails the gate on its own. What fails an unexpected disable is
 * route-floors.json, which names the controls each route must leave usable.
 */
function statusOf(outcome: ProofOutcome): ControlStatus {
  if (outcome.proven) {
    return "proven";
  }
  return outcome.proofType === DISABLED_PROOF_TYPE ? "disabled" : "unproven";
}

/** A control key with the duplicate ordinal dropped: one thing a user can do. */
function identityOfKey(key: string): string {
  return key.replace(/~\d+$/, "");
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
  let cleared: Array<Awaited<ReturnType<BrowserContext["cookies"]>>[number]> = [];
  try {
    const cookies = await context.cookies();
    const survivors = cookies.filter((cookie) => cookie.name !== LOCALE_COOKIE);
    if (survivors.length !== cookies.length) {
      await context.clearCookies();
      // Held for the finally below. Between the clear and the restore this
      // context has no session at all, and swallowing a failure there would
      // sign the run out and turn one pinning failure into a false unproven
      // verdict for every control after it.
      cleared = survivors;
      await context.addCookies(survivors);
      cleared = [];
    }
    await context.addCookies([{ name: LOCALE_COOKIE, value: "en", url: BASE_URL }]);
  } catch (error) {
    // Never let locale pinning be the reason a run fails, but never let it
    // leave the context signed out either, and never let it fail silently.
    progress(
      `[locale] pinning failed: ${String(error).split("\n")[0].slice(0, 160)}`,
    );
  } finally {
    if (cleared.length > 0) {
      await context.addCookies(cleared).catch(() => {
        progress("[locale] session cookies could NOT be restored after clearing");
      });
    }
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

/**
 * Controls that change the state every other control is addressed through.
 *
 * The locale switcher is one form POST that renames every label on the page,
 * including its own siblings in the shell. Activating it mid-sweep left the
 * run holding English keys against a Bengali render and reporting working
 * navigation links as dead. Pinning the cookie only papered over it. These
 * are proven once, last, in a context that is then thrown away, exactly like
 * sign out, so nothing they change can reach another control's verdict.
 */
function isDeferred(control: RawControl): boolean {
  if (control.formAction.includes("/locale")) {
    return true;
  }
  return isSessionEnding(control);
}

/**
 * Navigates, retrying a transient answer.
 *
 * A deployed origin can drop for a couple of minutes and come back on its own
 * (issue #815), so a 502 or a 5xx is retried before anything is written down.
 * Used by every pass over a route, because a pass that skipped this recorded
 * one blip as a dead control, and in the deferred pass that verdict is then
 * cached and replayed to every route the control appears on.
 */
async function gotoWithRetry(
  page: Page,
  url: string,
  label: string,
): Promise<Response | null> {
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
      `[retry] ${label} answered ${response === null ? "nothing" : String(response.status())}, retrying`,
    );
    await page.waitForTimeout(20000);
    response = await page
      .goto(url, { waitUntil: "domcontentloaded", timeout: 45000 })
      .catch(() => null);
  }
  return response;
}

/** Navigates to a route and, when needed, re-opens the reveal chain. */
async function prepare(
  page: Page,
  url: string,
  revealPath: string[],
): Promise<boolean> {
  await pinLocale(page);
  // Through the retry, like every other navigation. This one runs on the three
  // hottest paths there are: the stale-page retry, the toggle persistence
  // re-locate, and the reveal-chain reopen. A transient answer here returns an
  // error page, the control is not on it, and the verdict is "could not be
  // found on a freshly loaded page", which reads as a defect in the console.
  await gotoWithRetry(page, url, url);
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
  guard: MutationGuard,
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
          // The gate typed the values this submission carries, so the write is
          // blocked: the click still reaches the application and the request it
          // builds is still proof, it just never lands on a real record.
          const { value: observation, blocked } = await guard.withWritesBlocked(() =>
            observe(page, async () => {
              await refreshed.click({ timeout: 8000 });
            }),
          );
          const outcome = verdictFromObservation(observation, { blockedMutations: blocked });
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
    const outcome = await proveField(page, locator, control, guard);
    return { outcome, dirty: outcome.proofType === "navigation", revealed: [] };
  }

  if (control.kind === "select") {
    const outcome = await proveSelect(page, locator, control, guard);
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
  // application. Not for a destructive control: nothing the gate types belongs
  // in a form whose submit deletes something.
  let filledFields: string[] = [];
  if (control.kind === "submit" && !destructive) {
    filledFields = await fillFormFor(locator);
  }

  // Every activation, not a chosen few. Containment used to depend on the
  // control's visible label matching a destructive-sounding word, which is not
  // a safety mechanism: `Change email` on /console/settings/profile calls
  // supabase.auth.updateUser and matched nothing, so both live runs of this
  // gate issued a real PUT to /auth/v1/user against the demo account. A rule
  // that has to guess which button is dangerous will keep guessing wrong.
  //
  // The click still happens, the application still builds and issues its
  // request, and the interception is what proves the control is wired. The one
  // exception is a toggle, whose whole proof is that its flip survives a
  // reload, and which the gate flips back and fails if it could not.

  // Tag the element so the reveal pass can tell "a new control appeared" from
  // "the control I just clicked relabelled itself to Sending…", which is a
  // transient state of one control, not two controls.
  await locator
    .evaluate((element: Element) => {
      element.setAttribute("data-ic-active", "1");
    })
    .catch(() => undefined);

  const { value: observation, blocked } = await guard.withWritesBlocked(() =>
    observe(page, async () => {
      await locator.click({ timeout: 8000 });
    }),
  );
  const outcome = verdictFromObservation(observation, { blockedMutations: blocked });
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
  // Not when the guard fired. What a blocked write reveals is the application's
  // network-error branch: a "Try again" button that exists only while that error
  // is on screen, cannot be reached again from a fresh page, and is therefore
  // reported as unlocatable on every run. That is the gate accusing the console
  // of a control the gate itself conjured.
  if (observation.domChanged && !observation.urlChanged && blocked.length === 0) {
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
    // Close whatever the click opened. This is tidying, not the safeguard:
    // what keeps the deletion from happening is the mutation guard above,
    // which stopped the request inside the browser. Escape after a click can
    // only ever close a dialog that is already open, and if the control
    // deleted on the first click there was nothing left to press it for.
    await page.keyboard.press("Escape").catch(() => undefined);
  }

  return { outcome, dirty, revealed };
}

test.describe("interaction coverage", () => {
  test.describe.configure({ mode: "serial" });

  test("every rendered control has a proven effect", async ({ browser }) => {
    // Under the CI job's 60 minute cap, so Playwright ends the run itself and
    // still writes the ledger and attaches it. A job killed by GitHub's own
    // timeout uploads nothing, which is the difference between "the sweep is
    // too slow" and no information at all.
    test.setTimeout(process.env.CI ? 50 * 60 * 1000 : 3 * 60 * 60 * 1000);

    mkdirSync(REPORT_DIR, { recursive: true });
    rmSync(PROGRESS_LOG, { force: true });

    const fixtures = loadRouteFixtures(ROUTE_FIXTURE_FILE);
    const discovered = discoverRoutes(APP_DIR, fixtures);
    const registry = parseRegistry(readFileSync(REGISTRY_FILE, "utf8"));
    const registryIndex = indexRegistry(registry);

    const problems: string[] = validateRegistry(registry, WEB_CONSOLE_DIR);
    problems.push(...exclusionProblems(collectExclusions(fixtures, registry)));
    const floors = loadFloors(ROUTE_FLOOR_FILE);
    for (const stale of staleFloors(floors, discovered.map((r) => r.pattern))) {
      problems.push(
        `route-floors.json records a floor for "${stale}", which is not a route in app/; delete the entry`,
      );
    }
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
    const guards = new Map<BrowserContext, MutationGuard>([
      [authed, mutationGuard(authed)],
      [anon, mutationGuard(anon)],
    ]);

    // Break proof hook. A gate nobody has watched fail is worth nothing, so
    // this neuters named controls at the event layer, leaving the markup and
    // every sibling untouched. Running the same route with and without it is
    // the only evidence that a proven verdict means anything:
    //
    //   INTERACTION_ROUTES=/console/analytics INTERACTION_SABOTAGE=24h,7d,30d,90d
    //
    // Blocking in the capture phase at the window, because React delegates to
    // a root listener and a handler removed from the element itself would
    // still be reached through delegation.
    const sabotage = (process.env.INTERACTION_SABOTAGE ?? "")
      .split(",")
      .map((value) => value.trim())
      .filter((value) => value !== "");
    if (sabotage.length > 0) {
      for (const context of [authed, anon]) {
        await context.addInitScript((names: string[]) => {
          for (const type of ["pointerdown", "mousedown", "mouseup", "click", "keydown"]) {
            window.addEventListener(
              type,
              (event) => {
                const target = event.target;
                if (!(target instanceof Element)) return;
                const element = target.closest(
                  "button, a, [role=button], [role=tab], [role=switch]",
                );
                if (!element) return;
                const label = (
                  element.getAttribute("aria-label") ??
                  element.textContent ??
                  ""
                ).trim();
                if (names.some((name) => name.toLowerCase() === label.toLowerCase())) {
                  event.stopImmediatePropagation();
                  event.preventDefault();
                }
              },
              true,
            );
          }
        }, sabotage);
      }
      progress(`[sabotage] handlers blocked for: ${sabotage.join(", ")}`);
    }

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
          disabled: 0,
          unproven: 0,
          // Not one. A route nobody measured is zero percent measured, and the
          // exclusion is what makes that acceptable, not the number.
          coverage: 0,
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
            disabled: 0,
            unproven: 0,
            coverage: 0,
          });
          continue;
        }
        concretePath = instance;
      }

      const context = authModeFor(route) === "anon" ? anon : authed;
      const guard = guards.get(context);
      if (guard === undefined) {
        throw new Error("no mutation guard installed for this context");
      }
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
        disabled: 0,
        unproven: 0,
        coverage: 0,
      };

      try {
        await pinLocale(page);
        const response = await gotoWithRetry(page, url, route.pattern);
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

          if (isDeferred(item.control)) {
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

          const result = await proveControl(page, url, item, key, guard);
          const status = statusOf(result.outcome);
          progress(
            `${status === "proven" ? "  ok  " : status === "disabled" ? "  --  " : "  XX  "}${route.pattern}  ${key}  ${result.outcome.proofType}`,
          );
          controlRecords.push({
            ...base,
            status,
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
      record.disabled = mine.filter((c) => c.status === "disabled").length;
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
    // Deferred controls are not independent of each other. Sign out revokes
    // the session server side, so every deferred control proven after it gets
    // a fresh context carrying a storage state that no longer authenticates,
    // lands on the sign in page, and is reported as unlocatable. That accused
    // both locale buttons on seventeen routes. Session enders go last.
    deferred.sort(
      (a, b) => Number(isSessionEnding(a.control)) - Number(isSessionEnding(b.control)),
    );
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
      // Guarded like every other activation. Sign out and the locale switcher
      // both POST, and both change state that outlives this run: one revokes
      // the session, the other rewrites the account's language preference. The
      // ordering hazard this pass exists to work around, sign out killing the
      // session every later control is addressed through, cannot happen once
      // the request is stopped in the browser.
      const deferredGuard = mutationGuard(context);
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
        // The same transient-outage retry and settle wait the main loop uses.
        // Without them a 502 that clears in twenty seconds, or a shell that has
        // not finished hydrating, records this control as unproven, and the
        // verdict is then cached and replayed to every route that renders it:
        // one blip fails the gate on seventeen routes.
        await gotoWithRetry(page, item.url, `${item.route} (deferred)`);
        await page.waitForLoadState("networkidle", { timeout: 8000 }).catch(() => undefined);
        const locator = await locateByKey(page, item.key);
        if (locator !== null) {
          const { value: observation, blocked } = await deferredGuard.withWritesBlocked(() =>
            observe(page, async () => {
              await locator.click({ timeout: 8000 });
            }),
          );
          outcome = verdictFromObservation(observation, { blockedMutations: blocked });
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
        status: statusOf(outcome),
        proofType: outcome.proofType,
        detail: outcome.detail,
      });
      const record = routeRecords.find((r) => r.route === item.route);
      if (record) {
        record.enumerated += 1;
        const status = statusOf(outcome);
        if (status === "proven") {
          record.proven += 1;
        } else if (status === "disabled") {
          record.disabled += 1;
        } else {
          record.unproven += 1;
        }
        record.coverage = ratio(record.proven, record.enumerated);
      }
    }

    // Denominator guard. Coverage is proven/enumerated and both sides come
    // from the rendered DOM, so a route that renders less than it used to
    // shrinks the denominator and holds the percentage up while the surface
    // is degrading. Every visited route is measured against what
    // route-floors.json says it must render and leave usable.
    const visited: VisitedRoute[] = routeRecords.map((record) => {
      const mine = controlRecords.filter((control) => control.route === record.route);
      return {
        route: record.route,
        visited: record.visited,
        enabled: mine
          .filter((control) => control.status !== "disabled")
          .map((control) => identityOfKey(control.key)),
        disabled: mine
          .filter((control) => control.status === "disabled")
          .map((control) => identityOfKey(control.key)),
      };
    });
    problems.push(...floorProblems(floors, visited));

    // A run that measured nothing must never report success. This gate spent
    // its whole first life in exactly that state: its sign-in wrote no session,
    // so the sweep never ran, and the only thing that eventually said so was an
    // unrelated ENOENT. An empty result is the loudest possible failure of a
    // coverage gate and it needs to be stated as one.
    if (FILTER.length === 0) {
      if (routeRecords.filter((record) => record.visited).length === 0) {
        problems.push(
          "the sweep visited no route at all, so it measured nothing; check the session, the origin, and that the app is up",
        );
      }
      if (controlRecords.length === 0) {
        problems.push(
          "the sweep enumerated no control at all, so it measured nothing; a coverage report over an empty set is not coverage",
        );
      }
    }

    const staleDeclarations = registryIndex
      .unmatched()
      .map(
        (entry) =>
          `control registry declares "${entry.route} :: ${entry.control}" (owner ${entry.owner}), and this run enumerated no such control`,
      );

    await authed.close();
    await anon.close();

    const report = buildReport({
      baseUrl: BASE_URL,
      partial: FILTER.length > 0,
      routesDiscovered: discovered.length,
      routes: routeRecords,
      controls: controlRecords,
      problems,
      staleDeclarations,
      externalDestinations: externalDestinationReport(),
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
  // A catch-all segment spans path separators and a plain one does not. Both
  // were compiled to `([^/]+)`, so `/docs/[...slug]` never matched the
  // `/docs/guide/setup` the app rendered, and the route was reported as having
  // no reachable instance while a link to it sat on the page.
  const regex = new RegExp(
    `^${pattern
      .replace(/\[(\.\.\.)?[^\]]+\]/g, (_match, spread: string | undefined) =>
        spread === undefined ? "([^/]+)" : "(.+)",
      )
      .replace(/\//g, "\\/")}$`,
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
