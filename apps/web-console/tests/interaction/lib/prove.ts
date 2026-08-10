// Activation and effect proof.
//
// Rendering is not functioning. Every enumerated control is activated for
// real and has to produce something observable: a network request, a URL
// change, a DOM mutation, a download, a popup, a native dialog, or (for a
// toggle) a value that survives a reload because the server gave it back.
// A control that produces none of those is a failure, not a skip.

import type { Locator, Page, Request } from "@playwright/test";

import { signatureInPage, type RawControl } from "./enumerate";

/** Controls whose activation is irreversible enough to need care. */
export const DESTRUCTIVE_PATTERN =
  /\b(delete|remove|revoke|destroy|deactivate|purchase|checkout|top ?up|pay now)\b/i;

/** Controls that end the session, so they run last and in a throwaway context. */
export const SESSION_ENDING_PATTERN = /\b(sign ?out|log ?out|logout|signout)\b/i;

const ASSET_PATTERN =
  /(_next\/static|__nextjs|\/favicon|\.(png|jpe?g|gif|svg|webp|ico|woff2?|ttf|css|map)(\?|$))/i;

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
  return url.split("#")[0];
}

async function safeSignature(page: Page): Promise<string> {
  try {
    return await page.evaluate(signatureInPage);
  } catch {
    return `unavailable:${Date.now()}`;
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
  const domStableBaseline = signatureA === signatureB;
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

  const onRequest = (request: Request): void => {
    const url = normalizeUrl(request.url());
    if (ASSET_PATTERN.test(url) || baseline.has(url)) {
      return;
    }
    requests.push(`${request.method()} ${url}`);
  };
  const onResponse = (response: {
    status(): number;
    url(): string;
    request(): Request;
  }): void => {
    const request = response.request();
    const url = normalizeUrl(response.url());
    if (ASSET_PATTERN.test(url)) {
      return;
    }
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) {
      navStatus = response.status();
    }
    if (response.status() >= 400 && !baseline.has(url)) {
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
  } catch (error) {
    actError = String(error).split("\n")[0].slice(0, 200);
  }
  // Only pay for a settle wait when the activation actually moved something.
  // The common failure this gate exists to catch is a control that fires
  // nothing at all, and charging four seconds of idle polling for each of
  // those turns a twenty minute run into a three hour one.
  await page.waitForTimeout(250);
  if (requests.length > 0 || page.url() !== urlBefore) {
    await page.waitForLoadState("networkidle", { timeout: 4000 }).catch(() => undefined);
    await page.waitForTimeout(250);
  }

  page.off("request", onRequest);
  page.off("response", onResponse);
  page.off("popup", onPopup);
  page.off("download", onDownload);
  page.off("dialog", onDialog);
  page.off("console", onConsole);

  const signatureAfter = await safeSignature(page);
  const urlAfter = page.url();
  const errorsAfter = await page.evaluate(errorSurfacesInPage).catch((): string[] => []);

  return {
    urlBefore,
    urlAfter,
    urlChanged: normalizeUrl(urlBefore) !== normalizeUrl(urlAfter),
    requests,
    domChanged: domStableBaseline && signatureAfter !== signatureB,
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

/**
 * How a failed request is classified.
 *
 * `broken-endpoint`  the route is not mounted, or the server crashed on it.
 * `rate-limited`     the gate itself ran into a limiter, so the verdict is
 *                    unusable; reported rather than counted either way.
 * `failed-request`   the server read the request and refused it.
 *
 * Only the last of these can ever be evidence of a working control, and only
 * under the condition documented on `ProofContext.harnessSuppliedInput`.
 */
const NOT_MOUNTED = new Set([404, 405, 410, 501]);
const SEMANTIC_REJECTION = new Set([400, 409, 422]);

function statusOf(entry: string): number {
  return Number.parseInt(entry.slice(0, 3), 10);
}

export interface ProofContext {
  /**
   * True when the harness itself supplied the values the request carried.
   *
   * This is the whole rule for telling an expected 4xx from an unexpected one,
   * and it is deliberately a property of the *activation*, not of the status.
   * When the gate fills a form with "interaction gate probe" and submits it, a
   * 400, 409 or 422 back is the endpoint validating input, which is the
   * control working. Nothing else earns the exception: 401 and 403 mean the
   * session is wrong, 404, 405, 410 and 501 mean nothing is mounted there, 429
   * means the gate hit a limiter, and 5xx means the server fell over. None of
   * those is evidence that a control did its job, and a 400 on an activation
   * the gate did not feed is the application sending a request its own server
   * will not accept, which is a defect and not a proof.
   */
  harnessSuppliedInput?: boolean;
}

/** Reduces an observation to a verdict, using the generic evidence channels. */
export function verdictFromObservation(
  observation: Observation,
  context: ProofContext = {},
): ProofOutcome {
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
    if (status === 429 || status >= 500 || NOT_MOUNTED.has(status)) {
      return false;
    }
    if (SEMANTIC_REJECTION.has(status)) {
      return context.harnessSuppliedInput !== true;
    }
    return true;
  });
  if (unexcused.length > 0) {
    return {
      proven: false,
      proofType: "failed-request",
      detail: `activation sent a request the server refused: ${unexcused.slice(0, 3).join(", ")}`,
    };
  }

  // An activation whose only visible consequence is an error message did not
  // work, whatever the transport said. Excused on the same terms as a 4xx:
  // when the gate typed the value that was rejected, the message is the
  // application validating the gate own nonsense, which is it working.
  if (observation.errorSurfaces.length > 0 && context.harnessSuppliedInput !== true) {
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
 * A disabled control is allowed to do nothing, but only when the markup says
 * why. "Disabled with no explanation" is indistinguishable from "broken".
 */
export function verdictForDisabled(control: RawControl): ProofOutcome {
  if (control.disabledReason.trim() !== "") {
    return {
      proven: true,
      proofType: "disabled-with-reason",
      detail: control.disabledReason.trim(),
    };
  }
  return {
    proven: false,
    proofType: "unproven",
    detail: "disabled with no reason exposed to the user (no title, no aria-describedby)",
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

  // Restore, and confirm the restore also persisted. Leaving a live surface
  // flipped would be a side effect of the gate itself.
  const restoreObservation = await observe(page, async () => {
    await reloaded.click({ timeout: 5000 });
  });
  const restored = await relocate();
  const finalState = restored ? await readToggleState(restored) : null;
  const restoreNote =
    finalState === before
      ? "restored"
      : `WARNING: could not restore the original value (${restoreObservation.requests.slice(0, 2).join(", ")})`;

  return {
    proven: true,
    proofType: "persisted",
    detail: `flip ${String(before)} -> ${String(after)} survived a reload; ${restoreNote}`,
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
 * A form field's effect is the value it contributes to a submission. Proving
 * it means the value is accepted by the field and the field is actually wired
 * into a form (a name plus a form action), or the edit itself moves the page.
 */
export async function proveField(
  page: Page,
  locator: Locator,
  control: RawControl,
): Promise<ProofOutcome> {
  const original = await locator.inputValue().catch(() => "");
  const probe = probeValueFor(control.type);
  const observation = await observe(page, async () => {
    await locator.fill(probe, { timeout: 5000 });
  });
  const readBack = await locator.inputValue().catch(() => "");
  await locator.fill(original, { timeout: 5000 }).catch(() => undefined);

  if (readBack !== probe) {
    return {
      proven: false,
      proofType: "unproven",
      detail: `field did not accept input: wrote "${probe}", read back "${readBack}"`,
    };
  }
  const generic = verdictFromObservation(observation, { harnessSuppliedInput: true });
  if (generic.proven) {
    return generic;
  }
  if (control.fieldName !== "" && control.formAction !== "") {
    return {
      proven: true,
      proofType: "form-field",
      detail: `accepts input and submits as "${control.fieldName}" to ${control.formMethod.toUpperCase()} ${control.formAction}`,
    };
  }
  if (control.fieldName !== "") {
    return {
      proven: true,
      proofType: "form-field",
      detail: `accepts input and is bound to field "${control.fieldName}" of a client submitted form`,
    };
  }
  return {
    proven: false,
    proofType: "unproven",
    detail:
      "accepts input but has no name, no form action, and typing into it triggers no request and no rendered change",
  };
}

/**
 * Fills the empty fields of the form a submit control belongs to.
 *
 * A submit button activated against an empty form is stopped by the browser's
 * own required-field validation, which looks exactly like a button with no
 * handler. Proving a submit means submitting something the form accepts.
 * Returns the fields it touched, so the report can say what was submitted.
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
    if (control.fieldName !== "" && control.formAction !== "") {
      return {
        proven: true,
        proofType: "form-field",
        detail: `single-option select bound to "${control.fieldName}" of ${control.formMethod.toUpperCase()} ${control.formAction}`,
      };
    }
    return {
      proven: false,
      proofType: "unproven",
      detail: "select offers no alternative option and is not bound to any form",
    };
  }

  const observation = await observe(page, async () => {
    await locator.selectOption(next, { timeout: 5000 });
  });
  const generic = verdictFromObservation(observation);
  if (generic.proven) {
    return generic;
  }
  await locator.selectOption(current, { timeout: 5000 }).catch(() => undefined);
  if (control.fieldName !== "" && control.formAction !== "") {
    return {
      proven: true,
      proofType: "form-field",
      detail: `changing the value submits as "${control.fieldName}" to ${control.formMethod.toUpperCase()} ${control.formAction}`,
    };
  }
  return {
    proven: false,
    proofType: "unproven",
    detail: "changing the selection triggers no request, no navigation, and no rendered change",
  };
}

/**
 * External links are browser primitives: the click always navigates, so the
 * only thing worth proving is that the destination is alive.
 */
export async function proveExternalLink(
  page: Page,
  href: string,
): Promise<ProofOutcome> {
  try {
    const response = await page.request.get(href, { timeout: 15000, maxRedirects: 5 });
    if (response.status() >= 400) {
      return {
        proven: false,
        proofType: "unproven",
        detail: `external destination ${href} answered ${response.status()}`,
      };
    }
    return {
      proven: true,
      proofType: "external-link",
      detail: `${href} answered ${response.status()}`,
    };
  } catch (error) {
    return {
      proven: false,
      proofType: "unproven",
      detail: `external destination ${href} is unreachable: ${String(error).split("\n")[0].slice(0, 160)}`,
    };
  }
}
