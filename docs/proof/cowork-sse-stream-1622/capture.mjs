/*
 * Cowork step streaming over SSE, issue #1622.
 *
 * Drives the real forked chat front end through a Cowork submission and
 * measures, for each step the run takes, how long it took to appear in the
 * transcript. The agent API is served by agent-wire.mjs beside this file,
 * which is a real HTTP server in front of the real chat container rather than
 * a browser interception: the claim under test is that a body arrives in
 * pieces over an open connection, and Playwright's route.fulfill can only send
 * a body it has already finished writing.
 *
 * Run it twice against the same front end:
 *
 *   MODE=stream  the wire serves the subscription
 *   MODE=poll    the wire answers the subscription 404, which is what a
 *                deployment without this change does, so the front end falls
 *                back to the three second cursor read
 *
 * The difference between the two latency tables is the whole claim.
 *
 * performance.now(), not Date.now(), for every elapsed measurement. This runs
 * on WSL2, whose wall clock is periodically resynchronised against the Windows
 * host and steps backwards by a few hundred milliseconds when it does, which
 * put samples out of order in the sibling capture's first published artifacts.
 */

import { chromium } from '@playwright/test';

const APP = process.env.APP_URL || 'http://127.0.0.1:3423';
const OUT = process.env.OUT_DIR || '/tmp/proof-1622-sse';
const LABEL = process.env.LABEL || (process.env.MODE === 'poll' ? 'poll' : 'stream');
const EMAIL = process.env.PROOF_EMAIL || 'cowork-proof@hive.invalid';
const PASSWORD = process.env.PROOF_PASSWORD;
if (!PASSWORD) {
	throw new Error('PROOF_PASSWORD is required: this account is created on a throwaway container');
}

const INSTRUCTIONS =
	'Create a file named sixcap.txt containing the text HIVE-COWORK-OK, then show its contents';

/*
 * When each step happens, and the words that identify it on screen. Kept in
 * step with agent-wire.mjs: this half says what to look for, that half says
 * when it happens, and a mismatch shows up as a step that never appears rather
 * than as a silent pass.
 */
const EXPECTED = [
	{ at: 3.0, match: 'list the workspace' },
	{ at: 4.2, match: 'AGENTS.md' },
	{ at: 5.4, match: 'write sixcap.txt' },
	{ at: 6.6, match: 'wrote 14 bytes' },
	// 'Workspace file: sixcap.txt', not 'sixcap.txt': the bare name also
	// occurs in the tool call two steps earlier, so the short matcher reported
	// this step as having appeared before it happened.
	{ at: 7.8, match: 'Workspace file: sixcap.txt' },
	{ at: 9.0, match: 'cat sixcap.txt' },
	{ at: 10.2, match: 'HIVE-COWORK-OK' }
];

const mono0 = performance.now();
const since = () => performance.now() - mono0;
const log = [];
const say = (line) => {
	const stamp = (since() / 1000).toFixed(2).padStart(6);
	log.push(`[+${stamp}s] ${line}`);
	console.log(`[+${stamp}s] ${line}`);
};

const browser = await chromium.launch({ args: ['--no-sandbox'] });
const context = await browser.newContext({
	viewport: { width: 1280, height: 900 },
	recordVideo: { dir: `${OUT}/video-${LABEL}`, size: { width: 1280, height: 900 } }
});
const page = await context.newPage();

const shoot = async (name) => {
	await page.screenshot({ path: `${OUT}/${LABEL}-${name}.png` });
	say(`shot ${name}`);
};

