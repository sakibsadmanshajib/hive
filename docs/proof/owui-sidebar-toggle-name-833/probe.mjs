// Probe the running Open WebUI for the sidebar toggle's accessible name.
// usage: node probe.mjs <label> <outdir>
// Run from apps/web-console, where Playwright is already installed.
import { chromium } from 'playwright';
import fs from 'node:fs';
import path from 'node:path';

const BASE = process.env.OWUI_BASE ?? 'http://localhost:3009';
const label = process.argv[2] ?? 'run';
const outdir = process.argv[3] ?? '.';
fs.mkdirSync(outdir, { recursive: true });

const VIEWPORTS = [
  { name: 'desktop', width: 1280, height: 720 },
  { name: 'mobile', width: 390, height: 844 },
];

const lines = [];
const say = (s) => { lines.push(s); console.log(s); };

const browser = await chromium.launch();
for (const vp of VIEWPORTS) {
  const ctx = await browser.newContext({ viewport: { width: vp.width, height: vp.height } });
  const page = await ctx.newPage();
  await page.goto(BASE, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(6000);

  say(`\n===== ${label} / ${vp.name} ${vp.width}x${vp.height} =====`);
  say(`url: ${page.url()}`);

  // Every button in the document, with its computed accessible name and role state.
  const buttons = await page.evaluate(() => {
    const out = [];
    for (const el of document.querySelectorAll('button,[role="button"]')) {
      const r = el.getBoundingClientRect();
      out.push({
        id: el.id || null,
        cls: (el.className || '').toString().slice(0, 60),
        ariaLabel: el.getAttribute('aria-label'),
        ariaExpanded: el.getAttribute('aria-expanded'),
        ariaControls: el.getAttribute('aria-controls'),
        ariaHaspopup: el.getAttribute('aria-haspopup'),
        text: (el.innerText || '').trim().slice(0, 40),
        visible: r.width > 0 && r.height > 0,
      });
    }
    return out;
  });
  say('-- buttons in DOM (visible only) --');
  for (const b of buttons.filter((b) => b.visible)) say(JSON.stringify(b));

  // The query the coverage harness needs.
  for (const name of ['Open Sidebar', 'Close Sidebar', 'Temporary Chat', 'Save Chat', 'Chat options']) {
    const n = await page.getByRole('button', { name, exact: true }).count();
    say(`getByRole(button, name="${name}") -> ${n}`);
  }

  // Playwright's own accessibility snapshot of the page, trimmed to the top bar.
  const snap = await page.locator('body').ariaSnapshot();
  fs.writeFileSync(path.join(outdir, `aria-${label}-${vp.name}.txt`), snap);
  say(`aria snapshot -> aria-${label}-${vp.name}.txt (${snap.split('\n').length} lines)`);

  await page.screenshot({ path: path.join(outdir, `screen-${label}-${vp.name}.png`), fullPage: false });
  await ctx.close();
}
await browser.close();
fs.writeFileSync(path.join(outdir, `probe-${label}.txt`), lines.join('\n') + '\n');
