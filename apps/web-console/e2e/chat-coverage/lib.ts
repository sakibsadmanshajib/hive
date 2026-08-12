// Live interaction-coverage engine for the Hive chat surface (Open WebUI).
//
// The rule this file exists to enforce: every control the chat app renders has
// to be shown to *do something*, live, against a running deployment. Nothing
// here carries a hand-written list of controls. The control set is read out of
// the rendered DOM on every run, so a control added upstream (or by a Hive
// patch) is enumerated the first time it renders and has to earn a proof or
// fail the gate. A hand-written inventory would go stale silently, which is
// the exact failure mode the owner named.
import type { Page, Locator } from "@playwright/test";

import { redactText, redactUrl } from "../../tests/e2e/support/redact";

export { redactText, redactUrl };

declare global {
  interface Window {
    /** Mutation records counted by the watcher below. */
    __covMut?: number;
    __covObs?: MutationObserver;
    /** Events the sabotage hook intercepted, for the self-test. */
    __covBlocked?: number;
  }
}

// Everything a user can reach with a mouse or a keyboard. Roles are included
// alongside tags because Open WebUI builds switches, tabs and menu items out of
// plain <button>s with ARIA roles.
export const SELECTOR = [
  "a[href]",
  "button",
  "input:not([type=hidden])",
  "select",
  "textarea",
  "summary",
  "[role=button]",
  "[role=link]",
  "[role=menuitem]",
  "[role=menuitemcheckbox]",
  "[role=menuitemradio]",
  "[role=tab]",
  "[role=switch]",
  "[role=checkbox]",
  "[role=radio]",
  "[role=option]",
  "[role=combobox]",
  "[role=slider]",
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])',
].join(", ");

export type Control = {
  key: string;
  surface: string;
  covId: string;
  tag: string;
  type: string;
  role: string;
  name: string;
  label: string;
  disabled: boolean;
  reason: string;
  state: string | null;
  href: string;
  contentEditable: boolean;
};

export type Proof =
  | "navigate"
  | "network"
  | "dom"
  | "overlay"
  | "download"
  | "filechooser"
  | "value"
  | "persisted"
  // Checked but deliberately never activated: a control whose own name says it
  // destroys data, or one the app renders disabled. Its own category because
  // it is NOT proof that the control works, and it is NOT excused either: the
  // gate fails on it unless inert-registry.json carries a justification for
  // that key. Two earlier versions of this file counted both as coverage, and
  // a disabled control with a title attribute was worth as much as a working
  // one, so #846 could have been "fixed" by disabling Admin Settings.
  | "not-fired";

export type Result = {
  key: string;
  surface: string;
  name: string;
  role: string;
  proven: boolean;
  proof: Proof | "none";
  detail: string;
};

/**
 * A control that was checked but not activated. Not proven, and not a
 * failure either: it is carried in its own bucket so it can never be read as
 * coverage, and so the count of controls nobody dares fire is visible.
 */
export function isDeferred(result: Result): boolean {
  return result.proof === "not-fired";
}

/**
 * Assets and background chatter never count as evidence that a control did
 * something. socket.io is the important exclusion: Open WebUI long-polls it
 * every few seconds, so counting it would hand a free pass to every control
 * whose click happened to land near a poll.
 */
const NOT_MOUNTED = new Set([404, 405, 410, 501]);

/**
 * Wording an application uses to say that what the user asked for did not
 * happen. Narrow on purpose: a live region that announces a result is not a
 * failure, and treating one as a failure trades a false pass for a false
 * accusation.
 */
