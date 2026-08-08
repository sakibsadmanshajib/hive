import { test, expect, type Locator } from "@playwright/test";

test.use({ storageState: "e2e/phase-19/owui/.auth/owui-user.json" });

// Mirrors owui.setup.ts, which resolves the same way. Needed literally here
// because the last test builds its own context and so gets no baseURL from the
// project config.
const OWUI_URL = process.env.OWUI_URL ?? "http://localhost:3002";

// The launcher is injected by deploy/docker/owui-static/loader.js and styled by
// deploy/docker/owui-static/custom.css. Both are hand-written overrides against
// a pinned, unforked Open WebUI image, so nothing else in the build fails when
// they rot. These are the three properties that were verified by hand and would
// otherwise silently regress.
const LAUNCHER = "#hive-agent-launcher";

// Open WebUI's own model selector, matched by the id upstream's code depends on
// itself: the pinned bundle's model-selector keyboard shortcut is a literal
// `document.getElementById("model-selector-0-button").click()`, and the id is
// assigned unconditionally alongside aria-expanded, so it is present in every
// state the button has.
//
// Its aria-label is NOT. Upstream renders "Select a model" only while nothing is
// selected and "Selected model: <id>" once something is (i18n keys
// `Select a model` and `Selected model: {{modelName}}`). Matching the first of
// those, as this file did originally, was never a correct handle on the
// selector: it silently stops matching the moment the deployment actually serves
// a model and Open WebUI auto-selects one. That is why the clearance check below
// passed for as long as the e2e catalogue came back empty and then failed at the
// very first width once #735 and #737 made models resolve (nightly run
// 31042840516, "element(s) not found" at 1440px, before any geometry was read).
//
// 03-chat-model-switch.spec.ts and 06-tenant-model-visibility.spec.ts hit the
// same rot earlier (run 28688154897) and settled on a role query matching both
// label forms. That is fine where the button is only being clicked; this file
// goes further because it measures the element, and because the id also survives
// a locale change, which an English-label match does not.
const MODEL_SELECTOR = 'button[id^="model-selector-"][id$="-button"]';

test("launcher matches Open WebUI's own header icon buttons", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");

  const launcher = page.locator(LAUNCHER);
  await expect(launcher).toBeVisible();

  // Read the values off a real OWUI header button rather than hardcoding them,
  // so a future upstream restyle shows up here as a mismatch instead of leaving
  // the launcher quietly out of step.
  const native = page.locator('button[aria-label="Controls"]').first();
  await expect(native).toBeVisible();

  const shape = (locator: Locator) =>
    locator.evaluate((el: Element) => {
      const cs = getComputedStyle(el);
      return {
        padding: cs.padding,
        borderRadius: cs.borderRadius,
        borderWidth: cs.borderWidth,
        backgroundColor: cs.backgroundColor,
        boxShadow: cs.boxShadow,
        color: cs.color,
      };
    });

  expect(await shape(launcher)).toEqual(await shape(native));

  // Hover state too: OWUI tints the background rather than adding a border.
  // Both sides animate over 150ms, so the value has to be read once it settles
  // -- reading straight after hover() catches a mid-transition colour and makes
  // this compare two arbitrary points on two tweens.
  const settledHoverBackground = async (locator: Locator) => {
    await locator.hover();
    let previous = "";
    await expect
      .poll(async () => {
        const current = await locator.evaluate(
          (el: Element) => getComputedStyle(el).backgroundColor,
        );
        const settled = current === previous;
        previous = current;
        return settled;
      })
      .toBe(true);
    return previous;
  };

  const nativeHover = await settledHoverBackground(native);
  const launcherHover = await settledHoverBackground(launcher);
  expect(launcherHover).toBe(nativeHover);
  // Guards against the whole comparison passing because neither element tinted.
  expect(nativeHover).not.toBe("rgba(0, 0, 0, 0)");
});

