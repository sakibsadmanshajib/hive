// #833 proof sweep: can an automated pass pin the sidebar open by accessible
// name, and are the four blocked surfaces reachable once it is?
// usage: node sweep.mjs <label> <outdir>
// Run from apps/web-console, where Playwright is already installed.
import { chromium } from 'playwright';
import fs from 'node:fs';
import path from 'node:path';

const BASE = process.env.OWUI_BASE ?? 'http://localhost:3009';
const label = process.argv[2] ?? 'run';
const outdir = process.argv[3] ?? '.';
const seed = process.env.SEED_CHATS !== '0';
fs.mkdirSync(outdir, { recursive: true });

const lines = [];
const say = (s) => { lines.push(s); console.log(s); };

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 } });
const page = await ctx.newPage();
await page.goto(BASE, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(6000);

if (seed) {
  // Two chats with a user turn and an assistant turn, created through Open
  // WebUI's own API with the session the page already holds. The token is
  // read inside the browser and never leaves it.
  const seeded = await page.evaluate(async () => {
    const token = localStorage.token;
    const out = [];
    for (const title of ['Accessibility proof chat', 'Second proof chat']) {
      const now = Math.floor(Date.now() / 1000);
      const a = crypto.randomUUID();
      const b = crypto.randomUUID();
      const user = { id: a, parentId: null, childrenIds: [b], role: 'user', content: 'What does the sidebar toggle announce?', timestamp: now };
      const bot = { id: b, parentId: a, childrenIds: [], role: 'assistant', content: 'Its accessible name, and whether the sidebar is open.', model: 'proof-model', modelName: 'proof-model', timestamp: now, done: true };
      const res = await fetch('/api/v1/chats/new', {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
        body: JSON.stringify({
          chat: {
            title,
            models: ['proof-model'],
            messages: [user, bot],
            history: { messages: { [a]: user, [b]: bot }, currentId: b },
            timestamp: now * 1000,
          },
        }),
      });
      out.push(`${title}: HTTP ${res.status}`);
    }
    return out;
  });
  for (const s of seeded) say(`seed ${s}`);
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(5000);
}

say(`\n===== ${label} =====`);
say(`url: ${page.url()}`);

const pin = page.getByRole('button', { name: 'Open Sidebar', exact: true, expanded: false });
say(`[pin] getByRole(button, name="Open Sidebar", expanded=false) -> ${await pin.count()}`);
say(`[pin] getByRole(button, name="Open Sidebar") (any state)     -> ${await page.getByRole('button', { name: 'Open Sidebar', exact: true }).count()}`);
say(`[pin] getByRole(button, name="Close Sidebar", expanded=true) -> ${await page.getByRole('button', { name: 'Close Sidebar', exact: true, expanded: true }).count()}`);

await page.screenshot({ path: path.join(outdir, `${label}-1-collapsed.png`) });

if (await pin.count()) {
  await pin.first().click();
  await page.waitForTimeout(2500);
  say('[pin] clicked the control found by accessible name');
}

say(`[after click] Close Sidebar, expanded=true -> ${await page.getByRole('button', { name: 'Close Sidebar', exact: true, expanded: true }).count()}`);
say(`[after click] Open Sidebar, expanded=false -> ${await page.getByRole('button', { name: 'Open Sidebar', exact: true, expanded: false }).count()}`);

// surface 1: sidebar
const rows = page.locator('#sidebar a[href^="/c/"]');
say(`[surface sidebar] chat rows visible -> ${await rows.count()}`);
await page.screenshot({ path: path.join(outdir, `${label}-2-sidebar-open.png`) });

// surface 2: chat-item-menu
const row = rows.first();
let itemMenuItems = 0;
if (await rows.count()) {
  await row.hover();
  await page.waitForTimeout(500);
  const itemMenu = page.locator('#sidebar').getByRole('button', { name: 'Chat Menu' });
  say(`[surface chat-item-menu] row menu buttons by name -> ${await itemMenu.count()}`);
  if (await itemMenu.count()) {
    await itemMenu.first().click();
    await page.waitForTimeout(1200);
    itemMenuItems = await page.getByRole('menuitem').count();
    say(`[surface chat-item-menu] menu items after open -> ${itemMenuItems}`);
    await page.screenshot({ path: path.join(outdir, `${label}-3-chat-item-menu.png`) });
    await page.keyboard.press('Escape');
    await page.waitForTimeout(600);
  }
}

// surface 3: chat-message-actions
let actions = 0;
if (await rows.count()) {
  await row.click();
  await page.waitForTimeout(3000);
  const assistant = page.locator('.chat-assistant, [id^="message-"]').first();
  if (await assistant.count()) await assistant.hover();
  await page.waitForTimeout(1000);
  for (const name of ['Copy', 'Edit', 'Read Aloud', 'Regenerate', 'Good Response', 'Bad Response']) {
    const n = await page.getByRole('button', { name, exact: true }).count();
    if (n) actions += n;
    say(`[surface chat-message-actions] "${name}" -> ${n}`);
  }
  await page.screenshot({ path: path.join(outdir, `${label}-4-message-actions.png`) });
}

// surface 4: composer-controls
const controls = page.getByRole('button', { name: 'Controls', exact: true });
say(`[surface composer-controls] Controls button -> ${await controls.count()}`);
let panel = 0;
if (await controls.count()) {
  await controls.first().click();
  await page.waitForTimeout(1500);
  panel = await page.getByText('Advanced Params', { exact: false }).count();
  say(`[surface composer-controls] panel content after click -> ${panel}`);
  await page.screenshot({ path: path.join(outdir, `${label}-5-controls.png`) });
}

const snap = await page.locator('body').ariaSnapshot();
fs.writeFileSync(path.join(outdir, `aria-${label}-sweep.txt`), snap);
say(`aria snapshot -> aria-${label}-sweep.txt`);

await ctx.close();
await browser.close();
fs.writeFileSync(path.join(outdir, `sweep-${label}.txt`), lines.join('\n') + '\n');
