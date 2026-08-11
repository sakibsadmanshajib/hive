// Surface openers. These are navigation recipes only: how to get the app into
// the state where a group of controls is on screen. They deliberately contain
// no list of controls, because what is on screen is read from the DOM by
// lib.ts, so a control added to any of these surfaces later is picked up
// without editing this file.
import type { Page } from "@playwright/test";

import { enumerate, redactUrl, type Control } from "./lib";

export type Surface = {
  id: string;
  /** Puts the app into the state where this surface's controls are rendered. */
  open: (page: Page) => Promise<void>;
  /**
   * Enumerate only what this surface adds on top of the page underneath it, so
   * the sidebar's buttons are not re-counted inside every dropdown.
   */
  delta?: boolean;
  /**
   * True for the settings tab that owns the modal's own chrome, whose delta is
   * taken against the chat page rather than against the open modal.
   */
  chrome?: boolean;
  /** Settings persist; a composer or search box does not. */
  persists?: boolean;
};

const SETTLE = 1500;

export async function gotoHome(page: Page): Promise<void> {
  // Always dismiss whatever is open first. A modal left over from the previous
  // surface swallows the clicks that follow, and the resulting timeout would be
  // recorded as a dead control rather than as the harness's own mess.
  await page.keyboard.press("Escape").catch(() => {});
  if (new URL(page.url()).pathname !== "/" || (await isOverlayOpen(page))) {
    await page.goto("/", { waitUntil: "domcontentloaded" });
  }
  // Three attempts, because the demo box has gone unreachable for a few minutes
  // at a time mid-run (#815) and recovered on its own. Failing here on the
  // first miss turns an outage into a page of dead controls.
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      await homeReady(page).waitFor({ timeout: 45_000 });
      await page.waitForTimeout(SETTLE);
      return;
    } catch (err) {
      if (attempt === 2) {
        // Say where it actually was. "waitFor timed out" on its own cannot tell
        // a signed-out session (#782) from an unreachable box (#815) from a
        // control that navigated somewhere unexpected.
        const names = await page
          .$$eval("button, a[href]", (els) =>
            els
              .map((e) => (e.getAttribute("aria-label") || e.textContent || "").trim().slice(0, 30))
              .filter(Boolean)
              .slice(0, 12),
          )
          .catch(() => []);
        // Redacted, because this message is written to the ledger and to CI
        // logs. If the app bounced the run through its OAuth callback, the raw
        // URL here would publish a live `code` and `state`.
        throw new Error(
          `could not get back to the chat home. url=${redactUrl(page.url())} visible=${JSON.stringify(names)}`,
        );
      }
      await page.waitForTimeout(20_000);
      await page.goto("/", { waitUntil: "domcontentloaded" }).catch(() => {});
    }
  }
}

/**
 * "New Chat" is a button while the sidebar is collapsed and a link once it is
 * open, so a role-specific wait passes before the first click and then times
 * out for the rest of the run. Match either.
 */
export function homeReady(page: Page) {
  return page
    .getByRole("button", { name: /new chat/i })
    .or(page.getByRole("link", { name: /new chat/i }))
    .first();
}

async function isOverlayOpen(page: Page): Promise<boolean> {
  return page.evaluate(
    () => document.querySelectorAll('[role="dialog"], [role="menu"], [role="listbox"]').length > 0,
  );
}

/**
 * The sidebar is the one piece of chrome that rewrites the whole control set
 * when it moves: collapsed it offers "Open Sidebar" and a button "New Chat",
 * expanded it offers "Close Sidebar", a link "New Chat" and the chat list. Each
 * surface therefore pins it, so a control proven on one pass is still present
 * on the next.
 */
export async function ensureSidebar(page: Page, open: boolean): Promise<void> {
  const toggle = page.getByRole("button", {
    name: open ? /open sidebar/i : /close sidebar/i,
  });
  if (await toggle.isVisible().catch(() => false)) {
    await toggle.click().catch(() => {});
    await page.waitForTimeout(SETTLE);
  }
}

export async function openSidebar(page: Page): Promise<void> {
  await gotoHome(page);
  await ensureSidebar(page, true);
}

export async function openSettings(page: Page): Promise<void> {
  await gotoHome(page);
  await page.getByRole("button", { name: /user menu/i }).first().click();
  await page.waitForTimeout(800);
  await page.getByRole("button", { name: /^settings$/i }).first().click();
  await page.getByRole("tab").first().waitFor({ timeout: 20_000 });
  await page.waitForTimeout(SETTLE);
}

async function clickTop(page: Page, name: RegExp): Promise<void> {
  await gotoHome(page);
  await ensureSidebar(page, false);
  await page.getByRole("button", { name }).first().click();
  await page.waitForTimeout(SETTLE);
}

/**
 * Put the surface back exactly as it was first enumerated and find one control
 * on it again. Every proof starts from this, because clicking a control can
 * change what the surface contains, and attributing that drift to the *next*
 * control reports working controls as dead ones.
 */
export async function relocate(
  page: Page,
  surface: Surface,
  key: string,
): Promise<Control | undefined> {
  await surface.open(page);
  const all = await enumerate(page, surface.id);
  return all.find((c) => c.key === key);
}

