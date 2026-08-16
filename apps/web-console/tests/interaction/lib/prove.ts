// Activation and effect proof.
//
// Rendering is not functioning. Every enumerated control is activated for
// real and has to produce something observable: a network request, a URL
// change, a DOM mutation, a download, a popup, a native dialog, or (for a
// toggle) a value that survives a reload because the server gave it back.
// A control that produces none of those is a failure, not a skip.
//
// WHAT THE GATE IS ALLOWED TO CHANGE
// ----------------------------------
// One rule: when the gate supplies the value, the request never leaves the
// browser. Concretely, writes are blocked for
//
//   * anything whose label says delete, revoke, purchase and the like, and
//   * any activation carrying values the gate typed or chose: a filled form's
//     submit, a text field, a select.
//
// The activation still happens, the application still builds and issues its
// request, and the interception is the proof that the control is wired. What
// does not happen is the write. That matters because this gate is origin
// agnostic and has been run against the deployed console: the earlier version
// clicked first and pressed Escape afterwards, which is not a safeguard at
// all, and its recorded runs created API keys and sent a workspace invitation
// on the live demo tenant (docs/proof/interaction-coverage-2026-08-10).
//
// Toggles are the one exception, and deliberately: a toggle carries no value
// of the gate's own invention, its whole proof is that the flip survives a
// reload, and the gate flips it back and fails if the restore did not take.

import type { BrowserContext, Locator, Page, Request, Route } from "@playwright/test";

import { signatureInPage, type RawControl } from "./enumerate";

/** Controls whose activation is irreversible enough to need care. */
export const DESTRUCTIVE_PATTERN =
  /\b(delete|remove|revoke|destroy|deactivate|purchase|checkout|top ?up|pay now)\b/i;

/** Controls that end the session, so they run last and in a throwaway context. */
export const SESSION_ENDING_PATTERN = /\b(sign ?out|log ?out|logout|signout)\b/i;

const ASSET_PATTERN =
  /(_next\/static|__nextjs|\/favicon|\.(png|jpe?g|gif|svg|webp|ico|woff2?|ttf|css|map)(\?|$))/i;

const MUTATING_METHODS: ReadonlySet<string> = new Set(["POST", "PUT", "PATCH", "DELETE"]);

export interface Observation {
  urlBefore: string;
  urlAfter: string;
  urlChanged: boolean;
  requests: string[];
  domChanged: boolean;
  /** False when the page was already mutating on its own, so DOM evidence is unusable. */
  domStableBaseline: boolean;
  popup: boolean;
  download: string;
  dialog: string;
  consoleErrors: string[];
  actError: string;
  /** HTTP status of the document response, when the activation navigated. */
  navStatus: number | null;
  /** Non-2xx statuses seen on the XHR/fetch traffic the activation caused. */
  failedRequests: string[];
  /**
   * Error surfaces the activation put on screen that were not there before.
   *
   * A failed action is not a working control. A click that answers 500 and
   * renders "Something went wrong" produces both a request and a change to
   * the render, so without this the two strongest evidence channels both vote
   * to prove a control that plainly did not do its job.
   */
  errorSurfaces: string[];
}

/**
 * Blocks state-changing requests while an activation runs.
 *
 * Installed once per browser context. Disarmed by default, so an ordinary
 * click behaves exactly as a user's would; `withWritesBlocked` arms it for the
 * duration of one activation and reports what it stopped.
 *
 * The URL predicate keeps static assets out of the interceptor entirely: they
 * are the bulk of the traffic and can never be a mutation.
 */
export interface MutationGuard {
  withWritesBlocked<T>(act: () => Promise<T>): Promise<{ value: T; blocked: string[] }>;
}

/** Static assets can never be a mutation, and are the bulk of the traffic. */
function interceptable(url: URL): boolean {
  return !ASSET_PATTERN.test(url.href);
}

