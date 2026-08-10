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
  | "disabled-with-reason";

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
  const out: unknown[] = [];
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
      reason:
        el.getAttribute("title") ??
        el.getAttribute("aria-describedby") ??
        el.getAttribute("data-disabled-reason") ??
        "",
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
  return (await page.evaluate(ENUMERATE, {
    selector: SELECTOR,
    surface,
    scope: scope ?? null,
  })) as Control[];
}

export function locate(page: Page, ctl: Control): Locator {
  return page.locator(`[data-cov-id="${ctl.covId}"]`);
}

async function startWatch(page: Page): Promise<() => Promise<{ mutations: number }>> {
  await page.evaluate(() => {
    const w = window as unknown as { __covMut?: number; __covObs?: MutationObserver };
    w.__covObs?.disconnect();
    w.__covMut = 0;
    const obs = new MutationObserver((records) => {
      w.__covMut = (w.__covMut ?? 0) + records.length;
    });
    obs.observe(document.body, {
      subtree: true,
      childList: true,
      attributes: true,
      characterData: true,
    });
    w.__covObs = obs;
  });
  return async () => {
    const mutations = await page.evaluate(() => {
      const w = window as unknown as { __covMut?: number; __covObs?: MutationObserver };
      const n = w.__covMut ?? 0;
      w.__covObs?.disconnect();
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
export async function proveByClick(
  page: Page,
  ctl: Control,
  opts: { settleMs?: number; budgetMs?: number } = {},
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
  opts: { settleMs?: number } = {},
): Promise<Result> {
  const base = { key: ctl.key, surface: ctl.surface, name: ctl.label || ctl.name, role: ctl.role || ctl.tag };
  if (ctl.disabled) {
    const reason = ctl.reason.trim();
    return reason
      ? { ...base, proven: true, proof: "disabled-with-reason", detail: reason.slice(0, 120) }
      : { ...base, proven: false, proof: "none", detail: "disabled with no reason attribute" };
  }

  const requests: string[] = [];
  const onRequest = (r: { url: () => string }) => {
    const u = r.url();
    if (isMeaningfulRequest(u)) requests.push(u);
  };
  page.on("request", onRequest);
  // Statuses, not just URLs. This file never registered a response listener at
  // all, so a click that answered 500 and rendered an error toast came back
  // proven twice over, once on the request and once on the toast.
  const failed: string[] = [];
  const onResponse = (r: {
    status: () => number;
    url: () => string;
    request: () => { method: () => string };
  }) => {
    const u = r.url();
    if (!isMeaningfulRequest(u)) return;
    if (r.status() >= 400) failed.push(String(r.status()) + " " + r.request().method() + " " + u);
  };
  page.on("response", onResponse);
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
  page.off("request", onRequest);
  page.off("response", onResponse);
  page.off("download", onDownload);
  page.off("filechooser", onFileChooser);
  page.context().off("page", onPopup);

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

  if (download) return { ...base, proven: true, proof: "download", detail: "download started" };
  if (filechooser) return { ...base, proven: true, proof: "filechooser", detail: "file chooser opened" };
  if (popup) return { ...base, proven: true, proof: "navigate", detail: "opened a new tab" };
  if (urlAfter !== urlBefore)
    return { ...base, proven: true, proof: "navigate", detail: `${urlBefore} -> ${urlAfter}` };
  // Either direction. Closing a modal is as real an effect as opening one, and
  // an increase-only check reported every close button as dead.
  if (overlaysAfter !== overlaysBefore)
    return { ...base, proven: true, proof: "overlay", detail: `overlays ${overlaysBefore} -> ${overlaysAfter}` };
  if (requests.length > 0)
    return { ...base, proven: true, proof: "network", detail: requests.slice(0, 3).join(", ").slice(0, 200) };
  if (signatureAfter !== signatureBefore)
    return { ...base, proven: true, proof: "dom", detail: "what is on screen changed" };
  // Last resort. A settings tab switch emits 6 or 7 mutation records, so any
  // threshold that filtered idle churn also swallowed it; the signature above
  // is the real test and this only catches changes too subtle to render.
  if (mutations > 8)
    return { ...base, proven: true, proof: "dom", detail: `${mutations} mutations` };
  return {
    ...base,
    proven: false,
    proof: "none",
    detail: clickError || `no effect (mutations=${mutations})`,
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

export function summarise(results: Result[]) {
  const bySurface = new Map<string, { total: number; proven: number; unproven: string[] }>();
  for (const r of results) {
    const s = bySurface.get(r.surface) ?? { total: 0, proven: 0, unproven: [] };
    s.total += 1;
    if (r.proven) s.proven += 1;
    else s.unproven.push(`${r.name || "(unnamed)"} [${r.role}] :: ${r.detail}`);
    bySurface.set(r.surface, s);
  }
  const total = results.length;
  const proven = results.filter((r) => r.proven).length;
  return {
    total,
    proven,
    ratio: total === 0 ? 0 : Number((proven / total).toFixed(4)),
    surfaces: Object.fromEntries(bySurface),
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

export function floorFailures(
  floors: Floors,
  swept: Array<{ surface: string; enumerated: number }>,
): string[] {
  const out: string[] = [];
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