const FAILURE_TEXT =
  /\b(error|failed|failure|unable to|could ?n[o']t|went wrong|try again|invalid|denied|unauthori[sz]ed|forbidden|not found|timed out|too many requests)\b/i;

function isMeaningfulRequest(url: string): boolean {
  if (/\.(js|mjs|css|png|jpe?g|svg|webp|woff2?|ico|map)(\?|$)/i.test(url)) return false;
  if (/\/_app\/|\/static\/|\/favicon/i.test(url)) return false;
  if (/\/socket\.io\/|\/ws(\/|$)|\/__nextjs/i.test(url)) return false;
  return true;
}

/**
 * The shape of a request, with everything that varies between two otherwise
 * identical calls removed: the query string, and every id-looking path
 * segment. Two polls of the same endpoint collapse to one signature, so a poll
 * observed while nothing was happening can be recognised again when it lands
 * inside a control's settle window.
 */
export function requestSignature(url: string): string {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    return url;
  }
  const path = parsed.pathname
    .split("/")
    .map((segment) =>
      /^[0-9a-f-]{8,}$/i.test(segment) || /^\d+$/.test(segment) ? "#" : segment,
    )
    .join("/");
  return parsed.origin + path;
}

/**
 * Requests the page makes on its own, sampled with nobody touching it.
 *
 * Open WebUI polls REST endpoints as well as socket.io, and any poll that
 * lands inside a control's settle window used to be counted as that control's
 * network proof. Only socket.io was excluded, by name, which covered one
 * poller out of several. Sampling the idle page instead names them all, for
 * whatever the deployment actually does, and nothing that fires while nobody
 * is clicking can be evidence that a click did something.
 */
export async function sampleChatter(page: Page, ms: number): Promise<Set<string>> {
  const seen = new Set<string>();
  const onRequest = (r: { url: () => string }) => {
    const u = r.url();
    if (isMeaningfulRequest(u)) seen.add(requestSignature(u));
  };
  page.on("request", onRequest);
  await page.waitForTimeout(ms);
  page.off("request", onRequest);
  return seen;
}

// Runs inside the page. Tags every visible interactive element with a
// data-cov-id so the prover can find it again after a re-render, and returns a
// descriptor for each. Labels for unnamed controls (Open WebUI renders ~40
// nameless toggle buttons in Settings > Interface alone) are lifted from the
// nearest ancestor that carries readable text, which is what a sighted user
// reads as the control's name.
const ENUMERATE = (args: { selector: string; surface: string; scope: string | null }) => {
  const root = args.scope ? document.querySelector(args.scope) : document.body;
  if (!root) return [];
  const visible = (el: Element): boolean => {
    const r = el.getBoundingClientRect();
    if (r.width === 0 && r.height === 0) return false;
    if (el.closest('[aria-hidden="true"]')) return false;
    const s = getComputedStyle(el);
    return s.visibility !== "hidden" && s.display !== "none" && s.opacity !== "0";
  };
  const direct = (el: Element): string =>
    (
      el.getAttribute("aria-label") ||
      el.getAttribute("placeholder") ||
      el.getAttribute("title") ||
      (el as HTMLElement).innerText ||
      el.getAttribute("name") ||
      ""
    )
      .replace(/\s+/g, " ")
      .trim()
      .slice(0, 70);
  const ambient = (el: Element): string => {
    let node: Element | null = el.parentElement;
    for (let i = 0; i < 5 && node; i++, node = node.parentElement) {
      const text = (node as HTMLElement).innerText?.replace(/\s+/g, " ").trim() ?? "";
      if (text.length > 0 && text.length < 140) return text.slice(0, 70);
    }
    return "";
  };
  const stateOf = (el: Element): string | null => {
    const tag = el.tagName.toLowerCase();
    const role = el.getAttribute("role") ?? "";
    if (el.getAttribute("contenteditable") === "true") {
      return (el as HTMLElement).innerText;
    }
    if (role === "switch" || role === "checkbox") {
      return el.getAttribute("aria-checked") ?? (el as HTMLElement).innerText.trim();
    }
    if (tag === "select") return (el as HTMLSelectElement).value;
    if (tag === "input") {
      const input = el as HTMLInputElement;
      if (input.type === "checkbox" || input.type === "radio") return String(input.checked);
      if (input.type === "file") return null;
      return input.value;
    }
    if (tag === "textarea") return (el as HTMLTextAreaElement).value;
    return null;
  };
  const seen = new Map<string, number>();
  const out: Control[] = [];
  // Clear the previous enumeration's tags first. Ids are positional
  // (surface#index), so an element that was tagged on the last pass and is no
  // longer visible keeps an id the next pass hands to a different element, and
  // locate() then matches two nodes and its click is ambiguous.
  document.querySelectorAll("[data-cov-id]").forEach((el) => el.removeAttribute("data-cov-id"));
  const els = [...root.querySelectorAll(args.selector)].filter(visible);
  els.forEach((el, i) => {
    const tag = el.tagName.toLowerCase();
    const role = el.getAttribute("role") ?? "";
    const name = direct(el);
    const label = name || ambient(el);
    const base = `${args.surface}::${role || tag}::${label || "(unnamed)"}`;
    const n = (seen.get(base) ?? 0) + 1;
    seen.set(base, n);
    const covId = `${args.surface}#${i}`;
    el.setAttribute("data-cov-id", covId);
    out.push({
      key: n > 1 ? `${base}#${n}` : base,
      surface: args.surface,
      covId,
      tag,
      type: el.getAttribute("type") ?? "",
      role,
      name,
      label,
      disabled:
        el.hasAttribute("disabled") || el.getAttribute("aria-disabled") === "true",
      // Human-readable text only. aria-describedby used to be read here, and
      // it holds an element id, so "tooltip-7" was accepted as an explanation
      // of why a control is disabled.
      reason: el.getAttribute("title") ?? el.getAttribute("data-disabled-reason") ?? "",
      state: stateOf(el),
      href: el.getAttribute("href") ?? "",
      contentEditable: el.getAttribute("contenteditable") === "true",
    });
  });
  return out;
};

export async function enumerate(
  page: Page,
  surface: string,
  scope?: string,
): Promise<Control[]> {
  // No cast: ENUMERATE builds Control values, so page.evaluate carries the
  // type across on its own.
  return page.evaluate(ENUMERATE, {
    selector: SELECTOR,
    surface,
    scope: scope ?? null,
  });
}

export function locate(page: Page, ctl: Control): Locator {
  return page.locator(`[data-cov-id="${ctl.covId}"]`);
}

async function startWatch(page: Page): Promise<() => Promise<{ mutations: number }>> {
  await page.evaluate(() => {
    window.__covObs?.disconnect();
    window.__covMut = 0;
    const obs = new MutationObserver((records) => {
      window.__covMut = (window.__covMut ?? 0) + records.length;
    });
    obs.observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      characterData: true,
    });
    window.__covObs = obs;
  });
  return async () => {
    const mutations = await page.evaluate(() => {
      const n = window.__covMut ?? 0;
      window.__covObs?.disconnect();
      return n;
    });
    return { mutations };
  };
}

