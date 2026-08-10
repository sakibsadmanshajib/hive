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
      return `${text.length}:${text.replace(/\s+/g, " ").slice(0, 400)}`;
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
  page.off("request", onRequest);
  page.off("download", onDownload);
  page.off("filechooser", onFileChooser);
  page.context().off("page", onPopup);

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
