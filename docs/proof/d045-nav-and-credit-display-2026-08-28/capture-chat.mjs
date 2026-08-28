/*
 * Capture the chat shell served by this branch, through this repository's own
 * Caddyfile.owui, for the two chat-side findings in this change.
 *
 * 1. The sidebar carries no Knowledge row (D-045 ruling 2, issue #1109).
 * 2. /agent-workspace/tasks, the URL that used to render a second
 *    "Sign in to run agent tasks" gate, redirects to the chat root.
 *
 * Both are read out of the running page rather than described: the nav ids
 * present in the DOM are printed, and the redirect is followed with the
 * response chain recorded, so the log states what the browser actually saw.
 */
import { chromium } from 'playwright';
import { writeFileSync } from 'node:fs';

const OUT = process.env.OUT_DIR ?? '/out';
const BASE = process.env.CHAT_URL ?? 'http://caddy-proof:80';

const log = [];
const say = (line) => {
	log.push(line);
	console.log(line);
};

const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await context.newPage();

say(`base origin under test: ${BASE}`);
say('');

// ---------------------------------------------------------------- nav rows
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' });
await page.waitForSelector('#sidebar', { timeout: 60_000 });

// The sidebar ships two variants of every row and only one is visible: a
// 56px icon rail and the expanded row. Open it so the labels render.
const opener = page.getByLabel('Open Sidebar').first();
if (await opener.isVisible().catch(() => false)) {
	await opener.click();
	await page.waitForTimeout(500);
}

const navIds = await page.$$eval('[data-hive-nav]', (nodes) =>
	Array.from(new Set(nodes.map((node) => node.getAttribute('data-hive-nav')))).sort()
);
say(`nav ids present in the DOM: ${JSON.stringify(navIds)}`);
say(`knowledge row present: ${navIds.includes('knowledge')}`);
say(`workspace row present: ${navIds.includes('workspace')}`);

const rowText = await page
	.$$eval('#sidebar a[id^="hive-nav-"]', (nodes) =>
		nodes.map((node) => `${node.id} -> ${node.getAttribute('href')} "${node.textContent.trim()}"`)
	)
	.catch(() => []);
for (const row of rowText) {
	say(`  ${row}`);
}
say('');

await page.screenshot({ path: `${OUT}/01-sidebar-no-knowledge.png`, fullPage: false });
say('captured 01-sidebar-no-knowledge.png');
say('');

// -------------------------------------------------------- retired route
const chain = [];
page.on('response', (response) => {
	const { pathname } = new URL(response.url());
	if (pathname === '/agent-workspace/tasks' || pathname === '/') {
		chain.push(`${response.status()} ${pathname} -> ${response.headers().location ?? '(no Location)'}`);
	}
});

const target = `${BASE}/agent-workspace/tasks`;
say(`navigating to ${target}`);
await page.goto(target, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(1500);

const landedOn = new URL(page.url()).pathname;
say(`response chain: ${JSON.stringify(chain)}`);
say(`browser landed on: ${landedOn}`);
say(`second sign-in gate on the page: ${await page.getByText('Sign in to run agent tasks').count()}`);
say(`chat composer present on the landing page: ${await page.locator('#chat-input, [data-hive-cowork-row], textarea').count()}`);

await page.screenshot({ path: `${OUT}/02-agent-workspace-redirects-to-chat.png`, fullPage: false });
say('captured 02-agent-workspace-redirects-to-chat.png');

writeFileSync(`${OUT}/chat-capture.log`, `${log.join('\n')}\n`);

await browser.close();