/**
 * Error surfaces on screen right now.
 *
 * Open WebUI reports a failed action as a toast, which is both a change to the
 * render and usually the tail of a request, so without this the two strongest
 * evidence channels both vote to prove a control that visibly did not work.
 */
async function errorSurfaces(page: Page): Promise<string[]> {
  return page
    .evaluate(() => {
      const nodes = document.querySelectorAll(
        '[role="alert"], [aria-live="assertive"], [data-sonner-toast], .toast-error, .Toastify__toast--error',
      );
      const out: string[] = [];
      nodes.forEach((node) => {
        if (node.getAttribute("role") === "alertdialog") return;
        const text = (node.textContent ?? "").replace(/\s+/g, " ").trim().slice(0, 200);
        if (text !== "") out.push(text);
      });
      return out;
    })
    .catch((): string[] => []);
}

async function overlayCount(page: Page): Promise<number> {
  return page.evaluate(
    () =>
      document.querySelectorAll('[role="dialog"], [role="menu"], [role="listbox"], [data-melt-dialog-content], .modal')
        .length,
  );
}

/**
 * A cheap fingerprint of what the user can currently see.
 *
 * Counting mutation records alone could not tell a settings tab switch (6 or 7
 * records) from idle churn, and any threshold that cleared the churn also
 * swallowed the tab switch. Comparing the rendered text before and after says
 * plainly whether the screen changed.
 */
async function visibleSignature(page: Page): Promise<string> {
  return page
    .evaluate(() => {
      const scope =
        document.querySelector('[role="dialog"]') ??
        document.querySelector("main") ??
        document.body;
      const text = (scope as HTMLElement).innerText ?? "";
      // Digits are stripped because relative timestamps ("2d", "3m ago") tick
      // on their own; without this a control that did nothing still came back
      // proven whenever a clock in the sidebar rolled over during its click.
      return text.replace(/\d+/g, "#").replace(/\s+/g, " ").trim().slice(0, 600);
    })
    .catch(() => "");
}

/**
 * Click a control and report the first observable consequence: a navigation, a
 * meaningful network call, a new overlay, a download, a file chooser, or a
 * non-trivial DOM mutation. No consequence at all is a failure, which is the
 * whole point of the gate.
 */
export type ProveOptions = {
  settleMs?: number;
  budgetMs?: number;
  /**
   * Request signatures the page produces on its own, from sampleChatter. A
   * request whose signature is in here is background noise and can never be
   * this control's proof.
   */
  ignoreRequests?: ReadonlySet<string>;
};

export async function proveByClick(
  page: Page,
  ctl: Control,
  opts: ProveOptions = {},
): Promise<Result> {
  // Hard budget. A single control that wedges the page (a native dialog, a
  // beforeunload prompt, a permission request) must not be able to consume the
  // whole run; it gets reported and the sweep moves on.
  const budget = opts.budgetMs ?? 40_000;
  return Promise.race([
    proveByClickInner(page, ctl, opts),
    new Promise<Result>((resolve) =>
      setTimeout(
        () =>
          resolve({
            key: ctl.key,
            surface: ctl.surface,
            name: ctl.label || ctl.name,
            role: ctl.role || ctl.tag,
            proven: false,
            proof: "none",
            detail: `no verdict within ${budget}ms (the page stopped responding to the harness)`,
          }),
        budget,
      ),
    ),
  ]);
}