test("launcher is present down to 768px and never overlaps the header", async ({
  page,
}) => {
  // 768px is the measured floor, set by OWUI's unclamped model selector rather
  // than by the launcher; custom.css carries the measurement table. Below it the
  // launcher is deliberately absent and the per-message Action is the entry
  // point, so 375px asserts absence, not presence.
  // The clearance the gate is built on is consumed by the SELECTED model's id,
  // so measuring against the short "Select a model" placeholder would prove
  // nothing. Use the longest id this deployment actually serves, falling back to
  // the longest realistic OpenRouter-style id when the catalogue is empty (which
  // it is wherever the shim has no tenant mapping for the test account).
  const served: string[] = await page.evaluate(async () => {
    try {
      const res = await fetch("/api/models", {
        headers: { Authorization: `Bearer ${localStorage.getItem("token")}` },
      });
      const body = await res.json();
      return (body?.data ?? []).map((m: { id?: string; name?: string }) =>
        String(m.id ?? m.name ?? ""),
      );
    } catch {
      return [];
    }
  });
  const longestModelId = [
    ...served,
    "meta-llama/llama-4-maverick-17b-128e-instruct",
  ].sort((a, b) => b.length - a.length)[0];

  for (const width of [1440, 1024, 900, 768]) {
    await page.setViewportSize({ width, height: 820 });
    await page.goto("/");
    const launcher = page.locator(LAUNCHER);
    await expect(launcher, `launcher at ${width}px`).toBeVisible();

    // loader.js injects before OWUI hydrates, so the launcher exists while the
    // header is still empty. Wait for OWUI's own selector before measuring
    // against it, otherwise the clearance check runs on a half-built header.
    await expect(
      page.locator(MODEL_SELECTOR).first(),
      `OWUI model selector at ${width}px`,
    ).toBeVisible();

    // Push OWUI's model selector out to its worst realistic width first. Its
    // computed max-width is `none`, so the left-hand header group grows with
    // this string and is what the launcher has to clear.
    const selectorGrew = await page.evaluate(
      ({ selector, label }) => {
        const target = document.querySelector(selector);
        if (!target) return null;
        const before = target.getBoundingClientRect().width;
        const walk = document.createTreeWalker(target, NodeFilter.SHOW_TEXT);
        const nodes: Text[] = [];
        while (walk.nextNode()) {
          const node = walk.currentNode;
          // Whitespace-only nodes are Svelte formatting artefacts. Writing the
          // label into one of those would leave the button's real text in place
          // and the group at its original width, which the widening assertion
          // below would then report as a defect in the launcher.
          if (node instanceof Text && node.nodeValue?.trim()) nodes.push(node);
        }
        if (!nodes.length) return null;
        nodes[0].nodeValue = label;
        return { before, after: target.getBoundingClientRect().width };
      },
      { selector: MODEL_SELECTOR, label: longestModelId },
    );

    // If this stops finding the selector, the overlap check below is measuring
    // a header that never widened, which is exactly the false pass to avoid.
    expect(selectorGrew, `model selector not found at ${width}px`).not.toBeNull();
    expect(
      selectorGrew?.after ?? 0,
      `model selector did not widen at ${width}px`,
    ).toBeGreaterThan(selectorGrew?.before ?? 0);

    // Nothing else in the header band may intersect it: this element sits above
    // OWUI's, so an overlap would swallow clicks meant for OWUI's own controls.
    const overlaps = await page.evaluate((sel) => {
      const el = document.querySelector(sel);
      if (!el) return ["launcher missing"];
      const box = el.getBoundingClientRect();
      return Array.from(document.querySelectorAll("button, a, input, textarea"))
        .filter((other) => other !== el && !el.contains(other))
        .filter((other) => {
          const b = other.getBoundingClientRect();
          if (b.top > 60 || b.bottom < 0 || b.width < 8 || b.height < 8) {
            return false;
          }
          return (
            b.left < box.right &&
            b.right > box.left &&
            b.top < box.bottom &&
            b.bottom > box.top
          );
        })
        .map(
          (other) =>
            other.getAttribute("aria-label") ||
            (other.textContent ?? "").trim().slice(0, 24) ||
            other.tagName,
        );
    }, LAUNCHER);
    expect(
      overlaps,
      `overlaps at ${width}px with model id "${longestModelId}"`,
    ).toEqual([]);

    // Icon-only below 1024px, matching the shape of OWUI's own header controls.
    const labelShown = await page
      .locator("#hive-agent-launcher-label")
      .evaluate((el: Element) => getComputedStyle(el).display !== "none");
    expect(labelShown, `label at ${width}px`).toBe(width >= 1024);
  }

  await page.setViewportSize({ width: 375, height: 820 });
  await page.goto("/");
  await expect(page.locator(LAUNCHER)).toBeHidden();
});

