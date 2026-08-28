/*
 * Capture the console's Available balance card, rendered by the real
 * BillingOverview component from this branch, inside the real Next.js app and
 * against the real stylesheet, with fixture props.
 *
 * This is a FIXTURE capture and the log says so where it is written. The two
 * real balance surfaces both sit behind a validated Supabase session
 * (getRequestContext in apps/web-console/lib/control-plane/client.ts calls
 * getUser(), a server-side round trip), and no Supabase was legitimately
 * available for this capture. What is fixture here is the balance numbers and
 * the absence of a control plane; the component, its formatter and the
 * stylesheet are the shipped ones.
 */
import { chromium } from 'playwright';
import { writeFileSync } from 'node:fs';

const OUT = process.env.OUT_DIR ?? '/out';
const BASE = process.env.CONSOLE_URL ?? 'http://console-proof:3000';

const log = [];
const say = (line) => {
	log.push(line);
	console.log(line);
};

const browser = await chromium.launch();
const context = await browser.newContext({
	viewport: { width: 1200, height: 700 },
	deviceScaleFactor: 2
});
const page = await context.newPage();

say('FIXTURE CAPTURE. Real component, real stylesheet, fixture balance.');
say('Balance fixture: 458,419,464 available, 460,000,000 posted, 1,580,536 reserved.');
say('That available figure is the one observed on the live demo box on 2026-08-28.');
say('');

await page.goto(`${BASE}/proof-fixture`, { waitUntil: 'networkidle', timeout: 180_000 });
await page.waitForSelector('[data-numeric]', { timeout: 120_000 });

const headline = await page.locator('[data-numeric]').first().textContent();
say(`headline figure rendered: ${JSON.stringify(headline)}`);

const cardText = (await page.locator('main').innerText()).split('\n').filter(Boolean);
say('card text, line by line:');
for (const line of cardText) {
	say(`  ${line}`);
}

await page.locator('main').screenshot({ path: `${OUT}/03-console-balance-in-usd.png` });
say('');
say('captured 03-console-balance-in-usd.png');

writeFileSync(`${OUT}/console-capture.log`, `${log.join('\n')}\n`);

await browser.close();