async function proveByClickInner(
  page: Page,
  ctl: Control,
  opts: ProveOptions = {},
): Promise<Result> {
  const base = { key: ctl.key, surface: ctl.surface, name: ctl.label || ctl.name, role: ctl.role || ctl.tag };
  if (ctl.disabled) {
    // Never proof. A disabled control demonstrates nothing about whether the
    // thing behind it works, and counting one as coverage means a broken
    // control can be "fixed" by disabling it and the ratio goes UP. It needs a
    // justification in inert-registry.json like any other unfireable control.
    const reason = ctl.reason.trim();
    return {
      ...base,
      proven: false,
      proof: "not-fired",
      detail: reason ? `disabled: ${reason.slice(0, 100)}` : "disabled with no stated reason",
    };
  }

  const chatter = opts.ignoreRequests ?? new Set<string>();
  const requests: string[] = [];
  const onRequest = (r: { url: () => string }) => {
    const u = r.url();
    if (!isMeaningfulRequest(u)) return;
    if (chatter.has(requestSignature(u))) return;
    requests.push(u);
  };
  page.on("request", onRequest);
  // Statuses, not just URLs. This file never registered a response listener at
  // all, so a click that answered 500 and rendered an error toast came back
  // proven twice over, once on the request and once on the toast.
  const failed: string[] = [];
  // Answered, and answered well. A request on its own is not evidence that
  // anything happened at the other end: a control pointing at an endpoint that
  // hangs emits a request, never gets a response, and used to be proven for
  // ever on the strength of the request alone. That is the same "no server
  // verdict" hole that aborted writes had.
  const answered: string[] = [];
  const onResponse = (r: {
    status: () => number;
    url: () => string;
    request: () => { method: () => string };
  }) => {
    const u = r.url();
    if (!isMeaningfulRequest(u)) return;
    if (chatter.has(requestSignature(u))) return;
    if (r.status() >= 400) {
      failed.push(String(r.status()) + " " + r.request().method() + " " + redactUrl(u));
      return;
    }
    answered.push(String(r.status()) + " " + r.request().method() + " " + redactUrl(u));
  };
  page.on("response", onResponse);
  // A request that never reaches a server emits `request` and then
  // `requestfailed`, and no `response` at all. Without this listener a
  // connection reset, a DNS failure, or a write this suite's own safety net
  // aborted looks exactly like a successful call on the request channel, and
  // is credited as network proof with no verdict behind it.
  const transportFailed: string[] = [];
  const onRequestFailed = (r: {
    url: () => string;
    method: () => string;
    failure: () => { errorText: string } | null;
  }) => {
    const u = r.url();
    if (!isMeaningfulRequest(u)) return;
    if (chatter.has(requestSignature(u))) return;
    transportFailed.push(
      (r.failure()?.errorText ?? "failed") + " " + r.method() + " " + redactUrl(u),
    );
  };
  page.on("requestfailed", onRequestFailed);
  let download = false;
  let filechooser = false;
  const onDownload = () => {
    download = true;
  };
  // Cancel it immediately. An open file chooser is modal to the page, and a
  // registered handler that never answers leaves it open, which hangs every
  // action that follows.
  const onFileChooser = (chooser: { setFiles: (f: string[]) => Promise<void> }) => {
    filechooser = true;
    void chooser.setFiles([]).catch(() => {});
  };
  page.on("download", onDownload);
  page.on("filechooser", onFileChooser);
  // External links carry target=_blank, so their whole effect is a new tab.
  let popup = false;
  const onPopup = () => {
    popup = true;
  };
  page.context().on("page", onPopup);
  // Cleanup in a finally, not on the happy path. Everything below can throw
  // (the page can die mid-settle, and the budget race can abandon this call
  // entirely), and a throw used to leave five listeners and a MutationObserver
  // attached for the rest of a 45 minute run.
  const release = () => {
    page.off("request", onRequest);
    page.off("response", onResponse);
    page.off("requestfailed", onRequestFailed);
    page.off("download", onDownload);
    page.off("filechooser", onFileChooser);
    page.context().off("page", onPopup);
  };

  try {
    return await proveInner();
  } finally {
    release();
    await page
      .evaluate(() => {
        window.__covObs?.disconnect();
      })
      .catch(() => {});
  }

  async function proveInner(): Promise<Result> {
  const urlBefore = page.url();
  const overlaysBefore = await overlayCount(page);
  const signatureBefore = await visibleSignature(page);
  const errorsBefore = new Set(await errorSurfaces(page));
  const stop = await startWatch(page);

  let clickError = "";
  try {
    await locate(page, ctl).click({ timeout: 6000, trial: false });
  } catch (err) {
    // A control that refuses the click (intercepted, detached, not stable) is
    // reported rather than swallowed: silence here would read as "no effect"
    // for a reason that has nothing to do with the control's wiring.
    clickError = String(err).split("\n")[0].slice(0, 140);
  }
  await page.waitForTimeout(opts.settleMs ?? 1200);

  const { mutations } = await stop();
  const urlAfter = page.url();
  const overlaysAfter = await overlayCount(page);
  const signatureAfter = await visibleSignature(page);
  const errorsAfter = (await errorSurfaces(page)).filter(
    (text) => !errorsBefore.has(text) && FAILURE_TEXT.test(text),
  );
  // Failure first, and before any positive channel. Evidence that an
  // activation was refused is not evidence that the control works, and a
  // refusal produces a request and a rendered change just like a success does.
  // No harnessSuppliedInput exception is carved out here: nothing in this
  // sweep feeds a control synthetic input before clicking it, because typed
  // values are proven by reading state back rather than by clicking, so every
  // 4xx and 5xx a click provokes is unexpected by construction.
  const notMounted = failed.filter((e) => {
    const code = Number.parseInt(e.slice(0, 3), 10);
    return NOT_MOUNTED.has(code) || code >= 500;
  });
  const refused = failed.filter((e) => !notMounted.includes(e));
  if (notMounted.length > 0)
    return {
      ...base,
      proven: false,
      proof: "none",
      detail: "called an endpoint that is not mounted or that failed: " + notMounted.slice(0, 2).join(", ").slice(0, 180),
    };
  if (refused.length > 0)
    return {
      ...base,
      proven: false,
      proof: "none",
      detail: "the server refused the request it sent: " + refused.slice(0, 2).join(", ").slice(0, 180),
    };
  if (errorsAfter.length > 0)
    return {
      ...base,
      proven: false,
      proof: "none",
      detail: "raised an error to the user: " + errorsAfter.slice(0, 2).join(" | ").slice(0, 180),
    };
  if (transportFailed.length > 0)
    return {
      ...base,
      proven: false,
      proof: "none",
      detail:
        "its request never reached a server: " +
        transportFailed.slice(0, 2).join(", ").slice(0, 180),
    };

  if (download) return { ...base, proven: true, proof: "download", detail: "download started" };
  if (filechooser) return { ...base, proven: true, proof: "filechooser", detail: "file chooser opened" };
  if (popup) return { ...base, proven: true, proof: "navigate", detail: "opened a new tab" };
  if (urlAfter !== urlBefore)
    return {
      ...base,
      proven: true,
      proof: "navigate",
      detail: `${redactUrl(urlBefore)} -> ${redactUrl(urlAfter)}`,
    };
  // Either direction. Closing a modal is as real an effect as opening one, and
  // an increase-only check reported every close button as dead.
  if (overlaysAfter !== overlaysBefore)
    return { ...base, proven: true, proof: "overlay", detail: `overlays ${overlaysBefore} -> ${overlaysAfter}` };
  // A request only counts once a server has answered it, and answered without
  // an error. Accepting the request alone proved a control pointing at a
  // hanging endpoint for ever, which is the same missing verdict that made an
  // aborted write look like proof. Needing a 2xx or 3xx costs no per-control
  // inventory: it is the same generic rule for every control on every surface.
  if (answered.length > 0)
    return {
      ...base,
      proven: true,
      proof: "network",
      detail: answered.slice(0, 3).join(", ").slice(0, 200),
    };
  if (requests.length > 0 && signatureAfter === signatureBefore)
    return {
      ...base,
      proven: false,
      proof: "none",
      detail:
        "sent a request that nothing answered inside the settle window: " +
        requests.slice(0, 2).map(redactUrl).join(", ").slice(0, 160),
    };
  if (signatureAfter !== signatureBefore)
    return { ...base, proven: true, proof: "dom", detail: "what is on screen changed" };
  // Deliberately no mutation-count fallback. A "more than eight records"
  // threshold used to sit here, and nothing ever measured what an idle page
  // emits in the same window, so it was a number that could pass a control on
  // background churn. The rendered-text signature above is the measured test;
  // the count survives only in the failure detail, where it explains rather
  // than decides.
  return {
    ...base,
    proven: false,
    proof: "none",
    detail: clickError || `no effect (mutations=${mutations})`,
  };
  }
}

