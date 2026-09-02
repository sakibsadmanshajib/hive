import { chromium } from 'playwright';

const OUT = '/out';
const BASE = process.env.OWUI_URL;
const password = process.env.PROOF_PASSWORD;
const email = 'proof1694@hive.invalid';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 2 });
const log = [];
page.on('console', (m) => log.push(`console:${m.type()}: ${m.text()}`));

// Sign up through the front end's own API rather than the form, so the
// capture does not depend on the sign-in layout. The account is local to this
// throwaway container and its password is generated per run.
const signup = await fetch(`${BASE}/api/v1/auths/signup`, {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ name: 'Proof 1694', email, password })
});
const account = await signup.json();
if (!account.token) throw new Error(`signup failed: ${signup.status}`);
log.push(`signup status ${signup.status}, session token received (redacted)`);
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded', timeout: 120000 });
await page.context().addCookies([
  { name: 'token', value: account.token, url: BASE }
]);
await page.evaluate((t) => localStorage.setItem('token', t), account.token);

// The composer banner mounts on the chat surface and fetches its own balance.
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded', timeout: 120000 });
await page.waitForTimeout(8000);
const banner = page.locator('[role="status"]', { hasText: /today/ }).first();
await banner.waitFor({ state: 'visible', timeout: 30000 });
await banner.screenshot({ path: `${OUT}/1694-05-chat-credits-banner.png` });
log.push(`banner text: ${(await banner.innerText()).replace(/\n/g, ' ')}`);

// Settings, Usage tab: the sidebar user menu opens the settings modal, and the
// Usage tab is one of its tabs (aria-controls=tab-usage).
await page.getByRole('button', { name: /User menu/i }).first().click();
await page.waitForTimeout(1200);
await page.getByRole('button', { name: /^Settings$/ }).first().click();
await page.waitForTimeout(2500);
await page.locator('button[aria-controls="tab-usage"]').first().click();
await page.waitForTimeout(6000);
const usagePane = page.locator('#tab-usage, [aria-labelledby]').filter({ hasText: 'Organization credit balance' }).last();
await usagePane.waitFor({ state: 'visible', timeout: 30000 });
await page.screenshot({ path: `${OUT}/1694-06-chat-settings-usage.png` });
log.push(`usage pane text: ${(await usagePane.innerText()).replace(/\n/g, ' | ')}`);

const bodyText = await page.locator('body').innerText();
const currency = bodyText.match(/[$৳€£¥]|USD|BDT/g);
log.push(`currency marks in rendered chat surfaces: ${currency ? currency.join(',') : 'none'}`);
log.push(`url: ${BASE}`);
console.log(log.join('\n'));
await browser.close();
