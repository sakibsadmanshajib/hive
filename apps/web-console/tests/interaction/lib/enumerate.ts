// Browser-side enumeration of every interactive control in a rendered page.
//
// This is the load-bearing half of the interaction coverage gate. It runs
// inside the page via `page.evaluate`, so every function below must be
// self-contained: Playwright serializes the function source and evaluates it
// in a context where this module's imports do not exist.
//
// The enumeration is deliberately derived from the rendered DOM rather than
// from a hand maintained list of selectors. A hand maintained list only ever
// covers the controls somebody remembered to add to it, which is exactly how
// a sidebar button carrying `aria-haspopup="menu"` and no click handler at
// all survived a full suite of tests that each asserted the button rendered.

export type ControlKind =
  | "link"
  | "toggle"
  | "textfield"
  | "select"
  | "submit"
  | "button"
  | "tab"
  | "menuitem"
  | "other";

export interface RawControl {
  /** Index of the `data-ic-idx` attribute stamped on the element. */
  idx: number;
  tag: string;
  role: string;
  type: string;
  /** Accessible-ish name, used for the stable control key. */
  name: string;
  href: string;
  testid: string;
  id: string;
  fieldName: string;
  kind: ControlKind;
  /** Why the element was enumerated, for auditability of the denominator. */
  matchedBy: string;
  disabled: boolean;
  /** Text that explains a disabled state, if the markup supplies one. */
  disabledReason: string;
  ariaChecked: string;
  ariaExpanded: string;
  ariaHasPopup: string;
  /** True when the element lives inside a `<form>`, whatever its action. */
  inForm: boolean;
  /** The enclosing form's action, empty for a client submitted form. */
  formAction: string;
  formMethod: string;
  ordinal: number;
  /** Depth of the reveal chain that exposed this control (0 = visible on load). */
  depth: number;
}

export interface EnumerationResult {
  controls: RawControl[];
  /** Elements skipped, with the reason, so the exclusions are reviewable. */
  skipped: Array<{ tag: string; name: string; reason: string }>;
}

/**
 * Stamps `data-ic-idx` on every enumerated element and returns the inventory.
 *
 * Re-running it is safe and idempotent for a given DOM: the ordering is
 * document order, so an index remains valid until the DOM itself changes.
 */