/**
 * A control whose OWN name says it destroys data.
 *
 * Matched on the name rather than on the request, because the decision has to
 * be taken before the click. Matched on the control's own accessible name and
 * never on the ambient text lifted from an ancestor: `label` falls back to up
 * to 140 characters of the surrounding panel, so a single "Delete All Chats"
 * heading in Settings > Data Controls made every button in that panel
 * destructive, which both hid two controls the same ledger proves dead and
 * threw away three controls that were genuinely proven.
 *
 * A control with no accessible name at all is not classified as destructive:
 * unnamed and destructive is its own defect, and the gate reports it as an
 * unnamed control rather than quietly declining to test it.
 */
export const DESTRUCTIVE = /\b(delete|archive|clear|reset|remove|unshare|erase|wipe)\b/i;

export function isDestructive(ctl: Control): boolean {
  return DESTRUCTIVE.test(ctl.name);
}

/**
 * The check a destructive control gets instead of a click.
 *
 * It cannot be activated: the account is shared and the data is the demo's.
 * It cannot be activated with its write aborted at the network layer either,
 * which is what this suite used to do, because an aborted request has no
 * server verdict at all, so a button wired to an endpoint that no longer
 * exists came back proven. What is left that is worth asserting is that the
 * control is there, that it is offered to the user rather than sitting inert
 * and disabled with no explanation, and that it carries a name and, if it is
 * a link, a destination. That is recorded as "not-fired", never as proof.
 */