export function mutationGuard(context: BrowserContext): MutationGuard {
  return {
    async withWritesBlocked<T>(act: () => Promise<T>): Promise<{ value: T; blocked: string[] }> {
      const blocked: string[] = [];
      const handler = async (route: Route): Promise<void> => {
        const request = route.request();
        if (MUTATING_METHODS.has(request.method().toUpperCase())) {
          blocked.push(`${request.method()} ${redactUrl(request.url())}`);
          await route.abort("blockedbyclient").catch(() => undefined);
          return;
        }
        await route.continue().catch(() => undefined);
      };
      // Registered for the activation and removed straight after, rather than
      // left installed and toggled by a flag. An interceptor in the path of
      // every request is not free and not neutral: it changes how the browser
      // handles a request even when it lets it through, and this suite's whole
      // job is to observe what a page does on its own.
      await context.route(interceptable, handler);
      try {
        const value = await act();
        return { value, blocked: [...blocked] };
      } finally {
        await context.unroute(interceptable, handler).catch(() => undefined);
      }
    },
  };
}

/**
 * A URL safe to write into a report that is committed, uploaded as a CI
 * artifact from a public repository, and read by people.
 *
 * The fragment goes entirely: an OAuth implicit response puts a whole session
 * there. Credential-shaped query parameters are replaced by name, which covers
 * the invitation and password-reset links whose value in the query string is
 * itself the credential. PR #578 published four live invitation tokens exactly
 * this way.
 */
const SECRET_PARAMS =
  /^(token|token_hash|access_token|refresh_token|id_token|code|state|secret|password|apikey|api_key|key|invitation|invite|signature|sig)$/i;

export function redactUrl(raw: string): string {
  const withoutFragment = raw.split("#")[0];
  try {
    const url = new URL(withoutFragment);
    let touched = false;
    for (const name of [...url.searchParams.keys()]) {
      if (SECRET_PARAMS.test(name)) {
        url.searchParams.set(name, "REDACTED");
        touched = true;
      }
    }
    return touched ? url.toString() : withoutFragment;
  } catch {
    return withoutFragment;
  }
}

/**
 * Wording an application uses to tell a user that what they asked for did not
 * happen. Deliberately narrow: a live region that merely announces a result
 * ("3 keys", "Saved") is not a failure, and treating it as one would replace
 * a false pass with a false accusation.
 */