export function enumerateInPage(): EnumerationResult {
  const SELECTOR = [
    "a",
    "button",
    "input",
    "select",
    "textarea",
    "summary",
    "[onclick]",
    "[contenteditable]:not([contenteditable='false'])",
    "[tabindex]",
    "[role=button]",
    "[role=link]",
    "[role=tab]",
    "[role=menuitem]",
    "[role=menuitemcheckbox]",
    "[role=menuitemradio]",
    "[role=switch]",
    "[role=checkbox]",
    "[role=radio]",
    "[role=option]",
    "[role=combobox]",
    "[role=slider]",
    "[role=spinbutton]",
    "[role=searchbox]",
    "[role=textbox]",
  ].join(",");

  const skipped: Array<{ tag: string; name: string; reason: string }> = [];

  function textOf(el: Element): string {
    // `innerText` reflects what a user actually sees, but it is unimplemented
    // in jsdom, where this enumerator is unit tested. Fall back rather than
    // silently returning "" for every element.
    const rendered =
      el instanceof HTMLElement && typeof el.innerText === "string"
        ? el.innerText
        : el.textContent;
    return (rendered ?? "").trim().replace(/\s+/g, " ").slice(0, 80);
  }

  function nameOf(el: Element): string {
    const labelled = el.getAttribute("aria-labelledby");
    if (labelled) {
      const parts = labelled
        .split(/\s+/)
        .map((id) => document.getElementById(id))
        .filter((n): n is HTMLElement => n !== null)
        .map((n) => textOf(n));
      if (parts.length > 0) {
        return parts.join(" ").slice(0, 80);
      }
    }
    const candidates = [
      el.getAttribute("aria-label"),
      textOf(el),
      el.getAttribute("placeholder"),
      el.getAttribute("title"),
      el.getAttribute("alt"),
      el.getAttribute("name"),
      el.getAttribute("value"),
    ];
    for (const candidate of candidates) {
      if (candidate && candidate.trim() !== "") {
        return candidate.trim().replace(/\s+/g, " ").slice(0, 80);
      }
    }
    const img = el.querySelector("img[alt], svg[aria-label], svg title");
    if (img) {
      const alt =
        img.getAttribute("alt") ?? img.getAttribute("aria-label") ?? textOf(img);
      if (alt) {
        return alt.trim().slice(0, 80);
      }
    }
    return "";
  }

  function isRendered(el: Element): boolean {
    const style = getComputedStyle(el);
    if (style.display === "none" || style.visibility === "hidden") {
      return false;
    }
    // An unset computed opacity (jsdom returns "") is fully opaque, not
    // invisible: Number("") is 0, which would silently empty the inventory.
    const opacity = style.opacity === "" ? 1 : Number(style.opacity);
    if (Number.isFinite(opacity) && opacity === 0) {
      return false;
    }
    const rect = el.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) {
      return false;
    }
    if (el.closest("[aria-hidden='true']") !== null) {
      return false;
    }
    if (el.closest("[hidden]") !== null) {
      return false;
    }
    return true;
  }

  function kindOf(el: Element, role: string, type: string): ControlKind {
    const tag = el.tagName.toLowerCase();
    if (role === "switch" || role === "checkbox" || role === "menuitemcheckbox") {
      return "toggle";
    }
    if (tag === "input" && (type === "checkbox" || type === "radio")) {
      return "toggle";
    }
    if (tag === "input" && (type === "submit" || type === "button")) {
      return type === "submit" ? "submit" : "button";
    }
    if (tag === "input" || tag === "textarea" || role === "textbox" || role === "searchbox") {
      return "textfield";
    }
    if (tag === "select" || role === "combobox" || role === "listbox") {
      return "select";
    }
    if (tag === "a" || role === "link") {
      return "link";
    }
    if (role === "tab") {
      return "tab";
    }
    if (role === "menuitem" || role === "menuitemradio" || role === "option") {
      return "menuitem";
    }
    if (tag === "button") {
      return el.getAttribute("type") === "submit" ? "submit" : "button";
    }
    if (role === "button") {
      return "button";
    }
    return "other";
  }

  function disabledReasonOf(el: Element): string {
    const described = el.getAttribute("aria-describedby");
    if (described) {
      const parts = described
        .split(/\s+/)
        .map((id) => document.getElementById(id))
        .filter((n): n is HTMLElement => n !== null)
        .map((n) => textOf(n))
        .filter((t) => t !== "");
      if (parts.length > 0) {
        return parts.join(" ").slice(0, 160);
      }
    }
    const title = el.getAttribute("title");
    if (title && title.trim() !== "") {
      return title.trim().slice(0, 160);
    }
    // A sibling or ancestor hint is the common pattern for "you cannot do
    // this because ...". Look one container up, but only at short text so a
    // whole page body never counts as a reason.
    const parent = el.parentElement;
    if (parent) {
      const hint = parent.querySelector("[data-disabled-reason], .disabled-reason");
      if (hint) {
        return textOf(hint).slice(0, 160);
      }
    }
    return "";
  }

  const matched = new Map<Element, string>();
  for (const el of Array.from(document.querySelectorAll(SELECTOR))) {
    if (el.tagName === "OPTION") {
      continue;
    }
    if (el.tagName === "INPUT" && el.getAttribute("type") === "hidden") {
      continue;
    }
    if (el.getAttribute("tabindex") === "-1" && !el.hasAttribute("role")) {
      // Programmatic focus target, not a user affordance, unless it also
      // carries an interactive role (in which case another branch matched).
      const tag = el.tagName.toLowerCase();
      if (!["a", "button", "input", "select", "textarea", "summary"].includes(tag)) {
        skipped.push({ tag, name: nameOf(el), reason: "tabindex=-1, no role" });
        continue;
      }
    }
    matched.set(el, "selector");
  }

  // Pointer-cursor affordances that no selector caught. Only the outermost
  // such element counts: a `cursor: pointer` button propagates the computed
  // cursor to every span inside it, and counting those would inflate the
  // denominator with things a user perceives as one control.
  for (const el of Array.from(document.querySelectorAll("*"))) {
    if (matched.has(el)) {
      continue;
    }
    if (getComputedStyle(el).cursor !== "pointer") {
      continue;
    }
    const parent = el.parentElement;
    if (parent && getComputedStyle(parent).cursor === "pointer") {
      continue;
    }
    if (el.closest("a, button, [role=button], [role=tab], [role=menuitem], label") !== null) {
      continue;
    }
    matched.set(el, "cursor:pointer");
  }

  // A <label> is a proxy for the control it labels. Counting both doubles the
  // denominator without adding any safety, so drop labels whose target is
  // already enumerated, and keep orphan labels (which are a defect worth
  // surfacing: a click target that activates nothing).
  for (const el of Array.from(matched.keys())) {
    if (el.tagName !== "LABEL") {
      continue;
    }
    const forId = el.getAttribute("for");
    const target = forId
      ? document.getElementById(forId)
      : el.querySelector("input, select, textarea");
    if (target && matched.has(target)) {
      matched.delete(el);
      skipped.push({ tag: "label", name: nameOf(el), reason: "proxy for enumerated control" });
    }
  }

  const controls: RawControl[] = [];
  const seenKeys = new Map<string, number>();
  let idx = 0;

  for (const el of Array.from(document.querySelectorAll("*"))) {
    const matchedBy = matched.get(el);
    if (matchedBy === undefined) {
      continue;
    }
    if (!isRendered(el)) {
      skipped.push({ tag: el.tagName.toLowerCase(), name: nameOf(el), reason: "not rendered" });
      continue;
    }
    const role = el.getAttribute("role") ?? "";
    const type = el.getAttribute("type") ?? "";
    const name = nameOf(el);
    const form = el.closest("form");
    const disabled =
      el.hasAttribute("disabled") || el.getAttribute("aria-disabled") === "true";
    const key = `${el.tagName.toLowerCase()}|${role}|${name}|${el.getAttribute("href") ?? ""}`;
    const ordinal = seenKeys.get(key) ?? 0;
    seenKeys.set(key, ordinal + 1);

    el.setAttribute("data-ic-idx", String(idx));
    controls.push({
      idx,
      tag: el.tagName.toLowerCase(),
      role,
      type,
      name,
      href: el.getAttribute("href") ?? "",
      testid: el.getAttribute("data-testid") ?? "",
      id: el.getAttribute("id") ?? "",
      fieldName: el.getAttribute("name") ?? "",
      kind: kindOf(el, role, type),
      matchedBy,
      disabled,
      disabledReason: disabled ? disabledReasonOf(el) : "",
      ariaChecked: el.getAttribute("aria-checked") ?? "",
      ariaExpanded: el.getAttribute("aria-expanded") ?? "",
      ariaHasPopup: el.getAttribute("aria-haspopup") ?? "",
      inForm: form !== null,
      formAction: form ? form.getAttribute("action") ?? "" : "",
      formMethod: form ? (form.getAttribute("method") ?? "get").toLowerCase() : "",
      ordinal,
      depth: 0,
    });
    idx += 1;
  }

  return { controls, skipped };
}