export async function checkWithoutFiring(page: Page, ctl: Control): Promise<Result> {
  const base = {
    key: ctl.key,
    surface: ctl.surface,
    name: ctl.label || ctl.name,
    role: ctl.role || ctl.tag,
  };
  const el = locate(page, ctl);
  const visible = await el.isVisible().catch(() => false);
  if (!visible) {
    return { ...base, proven: false, proof: "none", detail: "destructive control is not on screen" };
  }
  if (ctl.disabled) {
    const reason = ctl.reason.trim();
    return {
      ...base,
      proven: false,
      proof: "not-fired",
      detail: reason ? `destructive and disabled: ${reason.slice(0, 90)}` : "destructive and disabled with no stated reason",
    };
  }
  if (ctl.name.trim() === "") {
    return {
      ...base,
      proven: false,
      proof: "none",
      detail: "destructive control carries no accessible name, so nobody can tell what it destroys",
    };
  }
  if (ctl.tag === "a" && ctl.href.trim() === "") {
    return { ...base, proven: false, proof: "none", detail: "destructive link has no href" };
  }
  return {
    ...base,
    proven: false,
    proof: "not-fired",
    detail:
      "destructive by its own name, so it was checked (present, enabled, named) and deliberately not activated",
  };
}

export function isStateful(ctl: Control): boolean {
  if (ctl.role === "switch" || ctl.role === "checkbox") return true;
  if (ctl.tag === "select" || ctl.tag === "textarea") return true;
  if (ctl.contentEditable) return true;
  if (ctl.tag === "input") return !["file", "submit", "button", "image"].includes(ctl.type);
  return false;
}

/** Flip a stateful control to a value different from its current one. */
export async function flip(page: Page, ctl: Control): Promise<string | null> {
  const el = locate(page, ctl);
  if (ctl.role === "switch" || ctl.role === "checkbox" || ctl.type === "checkbox") {
    await el.click({ timeout: 6000 });
    return null;
  }
  if (ctl.tag === "select") {
    const values = await el.evaluate((node) =>
      [...(node as HTMLSelectElement).options].map((o) => o.value),
    );
    const next = values.find((v) => v !== ctl.state);
    if (next === undefined) return null;
    await el.selectOption(next, { timeout: 6000 });
    return next;
  }
  if (ctl.type === "date") {
    const next = ctl.state === "1990-01-01" ? "1991-02-03" : "1990-01-01";
    await el.fill(next, { timeout: 6000 });
    return next;
  }
  if (ctl.type === "number" || ctl.type === "range") {
    const current = Number(ctl.state ?? "0");
    const next = String(Number.isFinite(current) ? current + 1 : 1);
    await el.fill(next, { timeout: 6000 });
    return next;
  }
  const next = `hive-coverage-${Date.now() % 100000}`;
  await el.fill(next, { timeout: 6000 });
  return next;
}

export async function readState(page: Page, covId: string): Promise<string | null> {
  return page.evaluate((id) => {
    const el = document.querySelector(`[data-cov-id="${id}"]`);
    if (!el) return null;
    const tag = el.tagName.toLowerCase();
    const role = el.getAttribute("role") ?? "";
    if (el.getAttribute("contenteditable") === "true") return (el as HTMLElement).innerText;
    if (role === "switch" || role === "checkbox")
      return el.getAttribute("aria-checked") ?? (el as HTMLElement).innerText.trim();
    if (tag === "select") return (el as HTMLSelectElement).value;
    if (tag === "input") {
      const input = el as HTMLInputElement;
      if (input.type === "checkbox" || input.type === "radio") return String(input.checked);
      return input.value;
    }
    if (tag === "textarea") return (el as HTMLTextAreaElement).value;
    return null;
  }, covId);
}

/**
 * Collapses instance keys to the identity they are instances of.
 *
 * The enumerator appends an ordinal to a duplicate key, so 52 rows of the chat
 * list produce 52 keys that differ only by that ordinal. Counting them as 52
 * controls made the ratio track how many chats the account happens to hold,
 * which is our own test litter as much as anything, rather than tracking the
 * product. One identity is one thing a user can do.
 */