test("launcher survives client-side navigation without duplicating", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/");
  await expect(page.locator(LAUNCHER)).toHaveCount(1);

  let documentLoads = 0;
  page.on("load", () => {
    documentLoads += 1;
  });

  // loader.js runs once per document load, so any duplication or disappearance
  // has to be caused by SvelteKit re-rendering, which is exactly what these
  // hops exercise. The zero-load assertion is what makes them meaningful.
  //
  // Every step below asserts the app actually moved, not just that the click
  // returned. A missing selector or a no-op click would otherwise leave this
  // test green while exercising no re-render at all.
  await page.locator('button[aria-label="Open Sidebar"]').first().click();
  // The drawer's own nav links becoming visible is the proof it opened.
  // Workspace, not Notes: #772 removes Notes from this deployment entirely
  // (ENABLE_NOTES=false plus the persisted-config reconcile), and Caddy 404s
  // the route, so a Notes link here would be a defect rather than a fixture.
  await expect(
    page.locator('a[href="/workspace"]').first(),
    "sidebar drawer did not open",
  ).toBeVisible();
  // Ordering matters: the visible Workspace link above is what makes this
  // absence meaningful. Without it a closed drawer would satisfy a zero count
  // and this would pin nothing. Notes coming back fails here, which is the
  // only place in the suite that would notice.
  await expect(
    page.locator('a[href="/notes"]'),
    "Notes is removed on this deployment (#772) but the sidebar still links to it",
  ).toHaveCount(0);
  await expect(page.locator(LAUNCHER)).toHaveCount(1);

  for (const href of ["/workspace", "/"]) {
    const link = page.locator(`a[href="${href}"]`).first();
    await expect(link, `no link to ${href} rendered`).toBeVisible();
    await link.click();
    // Exact match, or one segment deeper: OWUI redirects /workspace to its
    // first visible tab, which is /workspace/knowledge once #772 removes the
    // others. Deliberately not a bare startsWith, which "/" satisfies always
    // and would make this hop assert nothing.
    const arrived = (pathname: string) =>
      pathname === href || (href !== "/" && pathname.startsWith(`${href}/`));
    await page.waitForURL((url) => arrived(url.pathname), { timeout: 15_000 });
    await expect(page.locator(LAUNCHER), `after nav to ${href}`).toHaveCount(1);
  }

  expect(documentLoads, "hops must be client-side").toBe(0);
});

test("launcher appears after a sign-in that happens long after page load", async ({
  browser,
}) => {
  // Regression guard for the defect this file's sibling change fixed: OWUI signs
  // in without a document load, so loader.js never runs again and its poll is
  // the only thing that can notice. With the original ~6s ceiling, anyone who
  // sat on the sign-in screen for longer than that lost the launcher for the
  // whole session.
  const email = process.env.OWUI_E2E_EMAIL;
  const password = process.env.OWUI_E2E_PASSWORD;
  if (!email || !password) {
    test.skip(true, "needs OWUI_E2E_EMAIL/OWUI_E2E_PASSWORD");
    return;
  }

  test.setTimeout(120_000);
  // A fresh context on purpose: this one must start signed out, so it declares
  // an empty storageState explicitly rather than relying on what
  // browser.newContext does or does not inherit from this file's test.use, and
  // supplies its own baseURL for the same reason.
  const context = await browser.newContext({
    baseURL: OWUI_URL,
    storageState: { cookies: [], origins: [] },
    viewport: { width: 1440, height: 900 },
  });
  const page = await context.newPage();
  let documentLoads = 0;
  page.on("load", () => {
    documentLoads += 1;
  });

  await page.goto("/");
  await expect(page.locator(LAUNCHER)).toHaveCount(0);
  // Well past the fast phase of the poll.
  await page.waitForTimeout(12_000);

  const loadsBeforeSignIn = documentLoads;
  await page.fill('input[type="email"]', email);
  await page.fill('input[type="password"]', password);
  await page.click('button[type="submit"]');

  await expect(page.locator(LAUNCHER)).toBeVisible({ timeout: 30_000 });
  expect(
    documentLoads - loadsBeforeSignIn,
    "sign-in is client-side, which is why the poll has to outlive it",
  ).toBe(0);

  await context.close();
});