const FAILURE_TEXT =
  /\b(error|failed|failure|unable to|could ?n[o']t|went wrong|try again|invalid|denied|unauthori[sz]ed|forbidden|not found|timed out|too many requests)\b/i;

/**
 * Collects the text of every error surface currently rendered.
 *
 * `alertdialog` is excluded on purpose: a "delete this key?" confirmation is
 * an effect, not a failure.
 */
function errorSurfacesInPage(): string[] {
  const nodes = document.querySelectorAll(
    '[role="alert"], [aria-live="assertive"], [data-sonner-toast][data-type="error"], .toast-error, .Toastify__toast--error',
  );
  const out: string[] = [];
  nodes.forEach((node) => {
    if (node.getAttribute("role") === "alertdialog") {
      return;
    }
    const text = (node.textContent ?? "").replace(/\s+/g, " ").trim().slice(0, 200);
    if (text !== "") {
      out.push(text);
    }
  });
  return out;
}

function normalizeUrl(url: string): string {
  return redactUrl(url);
}

/**
 * The page's signature, or null when it could not be read.
 *
 * Null rather than a unique placeholder. The placeholder was a timestamp, which
 * never equals the baseline, so a read that failed because the page had
 * navigated or the context had closed came out as `domChanged: true` and
 * `verdictFromObservation` proved the control on it. An unreadable page is the
 * absence of evidence, and the callers below treat it as such.
 */
async function safeSignature(page: Page): Promise<string | null> {
  try {
    return await page.evaluate(signatureInPage);
  } catch {
    return null;
  }
}

/**
 * Runs `act` and reports every observable consequence.
 *
 * A baseline pass records the traffic and DOM churn the page produces on its
 * own, so a polling request or a chart animation is never mistaken for the
 * effect of a click.
 */
export async function observe(
  page: Page,
  act: () => Promise<void>,
): Promise<Observation> {
  const urlBefore = page.url();

  const baseline = new Set<string>();
  const baselineListener = (request: Request): void => {
    baseline.add(normalizeUrl(request.url()));
  };
  page.on("request", baselineListener);
  await page.waitForTimeout(250);
  page.off("request", baselineListener);

  const signatureA = await safeSignature(page);
  await page.waitForTimeout(250);
  const signatureB = await safeSignature(page);
  const domStableBaseline = signatureA !== null && signatureA === signatureB;
  const errorsBefore = new Set(
    await page.evaluate(errorSurfacesInPage).catch((): string[] => []),
  );

  const requests: string[] = [];
  const consoleErrors: string[] = [];
  let popup = false;
  let download = "";
  let dialog = "";

  let navStatus: number | null = null;
  const failedRequests: string[] = [];

  // The request objects seen during THIS activation's window. A response only
  // says something about a control when it answers a request that control
  // caused, and the window is roughly four and a half seconds against a 250
  // millisecond pre-click sample, so without this a background poll or the
  // late answer to something the page started before the click lands inside
  // the window and is read as this control's proof, or as its failure. Same
  // correlation the chat coverage suite settled on (PR #809, 09d39925).
  const mine = new Set<Request>();

  const onRequest = (request: Request): void => {
    const url = normalizeUrl(request.url());
    if (ASSET_PATTERN.test(url) || baseline.has(url)) {
      return;
    }
    mine.add(request);
    requests.push(`${request.method()} ${url}`);
  };
  const onResponse = (response: {
    status(): number;
    url(): string;
    request(): Request;
  }): void => {
    const request = response.request();
    const url = normalizeUrl(response.url());
    if (ASSET_PATTERN.test(url) || !mine.has(request)) {
      return;
    }
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
      navStatus = response.status();
    }
    if (response.status() >= 400) {
      failedRequests.push(`${response.status()} ${request.method()} ${url}`);
    }
  };
  const onPopup = (opened: Page): void => {
    popup = true;
    void opened.close().catch(() => undefined);
  };
  const onDownload = (item: { suggestedFilename(): string; cancel(): Promise<void> }): void => {
    download = item.suggestedFilename();
    void item.cancel().catch(() => undefined);
  };
  const onDialog = (item: {
    type(): string;
    message(): string;
    dismiss(): Promise<void>;
  }): void => {
    dialog = `${item.type()}: ${item.message().slice(0, 120)}`;
    void item.dismiss().catch(() => undefined);
  };
  const onConsole = (message: { type(): string; text(): string }): void => {
    if (message.type() === "error") {
      consoleErrors.push(message.text().slice(0, 200));
    }
  };

  page.on("request", onRequest);
  page.on("response", onResponse);
  page.on("popup", onPopup);
  page.on("download", onDownload);
  page.on("dialog", onDialog);
  page.on("console", onConsole);

  let actError = "";
  try {
    await act();
    // Only pay for a settle wait when the activation actually moved something.
    // The common failure this gate exists to catch is a control that fires
    // nothing at all, and charging four seconds of idle polling for each of
    // those turns a twenty minute run into a three hour one.
    await page.waitForTimeout(250);
    if (requests.length > 0 || page.url() !== urlBefore) {
      await page.waitForLoadState("networkidle", { timeout: 4000 }).catch(() => undefined);
      await page.waitForTimeout(250);
    }
  } catch (error) {
    actError = String(error).split("\n")[0].slice(0, 200);
  } finally {
    // In a finally, because the settle waits above throw whenever the page or
    // the context has closed, and this loop runs over hundreds of controls in
    // one process. Leaked listeners accumulate, and the leaked dialog and
    // download handlers go on dismissing dialogs and cancelling downloads for
    // later controls, destroying the evidence those observations need.
    page.off("request", onRequest);
    page.off("response", onResponse);
    page.off("popup", onPopup);
    page.off("download", onDownload);
    page.off("dialog", onDialog);
    page.off("console", onConsole);
  }

  const signatureAfter = await safeSignature(page);
  const urlAfter = page.url();
  const errorsAfter = await page.evaluate(errorSurfacesInPage).catch((): string[] => []);

  return {
    urlBefore,
    urlAfter,
    urlChanged: normalizeUrl(urlBefore) !== normalizeUrl(urlAfter),
    requests,
    domChanged: domStableBaseline && signatureAfter !== null && signatureAfter !== signatureB,
    domStableBaseline,
    popup,
    download,
    dialog,
    consoleErrors,
    actError,
    navStatus,
    failedRequests,
    errorSurfaces: errorsAfter.filter(
      (text) => !errorsBefore.has(text) && FAILURE_TEXT.test(text),
    ),
  };
}

export interface ProofOutcome {
  proven: boolean;
  /** How the effect was proven, or why it could not be. */
  proofType: string;
  detail: string;
}

/** Proof types that are neither a pass nor a failure of the control itself. */
export const DISABLED_PROOF_TYPE = "disabled";

/**
 * How a failed request is classified.
 *
 * `broken-endpoint`  the route is not mounted, or the server crashed on it.
 * `rate-limited`     the gate itself ran into a limiter, so the verdict is
 *                    unusable; reported rather than counted either way.
 * `failed-request`   the server read the request and refused it.
 *
 * Only the last of these can ever be evidence of a working control, and it
 * never is here: the gate blocks the requests it fed the values for, so a 4xx
 * that does come back was the application's own doing.
 */
const NOT_MOUNTED = new Set([404, 405, 410, 501]);

function statusOf(entry: string): number {
  return Number.parseInt(entry.slice(0, 3), 10);
}

export interface ProofContext {
  /**
   * Mutating requests the guard stopped during this activation.
   *
   * Their presence is the proof, and it is read before anything else that
   * could veto it: the application asked the server to change something, so
   * the control is wired, and the error the aborted request then puts on
   * screen is the gate's doing rather than a defect.
   */
  blockedMutations?: string[];
}

/** Reduces an observation to a verdict, using the generic evidence channels. */
export function verdictFromObservation(
  observation: Observation,
  context: ProofContext = {},
): ProofOutcome {
  const blocked = context.blockedMutations ?? [];
  if (blocked.length > 0) {
    return {
      proven: true,
      proofType: "wired-write-blocked",
      detail: `activation issued ${String(blocked.length)} state-changing request(s), stopped before leaving the browser: ${blocked.slice(0, 3).join(", ")}`,
    };
  }

  // A navigation that landed on a live document is proof on its own, and it is
  // settled before the request log is judged: everything the destination
  // fetches on load belongs to the destination, not to the control that got
  // there, and blaming a control for its target page background traffic is
  // exactly the false accusation this gate must never make.
  if (observation.navStatus !== null && observation.navStatus >= 400) {
    return {
      proven: false,
      proofType: "broken-endpoint",
      detail: `navigated to ${observation.urlAfter}, which answered ${String(observation.navStatus)}`,
    };
  }
  if (observation.urlChanged) {
    return {
      proven: true,
      proofType: "navigation",
      detail: `${observation.urlBefore} -> ${observation.urlAfter}`,
    };
  }

  const failures = observation.failedRequests;
  const notMounted = failures.filter((entry) => {
    const status = statusOf(entry);
    return NOT_MOUNTED.has(status) || status >= 500;
  });
  if (notMounted.length > 0) {
    return {
      proven: false,
      proofType: "broken-endpoint",
      detail: `activation called an endpoint that is not mounted or that failed: ${notMounted.slice(0, 3).join(", ")}`,
    };
  }
  const limited = failures.filter((entry) => statusOf(entry) === 429);
  if (limited.length > 0) {
    return {
      proven: false,
      proofType: "rate-limited",
      detail: `activation was rate limited, so it produced no usable verdict: ${limited.slice(0, 3).join(", ")}`,
    };
  }
  const unexcused = failures.filter((entry) => {
    const status = statusOf(entry);
    return !(status === 429 || status >= 500 || NOT_MOUNTED.has(status));
  });
  if (unexcused.length > 0) {
    return {
      proven: false,
      proofType: "failed-request",
      detail: `activation sent a request the server refused: ${unexcused.slice(0, 3).join(", ")}`,
    };
  }

  // An activation whose only visible consequence is an error message did not
  // work, whatever the transport said.
  if (observation.errorSurfaces.length > 0) {
    return {
      proven: false,
      proofType: "error-surface",
      detail: `activation raised an error to the user: ${observation.errorSurfaces.slice(0, 2).join(" | ")}`,
    };
  }

  if (observation.download !== "") {
    return { proven: true, proofType: "download", detail: observation.download };
  }
  if (observation.popup) {
    return { proven: true, proofType: "popup", detail: "opened a new page" };
  }
  if (observation.dialog !== "") {
    return { proven: true, proofType: "dialog", detail: observation.dialog };
  }
  if (observation.requests.length > 0) {
    return {
      proven: true,
      proofType: "network",
      detail: observation.requests.slice(0, 3).join(", "),
    };
  }
  if (observation.domChanged) {
    return { proven: true, proofType: "dom", detail: "rendered output changed" };
  }
  if (!observation.domStableBaseline) {
    return {
      proven: false,
      proofType: "unproven",
      detail:
        "no request, no navigation, and the page was already mutating on its own so DOM evidence was unusable",
    };
  }
  return {
    proven: false,
    proofType: "unproven",
    detail:
      observation.actError !== ""
        ? `activation failed: ${observation.actError}`
        : "activation produced no request, no navigation, and no change to the rendered output",
  };
}

/**
 * A disabled control is never proof of anything.
 *
 * It used to count as proven whenever the markup carried any `title`, which
 * made "disabled" the cheapest way to pass this gate: a permission regression
 * that greys out every action on a page would have kept it green, and so would
 * a feature that broke into a permanently disabled state. Markup is not
 * behaviour.
 *
 * So a disabled control is reported in its own bucket, never in the numerator,
 * and the controls that have to be usable are named in route-floors.json,
 * where "present and enabled" is asserted per route. That is the check an
 * accidental disable actually fails.
 */
export function verdictForDisabled(control: RawControl): ProofOutcome {
  const reason = control.disabledReason.trim();
  return {
    proven: false,
    proofType: DISABLED_PROOF_TYPE,
    detail:
      reason === ""
        ? "rendered disabled, with no reason exposed to the user (no title, no aria-describedby)"
        : `rendered disabled: ${reason}`,
  };
}

/**
 * Toggle proof, the case the owner named: flipping has to survive a reload.
 *
 * `relocate` must re-navigate and return the same control after a full page
 * load, so the value being read back is the one the server returned and not
 * the one React is holding in memory.
 */
export async function proveToggle(
  page: Page,
  locator: Locator,
  relocate: () => Promise<Locator | null>,
): Promise<ProofOutcome> {
  const before = await readToggleState(locator);
  if (before === null) {
    return {
      proven: false,
      proofType: "unproven",
      detail: "toggle exposes no checked state to read (no checked property, no aria-checked)",
    };
  }

  const observation = await observe(page, async () => {
    await locator.click({ timeout: 5000 });
  });

  const reloaded = await relocate();
  if (reloaded === null) {
    return {
      proven: false,
      proofType: "unproven",
      detail: "toggle could not be found again after reload, so persistence is unverifiable",
    };
  }
  const after = await readToggleState(reloaded);
  if (after === null) {
    return {
      proven: false,
      proofType: "unproven",
      detail: "toggle state unreadable after reload",
    };
  }
  if (after === before) {
    const context = verdictFromObservation(observation);
    return {
      proven: false,
      proofType: "unproven",
      detail: `flipping the toggle did not persist: it read ${String(before)} before the flip and ${String(after)} again after a reload (${context.proofType}: ${context.detail})`,
    };
  }

  // Restore, and confirm the restore also persisted. A failed restore is a
  // failure of the run, not a footnote: this is the one activation the gate
  // performs that deliberately changes real state, and the only thing that
  // makes that acceptable is putting it back. Reporting the control as proven
  // while walking away from a flipped setting is how a test suite becomes the
  // thing that broke the environment.
  const restoreObservation = await observe(page, async () => {
    await reloaded.click({ timeout: 5000 });
  });
  const restored = await relocate();
  const finalState = restored === null ? null : await readToggleState(restored);
  if (finalState !== before) {
    const context = verdictFromObservation(restoreObservation);
    return {
      proven: false,
      proofType: "unrestored",
      detail: `flip ${String(before)} -> ${String(after)} survived a reload, but the gate could not put it back: it now reads ${String(finalState)} (${context.proofType}: ${context.detail}). Restore this setting by hand`,
    };
  }

  return {
    proven: true,
    proofType: "persisted",
    detail: `flip ${String(before)} -> ${String(after)} survived a reload; restored`,
  };
}

async function readToggleState(locator: Locator): Promise<boolean | null> {
  return locator.evaluate((element: Element): boolean | null => {
    if (element instanceof HTMLInputElement) {
      return element.checked;
    }
    const aria = element.getAttribute("aria-checked") ?? element.getAttribute("aria-pressed");
    if (aria === "true") {
      return true;
    }
    if (aria === "false") {
      return false;
    }
    // Common headless-toggle pattern: state lives in a data attribute.
    const data = element.getAttribute("data-state");
    if (data === "checked" || data === "on") {
      return true;
    }
    if (data === "unchecked" || data === "off") {
      return false;
    }
    return null;
  });
}

/**
 * A form field's effect is the value it contributes to a submission.
 *
 * Two ways to prove it, and a name attribute on its own is not one of them:
 *
 *   1. Typing produces an observable consequence. For a controlled React input
 *      this is the strong case, and it is why the DOM signature tracks the
 *      disabled state of every control: typing into a field that gates a Save
 *      button visibly changes the page. It is also why deleting the onChange
 *      handler fails here, twice over, since React then reverts the value and
 *      the read-back below never matches.
 *   2. The field is named and lives inside a form, so the value it accepted is
 *      what that form submits.
 *
 * A named input in no form, whose edits change nothing anywhere, is exactly
 * the orphan this gate should report, and it used to pass on the name alone.
 */
export async function proveField(
  page: Page,
  locator: Locator,
  control: RawControl,
  guard: MutationGuard,
): Promise<ProofOutcome> {
  const original = await locator.inputValue().catch(() => "");
  const probe = probeValueFor(control.type);
  // The gate typed this value, so nothing it triggers may reach the server: an
  // autosaving field would otherwise persist "interaction gate probe" into a
  // real record.
  const { value: observation, blocked } = await guard.withWritesBlocked(() =>
    observe(page, async () => {
      await locator.fill(probe, { timeout: 5000 });
    }),
  );
  const readBack = await locator.inputValue().catch(() => "");
  await locator.fill(original, { timeout: 5000 }).catch(() => undefined);

  if (readBack !== probe) {
    return {
      proven: false,
      proofType: "unproven",
      detail: `field did not accept input: wrote "${probe}", read back "${readBack}"`,
    };
  }
  const generic = verdictFromObservation(observation, { blockedMutations: blocked });
  if (generic.proven) {
    return generic;
  }
  if (control.fieldName !== "" && control.inForm) {
    return {
      proven: true,
      proofType: "form-field",
      detail:
        control.formAction === ""
          ? `accepts input and submits as "${control.fieldName}" of a client submitted form`
          : `accepts input and submits as "${control.fieldName}" to ${control.formMethod.toUpperCase()} ${control.formAction}`,
    };
  }
  return {
    proven: false,
    proofType: "unproven",
    detail:
      "accepts input but belongs to no form and typing into it triggers no request and no rendered change, so nothing it holds can reach anywhere",
  };
}

/**
 * Fills the empty fields of the form a submit control belongs to.
 *
 * A submit button activated against an empty form is stopped by the browser's
 * own required-field validation, which looks exactly like a button with no
 * handler. Proving a submit means submitting something the form accepts.
 * Returns the fields it touched, so the report can say what was submitted.
 *
 * Every caller arms the mutation guard around the click that follows, because
 * these values are the gate's invention: a live run of the earlier version
 * created API keys and sent a workspace invitation this way.
 */
export async function fillFormFor(locator: Locator): Promise<string[]> {
  const form = locator.locator("xpath=ancestor::form[1]");
  if ((await form.count()) === 0) {
    return [];
  }
  const fields = form.locator(
    "input:not([type=hidden]):not([type=submit]):not([type=button]):not([type=checkbox]):not([type=radio]), textarea",
  );
  const filled: string[] = [];
  const count = await fields.count();
  for (let index = 0; index < count; index += 1) {
    const field = fields.nth(index);
    if (await field.isDisabled().catch(() => true)) {
      continue;
    }
    const current = await field.inputValue().catch(() => "");
    if (current !== "") {
      continue;
    }
    const descriptor = await field.evaluate((element: Element) => ({
      type: element.getAttribute("type") ?? "",
      name: element.getAttribute("name") ?? element.getAttribute("id") ?? "",
    }));
    const probe = probeValueFor(descriptor.type);
    await field.fill(probe, { timeout: 5000 }).catch(() => undefined);
    filled.push(descriptor.name === "" ? descriptor.type : descriptor.name);
  }
  return filled;
}

function probeValueFor(type: string): string {
  switch (type) {
    case "email":
      return "interaction-gate@example.invalid";
    case "number":
      return "1";
    case "url":
      return "https://example.invalid";
    case "tel":
      return "0000000000";
    case "date":
      return "2026-01-01";
    case "password":
      return "interaction-gate-probe";
    default:
      return "interaction gate probe";
  }
}

/** Selects a different option and reports the observed consequence. */
export async function proveSelect(
  page: Page,
  locator: Locator,
  control: RawControl,
  guard: MutationGuard,
): Promise<ProofOutcome> {
  const options = await locator.evaluate((element: Element): string[] => {
    if (!(element instanceof HTMLSelectElement)) {
      return [];
    }
    return Array.from(element.options).map((option) => option.value);
  });
  const current = await locator.inputValue().catch(() => "");
  const next = options.find((value) => value !== current);
  if (next === undefined) {
    if (control.fieldName !== "" && control.inForm) {
      return {
        proven: true,
        proofType: "form-field",
        detail: `single-option select bound to "${control.fieldName}" of a form`,
      };
    }
    return {
      proven: false,
      proofType: "unproven",
      detail: "select offers no alternative option and is not bound to any form",
    };
  }

  // The gate chose this option, so the same rule as a typed value applies.
  const { value: observation, blocked } = await guard.withWritesBlocked(() =>
    observe(page, async () => {
      await locator.selectOption(next, { timeout: 5000 });
    }),
  );
  const generic = verdictFromObservation(observation, { blockedMutations: blocked });
  if (generic.proven) {
    return generic;
  }
  await locator.selectOption(current, { timeout: 5000 }).catch(() => undefined);
  if (control.fieldName !== "" && control.inForm) {
    return {
      proven: true,
      proofType: "form-field",
      detail: `changing the value submits as "${control.fieldName}" of a form`,
    };
  }
  return {
    proven: false,
    proofType: "unproven",
    detail: "changing the selection triggers no request, no navigation, and no rendered change",
  };
}

/**
 * External links, and why their destination is not part of the verdict.
 *
 * Clicking an anchor whose href is an absolute http(s) URL always navigates:
 * that is a browser primitive, not application behaviour, so the control's own
 * effect is proven by the href being well formed. Whether the far end answers
 * is a fact about somebody else's server, and hanging a merge gate on a third
 * party's uptime buys a flaky red instead of a finding. The destinations are
 * checked anyway and reported in their own section of the ledger, which is
 * where the dead `https://hivegpt.io` documentation link surfaced (issue #883).
 *
 * Checked once per destination. A shell link repeated on seventeen routes is
 * one destination, and asking a third party seventeen times is slow and a good
 * way to be rate limited into a meaningless answer.
 */
export interface ExternalDestination {
  href: string;
  reachable: boolean;
  detail: string;
}

const externalDestinations = new Map<string, ExternalDestination>();

/** Every external destination checked so far, for the report. */
export function externalDestinationReport(): ExternalDestination[] {
  return [...externalDestinations.values()].sort((a, b) => a.href.localeCompare(b.href));
}

export async function proveExternalLink(
  page: Page,
  href: string,
): Promise<ProofOutcome> {
  if (!/^https?:\/\//i.test(href)) {
    return {
      proven: false,
      proofType: "unproven",
      detail: `external link href "${href}" is not an absolute http(s) URL, so clicking it does nothing a browser understands`,
    };
  }
  if (!externalDestinations.has(href)) {
    externalDestinations.set(href, await checkExternalDestination(page, href));
  }
  const destination = externalDestinations.get(href);
  return {
    proven: true,
    proofType: "external-link",
    detail:
      destination === undefined || destination.reachable
        ? `navigates to ${redactUrl(href)}`
        : `navigates to ${redactUrl(href)}, but ${destination.detail} (reported, not counted against the control)`,
  };
}

async function checkExternalDestination(
  page: Page,
  href: string,
): Promise<ExternalDestination> {
  try {
    const response = await page.request.get(href, { timeout: 15000, maxRedirects: 5 });
    return {
      href,
      reachable: response.status() < 400,
      detail: `answered ${String(response.status())}`,
    };
  } catch (error) {
    return {
      href,
      reachable: false,
      detail: `is unreachable: ${String(error).split("\n")[0].slice(0, 160)}`,
    };
  }
}