export function identityOf(key: string): string {
  return key.replace(/#\d+$/, "");
}

export function summarise(results: Result[]) {
  const bySurface = new Map<
    string,
    { total: number; proven: number; deferred: number; unproven: string[] }
  >();
  for (const r of results) {
    const s = bySurface.get(r.surface) ?? { total: 0, proven: 0, deferred: 0, unproven: [] };
    s.total += 1;
    if (r.proven) s.proven += 1;
    else if (isDeferred(r)) s.deferred += 1;
    else s.unproven.push(`${r.name || "(unnamed)"} [${r.role}] :: ${r.detail}`);
    bySurface.set(r.surface, s);
  }
  const total = results.length;
  const proven = results.filter((r) => r.proven).length;
  const deferred = results.filter(isDeferred).length;
  // Primary figure. An identity counts as proven only when every instance of
  // it proved, so a control that works on the first chat row and fails on the
  // seventh is unproven, and the gate fails on the failing instance anyway
  // because it fails on any unproven result at all.
  const byIdentity = new Map<string, { instances: number; proven: number; deferred: number }>();
  for (const r of results) {
    const id = identityOf(r.key);
    const cell = byIdentity.get(id) ?? { instances: 0, proven: 0, deferred: 0 };
    cell.instances += 1;
    if (r.proven) cell.proven += 1;
    if (isDeferred(r)) cell.deferred += 1;
    byIdentity.set(id, cell);
  }
  const identityTotal = byIdentity.size;
  const identityProven = [...byIdentity.values()].filter(
    (cell) => cell.proven === cell.instances,
  ).length;
  // An identity every instance of which was deliberately not fired. Counted
  // apart from both proven and failing, so the headline can never absorb it.
  const identityDeferred = [...byIdentity.values()].filter(
    (cell) => cell.deferred === cell.instances,
  ).length;
  return {
    // Primary. Distinct control identities, independent of how many rows of
    // repeated chrome the account happens to render.
    identities: identityTotal,
    identitiesProven: identityProven,
    identitiesDeferred: identityDeferred,
    identityRatio:
      identityTotal === 0 ? 0 : Number((identityProven / identityTotal).toFixed(4)),
    // Secondary, and not comparable between runs: raw instances, whose count
    // moves with account data rather than with the product.
    total,
    proven,
    deferred,
    ratio: total === 0 ? 0 : Number((proven / total).toFixed(4)),
    surfaces: Object.fromEntries(bySurface),
    identityInstances: Object.fromEntries(byIdentity),
  };
}

/**
 * Denominator guard.
 *
 * The ratio is proven over enumerated and both sides are read out of the
 * rendered DOM, so a surface that renders less than it used to shrinks the
 * denominator along with the numerator and the percentage holds or climbs
 * while the app degrades. A floor per surface, committed to the repository,
 * makes that drop a failure instead of an improvement. A floor rather than a
 * comparison against the previous run because a run starts from a clean
 * checkout with no artifact from the last one, so previous-run state can
 * always be missing and the check would become no check at all.
 */
export type Floors = Record<string, number>;

export type Swept = { surface: string; enumerated: number };

/**
 * @param floors committed minimum control count per surface
 * @param swept what this run actually enumerated
 * @param scope surfaces this run was asked to cover. A floor key outside it is
 *   not checked; a floor key inside it that never got swept is a failure,
 *   because iterating the swept list alone means a surface that vanishes takes
 *   its own floor with it and the denominator shrinks in silence.
 */
export function floorFailures(
  floors: Floors,
  swept: Swept[],
  scope: (surface: string) => boolean = () => true,
): string[] {
  const out: string[] = [];
  const sweptIds = new Set(swept.map((entry) => entry.surface));
  for (const surface of Object.keys(floors).sort()) {
    if (sweptIds.has(surface) || !scope(surface)) continue;
    out.push(
      surface +
        ": has a floor of " +
        String(floors[surface]) +
        " in surface-floors.json but this run never swept it, so its controls left the" +
        " denominator entirely. Sweep it, exclude it deliberately in surface-exclusions.json," +
        " or drop its floor with a reason",
    );
  }
  for (const entry of swept) {
    const floor = floors[entry.surface];
    if (floor === undefined) {
      out.push(
        entry.surface +
          ": enumerated " +
          String(entry.enumerated) +
          " controls but has no floor in surface-floors.json; record one, or a later run that renders nothing here reports full coverage of an empty surface",
      );
      continue;
    }
    if (entry.enumerated < floor) {
      out.push(
        entry.surface +
          ": enumerated " +
          String(entry.enumerated) +
          " controls, below its recorded floor of " +
          String(floor) +
          "; the surface renders less than it used to, which shrinks the denominator and inflates coverage. Fix the surface, or lower the floor in this PR with a reason",
      );
    }
  }
  return out;
}

/**
 * A surface deliberately left out of the sweep.
 *
 * Excluding a surface is allowed. Excluding it forever is not. An exclusion
 * that exists because something is broken names the issue that is broken, and
 * the gate fails the moment that issue closes, so the exclusion cannot outlive
 * its reason. A standing decision says permanent instead and names no issue.
 */
export type SurfaceExclusion = {
  id: string;
  reason?: string;
  issue?: number;
  permanent?: boolean;
  owner?: string;
};

export function exclusionFailures(exclusions: SurfaceExclusion[]): string[] {
  const out: string[] = [];
  for (const entry of exclusions) {
    const label = "surface exclusion " + (entry.id || "(no id)");
    if (!entry.id) out.push(label + " has no id");
    if (!(entry.reason ?? "").trim()) out.push(label + " has no reason");
    if (!(entry.owner ?? "").trim()) out.push(label + " has no owner");
    if (entry.issue === undefined && entry.permanent !== true) {
      out.push(
        label +
          " names no blocking issue and is not declared permanent; an exclusion with no end is how a denominator shrinks without anyone deciding it should",
      );
    }
    if (entry.issue !== undefined && entry.permanent === true) {
      out.push(label + " declares itself permanent and also names an issue; it is one or the other");
    }
  }
  return out;
}

// --- Reading the committed data files ---------------------------------------
//
// Hand-written validators rather than a cast on JSON.parse. A cast asserts a
// shape the compiler cannot check and the file cannot be made to honour, so a
// floors file whose numbers were saved as strings, or an exclusions file
// missing its array, would sail past the type system and turn into undefined
// at the point where the gate is deciding whether to fail.

function fields(value: unknown, where: string): Map<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${where}: expected a JSON object`);
  }
  return new Map(Object.entries(value));
}

function items(value: unknown, where: string): unknown[] {
  if (!Array.isArray(value)) throw new Error(`${where}: expected a JSON array`);
  return value;
}

function requiredString(map: Map<string, unknown>, name: string, where: string): string {
  const value = map.get(name);
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error(`${where}: "${name}" must be a non-empty string`);
  }
  return value;
}

function optionalString(map: Map<string, unknown>, name: string, where: string): string | undefined {
  const value = map.get(name);
  if (value === undefined) return undefined;
  if (typeof value !== "string") throw new Error(`${where}: "${name}" must be a string`);
  return value;
}

export function parseFloors(value: unknown): Floors {
  const surfaces = fields(fields(value, "surface-floors.json").get("surfaces"), "surface-floors.json surfaces");
  const out: Floors = {};
  for (const [surface, count] of surfaces) {
    if (typeof count !== "number" || !Number.isInteger(count) || count <= 0) {
      throw new Error(`surface-floors.json: floor for ${surface} must be a positive integer`);
    }
    out[surface] = count;
  }
  return out;
}

export function parseExclusions(value: unknown): SurfaceExclusion[] {
  const raw = items(fields(value, "surface-exclusions.json").get("surfaces"), "surface-exclusions.json surfaces");
  return raw.map((entry, index) => {
    const where = `surface-exclusions.json entry ${index}`;
    const map = fields(entry, where);
    const issue = map.get("issue");
    if (
      issue !== undefined &&
      (typeof issue !== "number" || !Number.isInteger(issue) || issue <= 0)
    ) {
      // A zero, a float or a string here would be looked up as an issue number
      // that cannot exist, GitHub would answer 404, and the exclusion would sit
      // there permanently with nothing able to expire it.
      throw new Error(`${where}: "issue" must be a positive integer issue number`);
    }
    const permanent = map.get("permanent");
    if (permanent !== undefined && typeof permanent !== "boolean") {
      throw new Error(`${where}: "permanent" must be a boolean`);
    }
    return {
      id: requiredString(map, "id", where),
      reason: optionalString(map, "reason", where),
      owner: optionalString(map, "owner", where),
      issue,
      permanent,
    };
  });
}

export type InertEntry = { key: string; justification: string };

export function parseRegistry(value: unknown): InertEntry[] {
  const raw = items(fields(value, "inert-registry.json").get("allowed"), "inert-registry.json allowed");
  return raw.map((entry, index) => {
    const where = `inert-registry.json entry ${index}`;
    const map = fields(entry, where);
    return {
      key: optionalString(map, "key", where) ?? "",
      justification: optionalString(map, "justification", where) ?? "",
    };
  });
}

export type RemovedSurfaces = {
  forbiddenControls: Array<{ surface: string; label: string; issue: string }>;
  forbiddenRoutes: Array<{ path: string; issue: string }>;
};

export function parseRemoved(value: unknown): RemovedSurfaces {
  const top = fields(value, "removed-surfaces.json");
  const controls = items(top.get("forbiddenControls"), "removed-surfaces.json forbiddenControls");
  const routes = items(top.get("forbiddenRoutes"), "removed-surfaces.json forbiddenRoutes");
  return {
    forbiddenControls: controls.map((entry, index) => {
      const where = `removed-surfaces.json forbiddenControls entry ${index}`;
      const map = fields(entry, where);
      return {
        surface: requiredString(map, "surface", where),
        label: requiredString(map, "label", where),
        issue: requiredString(map, "issue", where),
      };
    }),
    forbiddenRoutes: routes.map((entry, index) => {
      const where = `removed-surfaces.json forbiddenRoutes entry ${index}`;
      const map = fields(entry, where);
      return {
        path: requiredString(map, "path", where),
        issue: requiredString(map, "issue", where),
      };
    }),
  };
}

export function expiredExclusions(
  exclusions: SurfaceExclusion[],
  closed: ReadonlySet<number>,
): string[] {
  return exclusions
    .filter((entry) => entry.issue !== undefined && closed.has(entry.issue))
    .map(
      (entry) =>
        "surface " +
        entry.id +
        " is excluded because issue " +
        String(entry.issue) +
        " blocks it, but that issue is closed; delete the exclusion and let the gate measure the surface, or cite the issue that still blocks it",
    );
}
