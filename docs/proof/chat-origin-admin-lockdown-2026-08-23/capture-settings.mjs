// #949 capture: the Settings dialog, before and after the admin surface leaves
// the fork. Reads the count of admin links out of the DOM as well as
// photographing the dialog, so the claim is asserted from state rather than
// from eyesight, and additionally asks the SPA to navigate to /admin/settings
// the same way the removed link did, to show there is no page left to render.
//
// usage: node capture-settings.mjs <label> <baseUrl> <outDir>
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// Playwright lives in apps/web-console, the only place this repo installs it,
// resolved relative to this file. A git worktree has no node_modules of its
// own, so PLAYWRIGHT_CORE overrides the path for that case rather than
// hard-coding one machine's layout.
const { chromium } = await import(
  process.env.PLAYWRIGHT_CORE ??
    new URL('../../../apps/web-console/node_modules/playwright-core/index.mjs', import.meta.url)
      .href
);

const [label, baseUrl, outDir] = process.argv.slice(2);
if (!label || !baseUrl || !outDir) {
  console.error('usage: node capture-settings.mjs <label> <baseUrl> <outDir>');
  process.exit(2);
}
mkdirSync(outDir, { recursive: true });

const log = [];
const say = (line) => {
  log.push(line);
  console.log(line);
};

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
const failed = [];
page.on('response', (r) => {
  if (r.status() >= 400) failed.push(`${r.status()} ${new URL(r.url()).pathname}`);
});

await page.goto(baseUrl, { waitUntil: 'networkidle' });
await page.waitForTimeout(2500);
await page.reload({ waitUntil: 'networkidle' });
await page.waitForTimeout(1500);

const role = await page.evaluate(async () => {
  const token = window.localStorage.getItem('token');
  const r = await fetch('/api/v1/auths/', {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  });
  if (!r.ok) return `auths -> ${r.status}`;
  const body = await r.json();
  return `${body.role} ${body.email}`;
});
say(`[${label}] session role, read from Open WebUI itself: ${role}`);

const toggle = page.locator('[aria-label="Open Sidebar"]').first();
if (await toggle.count()) await toggle.click();
await page.waitForTimeout(800);

await page.click('[aria-label="User menu"]');
await page.waitForTimeout(600);
await page.getByRole('button', { name: 'Settings', exact: true }).click();
await page.waitForSelector('#settings-tabs-container', { timeout: 30_000 });
await page.waitForTimeout(900);

const adminLinks = await page.$$eval('a[href="/admin/settings"]', (n) => n.length);
const adminText = await page.$$eval('#settings-tabs-container', (nodes) =>
  nodes.map((n) => n.innerText.replace(/\s+/g, ' ').trim()),
);
say(`[${label}] settings admin links in the DOM: ${adminLinks}`);
say(`[${label}] settings tab rail text: ${JSON.stringify(adminText)}`);
await page.screenshot({ path: join(outDir, `${label}-settings-dialog.png`) });

// The removed link called goto('/admin/settings'). Ask the SPA to do exactly
// that, so the claim is about the route and not only about the link.
await page.keyboard.press('Escape');
await page.waitForTimeout(300);
const navResult = await page.evaluate(async () => {
  history.pushState({}, '', '/admin/settings');
  window.dispatchEvent(new PopStateEvent('popstate'));
  await new Promise((r) => setTimeout(r, 1500));
  return {
    path: location.pathname,
    heading: (document.querySelector('h1, h2, .text-2xl')?.innerText ?? '').slice(0, 80),
    hasAdminPanel: Boolean(
      document.querySelector('[href="/admin/settings/general"], [href="/admin/users/overview"]'),
    ),
    bodyStart: document.body.innerText.replace(/\s+/g, ' ').trim().slice(0, 120),
  };
});
say(`[${label}] client-side push to /admin/settings: ${JSON.stringify(navResult)}`);
await page.screenshot({ path: join(outDir, `${label}-admin-route.png`) });

say(`[${label}] 4xx/5xx seen: ${JSON.stringify([...new Set(failed)])}`);
writeFileSync(join(outDir, `${label}.log`), `${log.join('\n')}\n`);
await browser.close();