/** The surfaces that exist regardless of what the deployment contains. */
export const STATIC_SURFACES: Surface[] = [
  {
    id: "home",
    open: async (page) => {
      await gotoHome(page);
      await ensureSidebar(page, false);
    },
  },
  { id: "sidebar", open: openSidebar, delta: true },
  {
    id: "model-picker",
    open: (page) => clickTop(page, /selected model/i),
    delta: true,
  },
  { id: "composer-more", open: (page) => clickTop(page, /^more$/i), delta: true },
  {
    id: "composer-integrations",
    open: (page) => clickTop(page, /^integrations$/i),
    delta: true,
  },
  {
    id: "composer-controls",
    open: (page) => clickTop(page, /^controls$/i),
    delta: true,
  },
  { id: "user-menu", open: (page) => clickTop(page, /user menu/i), delta: true },
  { id: "search", open: (page) => clickTop(page, /^search$/i), delta: true },
  {
    id: "workspace",
    open: async (page) => {
      await page.goto("/workspace", { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(3000);
    },
  },
  {
    id: "chat-item-menu",
    open: async (page) => {
      await openSidebar(page);
      await page.getByRole("button", { name: /^chat menu$/i }).first().click();
      await page.waitForTimeout(SETTLE);
    },
    delta: true,
  },
  {
    id: "chat-message-actions",
    open: async (page) => {
      await openSidebar(page);
      const chat = page.locator('a[href^="/c/"]').first();
      await chat.click({ timeout: 20_000 });
      await page.waitForTimeout(4000);
      // The action bar is hover-revealed on the last assistant message.
      const messages = page.locator("[id^='message-']");
      const count = await messages.count();
      if (count > 0) await messages.nth(count - 1).hover();
      await page.waitForTimeout(SETTLE);
    },
    delta: true,
  },
];

/**
 * Settings tabs are read from the rendered tablist, not from a constant, so a
 * tab added or removed by an upstream bump or a Hive patch changes the surface
 * list on the next run with no code change.
 */
export async function discoverSettingsTabs(page: Page): Promise<Surface[]> {
  await openSettings(page);
  const tabs = await page.getByRole("tab").allInnerTexts();
  const named = tabs.map((t) => t.replace(/\s+/g, " ").trim()).filter(Boolean);
  // Discovering nothing is a broken modal, not a deployment with no settings.
  // Returning an empty list quietly would drop every settings tab out of the
  // denominator and report the remaining surfaces as the whole app.
  if (named.length === 0) {
    throw new Error(
      "settings discovery found no tabs; the settings modal did not render, so its controls " +
        "would silently leave the denominator",
    );
  }
  return named
    .map((tab, index) => ({
      id: `settings:${tab}`,
      persists: true,
      // Always a delta. The first tab owns the modal chrome (close, search, the
      // tablist, the Admin Settings link) but not the chat page underneath it,
      // which is what the sidebar and composer surfaces already cover and which
      // the modal makes unclickable anyway.
      delta: true,
      chrome: index === 0,
      open: async (page: Page) => {
        await openSettings(page);
        if (index > 0) {
          await page.getByRole("tab", { name: tab, exact: true }).first().click();
          await page.waitForTimeout(SETTLE);
        }
      },
    }));
}

/** Same idea for the Workspace section's own navigation. */
export async function discoverWorkspaceTabs(page: Page): Promise<Surface[]> {
  await page.goto("/workspace", { waitUntil: "domcontentloaded" });
  await page.waitForTimeout(3000);
  const hrefs = await page
    .locator('a[href^="/workspace/"]')
    .evaluateAll((els) =>
      [...new Set(els.map((e) => e.getAttribute("href") ?? ""))].filter(Boolean),
    );
  // Same reasoning as the settings tabs above. An empty result here used to be
  // returned silently, which took every workspace section out of the
  // denominator without a word in the report.
  if (hrefs.length === 0) {
    throw new Error(
      "workspace discovery found no sections; /workspace rendered no links, so every " +
        "workspace:* control would silently leave the denominator",
    );
  }
  return hrefs.map((href) => ({
    id: `workspace:${href.replace("/workspace/", "")}`,
    delta: true,
    open: async (page: Page) => {
      await page.goto(href, { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(3000);
    },
  }));
}

/**
 * Enumerate a surface. For delta surfaces the page underneath is enumerated
 * first and its controls are subtracted, so each control is attributed to
 * exactly one surface and counted once.
 */
export async function enumerateSurface(page: Page, surface: Surface): Promise<Control[]> {
  const signature = (key: string) => key.slice(key.indexOf("::"));
  let baseline = new Set<string>();
  if (surface.delta) {
    if (surface.id.startsWith("settings:")) {
      if (surface.chrome) {
        await gotoHome(page);
        await ensureSidebar(page, false);
      } else {
        await openSettings(page);
      }
    } else if (surface.id.startsWith("workspace:")) {
      await page.goto("/workspace", { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(3000);
    } else {
      // The canonical home state, sidebar collapsed. Taking the baseline in
      // whatever state the previous surface left behind made the sidebar's own
      // delta come out empty, because the sidebar was already open.
      await gotoHome(page);
      await ensureSidebar(page, false);
    }
    baseline = new Set(
      (await enumerate(page, "__base")).map((c) => signature(c.key)),
    );
  }
  await surface.open(page);
  const all = await enumerate(page, surface.id);
  return surface.delta ? all.filter((c) => !baseline.has(signature(c.key))) : all;
}
