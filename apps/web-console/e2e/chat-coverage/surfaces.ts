// Surface openers. These are navigation recipes only: how to get the app into
// the state where a group of controls is on screen. They deliberately contain
// no list of controls -- what is on screen is read from the DOM by lib.ts, so
// a control added to any of these surfaces later is picked up without editing
// this file.
import type { Page } from "@playwright/test";

import { enumerate, type Control } from "./lib";

export type Surface = {
  id: string;
  /** Puts the app into the state where this surface's controls are rendered. */
  open: (page: Page) => Promise<void>;
  /**
   * Enumerate only what this surface adds on top of the page underneath it, so
   * the sidebar's buttons are not re-counted inside every dropdown.
   */
  delta?: boolean;
  /** Settings persist; a composer or search box does not. */
  persists?: boolean;
};

const SETTLE = 1500;

export async function gotoHome(page: Page): Promise<void> {
  if (new URL(page.url()).pathname !== "/") {
    await page.goto("/", { waitUntil: "domcontentloaded" });
  }
  await page.getByRole("button", { name: /new chat/i }).first().waitFor({ timeout: 60_000 });
  await page.waitForTimeout(SETTLE);
}

export async function openSidebar(page: Page): Promise<void> {
  await gotoHome(page);
  const opener = page.getByRole("button", { name: /open sidebar/i });
  if (await opener.isVisible().catch(() => false)) {
    await opener.click();
    await page.waitForTimeout(SETTLE);
  }
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
  await page.getByRole("button", { name }).first().click();
  await page.waitForTimeout(SETTLE);
}

/** The surfaces that exist regardless of what the deployment contains. */
export const STATIC_SURFACES: Surface[] = [
  { id: "home", open: gotoHome },
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
  return tabs
    .map((t) => t.replace(/\s+/g, " ").trim())
    .filter(Boolean)
    .map((tab, index) => ({
      id: `settings:${tab}`,
      persists: true,
      // The first tab owns the modal chrome (close, search, tablist, the
      // Admin Settings link); every later tab is a delta on top of it.
      delta: index > 0,
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
      await openSettings(page);
    } else if (surface.id.startsWith("workspace:")) {
      await page.goto("/workspace", { waitUntil: "domcontentloaded" });
      await page.waitForTimeout(3000);
    } else {
      await gotoHome(page);
    }
    baseline = new Set(
      (await enumerate(page, "__base")).map((c) => signature(c.key)),
    );
  }
  await surface.open(page);
  const all = await enumerate(page, surface.id);
  return surface.delta ? all.filter((c) => !baseline.has(signature(c.key))) : all;
}
