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
  };
}

export interface ProofOutcome {
  proven: boolean;
  /** How the effect was proven, or why it could not be. */
  proofType: string;
  detail: string;
}

/**
 * Requests whose status means the endpoint is not there at all, as opposed to
 * a server that answered the semantics of the call. A control that fires a
 * request into a route nobody mounted is broken, not proven: that is the
 * exact shape of the console's api-keys, spend-alerts, budget and checkout
 * defects, each of which fires a perfectly observable request into a 404.
 */
const MISSING_ENDPOINT_STATUS = /^(404|405|410|501|5\d\d) /;

/** Reduces an observation to a verdict, using the generic evidence channels. */
export function verdictFromObservation(observation: Observation): ProofOutcome {
  const missing = observation.failedRequests.filter((entry) =>
    MISSING_ENDPOINT_STATUS.test(entry),
  );
  if (missing.length > 0) {
    return {
      proven: false,
      proofType: "broken-endpoint",
      detail: `activation called an endpoint that is not mounted: ${missing.slice(0, 3).join(", ")}`,
    };
  }
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
  const probe = probeValueFor(control);
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
  const generic = verdictFromObservation(observation);
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

function probeValueFor(control: RawControl): string {
  switch (control.type) {
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