say(`capture start, label=${LABEL}, app=${APP}`);
await page.goto(`${APP}/auth`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(2500);

// The account is created out of band through the front end's own signup API
// (see the README beside this file): the fork's sign-in page offers no sign-up
// form, and standing one up is not what this capture is about.
await page.locator('input[type="email"], input[autocomplete="email"]').first().fill(EMAIL);
await page.locator('input[type="password"]').first().fill(PASSWORD);
await page.getByRole('button', { name: /sign in/i }).first().click();
await page.waitForTimeout(6000);
say(`signed in, url=${page.url()}`);

for (const name of [/okay.*let.*s go/i, /get started/i, /dismiss/i, /close/i]) {
	const button = page.getByRole('button', { name }).first();
	if (await button.isVisible().catch(() => false)) {
		await button.click().catch(() => {});
		await page.waitForTimeout(800);
	}
}
await page.waitForTimeout(2000);
await shoot('01-composer');

// The mode toggle: a radiogroup with Chat and Work.
const work = page.getByRole('radio', { name: 'Work', exact: true }).first();
await work.waitFor({ state: 'visible', timeout: 20000 });
await work.click();
await page.waitForTimeout(800);
say('composer switched to Work');

const composer = page.locator('#chat-input, textarea#chat-input, [contenteditable="true"]').first();
await composer.click();
await composer.fill?.(INSTRUCTIONS).catch(async () => {
	await page.keyboard.type(INSTRUCTIONS);
});
await page.waitForTimeout(400);
await shoot('02-brief-typed');

const submittedAt = performance.now();
await page.keyboard.press('Enter');
say('submitted');

/*
 * Sample twice a second. The interval is what bounds the precision of every
 * number below, so it has to be well under the three second poll the control
 * run uses, or the two runs would be indistinguishable by construction.
 */
const timeline = [];
const firstSeen = new Map();
const shotsAt = [3.5, 5.0, 6.5, 8.0, 9.5, 11.0, 13.5];
let nextShot = 0;
const deadline = Date.now() + 20000;

while (Date.now() < deadline) {
	await page.waitForTimeout(500);
	const lines = await page
		.locator('.status-description')
		.allInnerTexts()
		.catch(() => []);
	const runAt = (performance.now() - submittedAt) / 1000;
	const cleaned = lines.map((line) => line.replace(/\s+/g, ' ').trim()).filter(Boolean);
	const joined = cleaned.join(' | ');

	for (const step of EXPECTED) {
		if (!firstSeen.has(step.match) && joined.includes(step.match)) {
			firstSeen.set(step.match, runAt);
			say(`step "${step.match}" happened at +${step.at.toFixed(1)}s, first on screen at +${runAt.toFixed(1)}s`);
		}
	}

	timeline.push({
		since_submit: Math.round(runAt * 10) / 10,
		line_count: cleaned.length,
		lines: cleaned
	});

	if (nextShot < shotsAt.length && runAt >= shotsAt[nextShot]) {
		await shoot(`03-run-t${shotsAt[nextShot].toFixed(1).replace('.', 'p')}s`);
		nextShot += 1;
	}
}
await shoot('04-finished');

/*
 * The measurement. Lateness is what separates a stream from a poll: both put
 * every step on screen eventually, and "eventually" is the defect.
 */
say('--- how late each step was ---');
const latencies = [];
for (const step of EXPECTED) {
	const seen = firstSeen.get(step.match);
	if (seen === undefined) {
		say(`  "${step.match}" never appeared`);
		continue;
	}
	const late = seen - step.at;
	latencies.push(late);
	say(`  "${step.match}": happened +${step.at.toFixed(1)}s, shown +${seen.toFixed(1)}s, late by ${late.toFixed(1)}s`);
}
const worst = latencies.length ? Math.max(...latencies) : null;
const mean = latencies.length ? latencies.reduce((a, b) => a + b, 0) / latencies.length : null;
const peak = Math.max(...timeline.map((s) => s.line_count), 0);

say('--- summary ---');
say(`steps that appeared: ${latencies.length} of ${EXPECTED.length}`);
say(`worst lateness: ${worst === null ? 'n/a' : worst.toFixed(1) + 's'}`);
say(`mean lateness: ${mean === null ? 'n/a' : mean.toFixed(1) + 's'}`);
say(`most step lines on screen at once: ${peak}`);
say(`final step lines: ${JSON.stringify(timeline.at(-1)?.lines ?? [])}`);

await context.close();
await browser.close();

const fs = await import('node:fs/promises');
await fs.writeFile(`${OUT}/capture-${LABEL}.log`, log.join('\n') + '\n');
await fs.writeFile(
	`${OUT}/timeline-${LABEL}.json`,
	JSON.stringify(
		{
			label: LABEL,
			expected: EXPECTED,
			first_seen: Object.fromEntries([...firstSeen].map(([k, v]) => [k, Math.round(v * 10) / 10])),
			worst_lateness_s: worst === null ? null : Math.round(worst * 10) / 10,
			mean_lateness_s: mean === null ? null : Math.round(mean * 10) / 10,
			peak_lines: peak,
			samples: timeline
		},
		null,
		2
	) + '\n'
);
