import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';

const OUT = '/out';
mkdirSync(OUT, { recursive: true });
const URL = process.env.TARGET_URL;

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1200 }, deviceScaleFactor: 2 });
const log = [];
page.on('console', (m) => log.push(`console:${m.type()}: ${m.text()}`));
await page.goto(URL, { waitUntil: 'networkidle', timeout: 120000 });
await page.waitForTimeout(1500);

const shots = [
  ['1694-01-credit-balance.png', 'Credit balance'],
  ['1694-02-api-keys.png', 'API keys'],
  ['1694-03-analytics.png', 'Analytics'],
  ['1694-04-model-catalog.png', 'Model catalog'],
];
for (const [file, heading] of shots) {
  const section = page.locator('section', { has: page.locator(`h2:text-is("${heading}")`) }).first();
  await section.screenshot({ path: `${OUT}/${file}` });
  log.push(`captured ${file} for section ${heading}`);
}

const body = await page.locator('main').innerText();
const currency = body.match(/[$৳€£¥]|USD|BDT/g);
log.push(`currency marks found in rendered main: ${currency ? currency.join(',') : 'none'}`);
const credits = [...body.matchAll(/[0-9][0-9,]{2,} credits?[a-z/]*/g)].map((m) => m[0]);
log.push(`credit figures rendered: ${[...new Set(credits)].join(' | ')}`);
log.push(`url: ${URL}`);
console.log(log.join('\n'));
await browser.close();
