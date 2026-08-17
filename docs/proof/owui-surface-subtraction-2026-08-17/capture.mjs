// A/B capture for the Open WebUI surface subtraction, 2026-08-17.
//
// Reads the surfaces out of the DOM as well as photographing them, so the
// claim in the README is asserted from state rather than from eyesight.
// Usage: node capture.mjs <label> <baseUrl> <outDir>
//
// Playwright is resolved from apps/web-console, the only place this repo
// installs it.
import { chromium } from '/home/sakib/hive/apps/web-console/node_modules/playwright-core/index.mjs';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const [label, baseUrl, outDir] = process.argv.slice(2);
if (!label || !baseUrl || !outDir) {
  console.error('usage: node capture.mjs <label> <baseUrl> <outDir>');
  process.exit(2);
}
mkdirSync(outDir, { recursive: true });

const log = [];
const say = (line) => {
  log.push(line);
  console.log(line);
};

const texts = (handles) =>
  Promise.all(handles.map(async (h) => (await h.innerText()).trim().replace(/\s+/g, ' ')));

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });

await page.goto(baseUrl, { waitUntil: 'networkidle' });
// The sidebar starts collapsed on a fresh profile. Its toggle carries the
// accessible name added by #833, which is the only stable handle on it.
const toggle = page.locator('[aria-label="Open Sidebar"]').first();
if (await toggle.count()) {
  await toggle.click();
}
await page.waitForSelector('#sidebar-new-chat-button:visible', { timeout: 60_000 });
await page.waitForTimeout(500);

// 1. Sidebar navigation entries. Upstream gives each pinned entry the id
// sidebar-<item>-button, and New Chat and Search the same shape.
const sidebarIds = await page.$$eval('[id^="sidebar-"][id$="-button"]', (nodes) =>
  nodes.map((n) => `${n.id}: ${n.innerText.trim().replace(/\s+/g, ' ')}`)
);
say(`[${label}] sidebar entries: ${JSON.stringify(sidebarIds)}`);
await page.screenshot({ path: join(outDir, `${label}-1-sidebar.png`) });

// 2. User menu.
await page.click('[aria-label="User menu"]');
await page.waitForTimeout(500);
const menuButtons = await page.$$('[role="menu"] button, [role="menu"] a');
const menuItems = (await texts(menuButtons)).filter(Boolean);
say(`[${label}] user menu: ${JSON.stringify(menuItems)}`);
await page.screenshot({ path: join(outDir, `${label}-2-usermenu.png`) });

// 3. Settings dialog tabs.
await page.keyboard.press('Escape');
await page.waitForTimeout(300);
await page.click('[aria-label="User menu"]');
await page.waitForTimeout(400);
await page.getByRole('button', { name: 'Settings', exact: true }).click();
await page.waitForSelector('#settings-tabs-container', { timeout: 30_000 });
await page.waitForTimeout(600);
const tabButtons = await page.$$('#settings-tabs-container button');
const tabs = (await texts(tabButtons)).filter(Boolean);
say(`[${label}] settings tabs: ${JSON.stringify(tabs)}`);

// The two entries inside the dialog that no flag reaches: the Admin Settings
// link in its bottom-left corner, and the vendor's translation link under the
// language picker on the General tab (which is the tab already open).
const adminLinks = await page.$$eval('a[href="/admin/settings"]', (n) => n.length);
const vendorLinks = await page.$$eval('[role="dialog"] a[href*="open-webui"], .modal a[href*="open-webui"]', (n) =>
  n.map((a) => a.getAttribute('href'))
);
say(`[${label}] settings admin links: ${adminLinks}`);
say(`[${label}] settings vendor links: ${JSON.stringify(vendorLinks)}`);
await page.screenshot({ path: join(outDir, `${label}-3-settings.png`) });

writeFileSync(join(outDir, `${label}.log`), `${log.join('\n')}\n`);
await browser.close();