/**
 * A compact signature of the page's observable state.
 *
 * Used to decide whether activating a control mutated the DOM. It excludes
 * the harness's own `data-ic-idx` stamps and everything that churns on its
 * own (chart transitions, relative timestamps) is absorbed by comparing two
 * consecutive baselines before drawing any conclusion.
 */
export function signatureInPage(): string {
  const body = document.body;
  const raw =
    typeof body.innerText === "string" ? body.innerText : (body.textContent ?? "");
  const text = raw.replace(/\s+/g, " ").trim();
  let hash = 0;
  for (let i = 0; i < text.length; i += 1) {
    hash = (hash * 31 + text.charCodeAt(i)) | 0;
  }
  const states: string[] = [];
  // `[disabled]` and `[aria-disabled]` are in here for the commonest effect a
  // text field has: typing into it enables the Save button beside it. Without
  // them that transition changes no text, no element count and no attribute
  // this signature reads, so the edit looked like it did nothing at all and
  // the field could only be proven from its markup.
  const stateful = document.querySelectorAll(
    "[aria-expanded],[aria-checked],[aria-selected],[aria-pressed],[aria-disabled],[disabled],[open],[role=dialog],[role=menu],[role=alert],input,select,textarea",
  );
  for (const el of Array.from(stateful)) {
    const value =
      el instanceof HTMLInputElement ||
      el instanceof HTMLSelectElement ||
      el instanceof HTMLTextAreaElement
        ? el.value
        : "";
    states.push(
      [
        el.tagName,
        el.getAttribute("role") ?? "",
        el.getAttribute("aria-expanded") ?? "",
        el.getAttribute("aria-checked") ?? "",
        el.getAttribute("aria-selected") ?? "",
        el.getAttribute("aria-pressed") ?? "",
        el.hasAttribute("open") ? "open" : "",
        el.hasAttribute("disabled") || el.getAttribute("aria-disabled") === "true"
          ? "disabled"
          : "",
        value,
      ].join("."),
    );
  }
  return [
    document.querySelectorAll("*").length,
    text.length,
    hash,
    states.join("~"),
  ].join("|");
}
