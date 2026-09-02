/*
 * Cowork step streaming capture, issues #1622 and #1504.
 *
 * Drives the real forked chat front end, built into a Docker image, through a
 * real Cowork submission, and records what the transcript shows while the run
 * is going.
 *
 * WHAT IS REAL AND WHAT IS SCRIPTED
 *
 * Real: the built front end, the composer, its mode toggle, the submit path,
 * agentTasks.ts's fetch and decode, foldRunSteps, applyCoworkRun, and the
 * transcript components that render the result. The chat is created and saved
 * through the front end's own backend, as any conversation is.
 *
 * Scripted: the agent API, intercepted in the browser. There is no Apptainer
 * sandbox on this box (the SIF is linux/amd64 and cannot be built or launched
 * on WSL2), so a real run is impossible here and the wire is played back
 * instead. The two wire shapes below are not invented: they are exactly the
 * two behaviours the Go tests in this change pin.
 *
 *   WIRE=old   The chain before this change. Step events become readable only
 *              AFTER the task reports a terminal status, because the poller
 *              wrote that status and the event syncer pulled the tail on an
 *              unrelated loop afterwards. The transcript's follower stops at
 *              the terminal status, so it never sees them.
 *   WIRE=new   The chain after it. Events appear while the run is going, and
 *              the last of them are readable strictly before the terminal
 *              status, because the poller flushes them immediately before it
 *              records that status.
 *
 * Run the old image with WIRE=old and this branch's image with WIRE=new for
 * the before and after pair.
 */

import { chromium } from 'playwright';

const OWUI = process.env.OWUI_URL || 'http://localhost:8080';
const OUT = process.env.OUT_DIR || '/out';
const LABEL = process.env.LABEL || 'after';
const WIRE = process.env.WIRE || 'new';
const EMAIL = process.env.PROOF_EMAIL || 'cowork-proof@hive.invalid';
const PASSWORD = process.env.PROOF_PASSWORD;
if (!PASSWORD) {
	throw new Error('PROOF_PASSWORD is required: this account is created on a throwaway container');
}

/*
 * performance.now(), not Date.now(), for every elapsed measurement here.
 *
 * Not a style choice. This runs on WSL2, whose wall clock is periodically
 * resynchronised against the Windows host and steps BACKWARDS when it is, by a
 * few hundred milliseconds. Stamping the samples with Date.now() put a sample
 * or two out of order in every published timeline, at a different point each
 * run, which reads as a broken harness and was flagged as one in review.
 * performance.now() is monotonic from process start and cannot do that.
 *
 * The scripted wire below still uses Date.now(), deliberately: those values
 * are ISO timestamps on a payload, where the wall clock is the right clock and
 * a few hundred milliseconds do not matter.
 */
const log = [];
const say = (line) => {
	const stamp = (since() / 1000).toFixed(2).padStart(6);
	log.push(`[+${stamp}s] ${line}`);
	console.log(`[+${stamp}s] ${line}`);
};
const t0 = Date.now();
const mono0 = performance.now();
const since = () => performance.now() - mono0;

/*
 * The run this capture plays back: two tool calls with their results, a
 * closing message, and one workspace file. The same shape the Go test
 * TestPoller_StoresEveryStepBeforeItPublishesATerminalStatus asserts on.
 */
const TASK_ID = '6567c0fb-e34a-4609-9fc7-bbea32fde598';
// `source_event_id` is on the wire (TaskEvent in
// apps/control-plane/internal/agenttask/events.go) and is carried here even
// though this front end's decodeEvent ignores it, so the playback cannot pass
// against a shape the server never sends. HIVE-COWORK-OK is fourteen bytes;
// the sizes below say fourteen for the same reason.
const STEPS = [
	{ seq: 1, source_event_id: 'e1', kind: 'tool_call', payload: { tool_name: 'bash', tool_call_id: 'c1', preview: 'list the workspace' } },
	{ seq: 2, source_event_id: 'e2', kind: 'tool_result', payload: { tool_name: 'bash', tool_call_id: 'c1', preview: 'AGENTS.md' } },
	{ seq: 3, source_event_id: 'e3', kind: 'tool_call', payload: { tool_name: 'str_replace_editor', tool_call_id: 'c2', preview: 'write sixcap.txt' } },
	{ seq: 4, source_event_id: 'e4', kind: 'tool_result', payload: { tool_name: 'str_replace_editor', tool_call_id: 'c2', preview: 'wrote 14 bytes' } },
	{ seq: 5, source_event_id: 'file:sixcap.txt:14:1756800000', kind: 'file', payload: { name: 'sixcap.txt', size: 14 } },
	{ seq: 6, source_event_id: 'e6', kind: 'message', payload: { role: 'agent', preview: 'sixcap.txt now holds HIVE-COWORK-OK' } }
];

// When each step becomes readable, in milliseconds after the run was
// submitted. Under the old wire nothing is readable until the run has already
// been reported finished.
const SANDBOX_READY_MS = 6000;
const STEP_AT_MS = [8000, 10000, 12000, 14000, 15000, 16000];
const FINISHED_MS = 17000;

const taskAt = (elapsed) => {
	const base = {
		id: TASK_ID,
		pack: 'knowledge-work-pack',
		instructions: 'Create a file named sixcap.txt containing the text HIVE-COWORK-OK, then show its contents',
		engine_session_ref: elapsed >= SANDBOX_READY_MS ? 'session-1' : '',
		result_summary_ref: '',
		error_message: '',
		created_at: new Date(t0).toISOString(),
		updated_at: new Date().toISOString(),
		started_at: elapsed >= SANDBOX_READY_MS ? new Date(t0 + SANDBOX_READY_MS).toISOString() : null,
		finished_at: elapsed >= FINISHED_MS ? new Date(t0 + FINISHED_MS).toISOString() : null
		// See the clock note at the top: these are wall-clock ISO strings on a
		// payload, which is what the real wire carries.
	};
	if (elapsed >= FINISHED_MS) {
		return { ...base, status: 'succeeded', result_summary_ref: 'sixcap.txt now holds HIVE-COWORK-OK' };
	}
	if (elapsed >= SANDBOX_READY_MS) {
		return { ...base, status: 'running' };
	}
	return { ...base, status: 'queued' };
};

const eventsAt = (elapsed, afterSeq) =>
	STEPS.filter((step, index) => {
		const readable =
			WIRE === 'old'
				// Written by the syncer's finishVanished, which runs only once
				// the row has left the active set, and that happens only after
				// the terminal transition. The lag is one syncer interval,
				// which on main is the 15s default of
				// HIVE_AGENT_TASK_POLL_INTERVAL, shared with the status poller.
				? elapsed >= FINISHED_MS + 15000
				: elapsed >= STEP_AT_MS[index];
		return readable && step.seq > afterSeq;
	}).map((step) => ({ ...step, created_at: new Date().toISOString() }));

let submittedAt = null;
const requests = [];

const browser = await chromium.launch({ args: ['--no-sandbox'] });
const context = await browser.newContext({
	viewport: { width: 1280, height: 900 },
	recordVideo: { dir: `${OUT}/video-${LABEL}`, size: { width: 1280, height: 900 } }
});
const page = await context.newPage();

await page.route('**/api/v1/hive/agent/**', async (route) => {
	const request = route.request();
	const url = new URL(request.url());
	const elapsed = submittedAt === null ? 0 : performance.now() - submittedAt;
	const json = (body) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(body) });

	if (request.method() === 'POST' && url.pathname.endsWith('/tasks')) {
		submittedAt = performance.now();
		requests.push(`${since()} POST /tasks`);
		say('composer submitted the run');
		return json(taskAt(0));
	}
	if (url.pathname.endsWith('/events')) {
		const afterSeq = Number(url.searchParams.get('after_seq') || 0);
		const events = eventsAt(elapsed, afterSeq);
		requests.push(`${since()} GET /events?after_seq=${afterSeq} -> ${events.length}`);
		return json({ events });
	}
	if (request.method() === 'GET') {
		const task = taskAt(elapsed);
		requests.push(`${since()} GET /tasks/{id} -> ${task.status}`);
		return json(task);
	}
	return json({});
});

const shoot = async (name) => {
	const file = `${OUT}/${LABEL}-${name}.png`;
	await page.screenshot({ path: file });
	say(`shot ${name}`);
};

say(`capture start, wire=${WIRE}, label=${LABEL}`);
await page.goto(`${OWUI}/auth`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(2500);

// The account is created out of band, through the front end's own signup API
// (see the README beside this file): the fork's sign-in page offers no sign-up
// form, and standing one up is not what this capture is about.
await page.locator('input[type="email"], input[autocomplete="email"]').first().fill(EMAIL);
await page.locator('input[type="password"]').first().fill(PASSWORD);
await page.getByRole('button', { name: /sign in/i }).first().click();
await page.waitForTimeout(6000);
say(`signed in, url=${page.url()}`);

// Dismiss whatever the first-run flow puts in front of the composer.
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
await shoot('02-work-mode');

const composer = page.locator('#chat-input, textarea#chat-input, [contenteditable="true"]').first();
await composer.click();
await composer.fill?.('Create a file named sixcap.txt containing the text HIVE-COWORK-OK, then show its contents').catch(async () => {
	await page.keyboard.type('Create a file named sixcap.txt containing the text HIVE-COWORK-OK, then show its contents');
});
await page.waitForTimeout(500);
await shoot('03-brief-typed');
await page.keyboard.press('Enter');

// Watch the transcript for the whole run, sampling what it shows.
const timeline = [];
const deadline = Date.now() + 42000;
let shots = 0;
while (Date.now() < deadline) {
	await page.waitForTimeout(1500);
	const lines = await page
		.locator('.status-description')
		.allInnerTexts()
		.catch(() => []);
	// Sampled AFTER the read, and on the capture's own clock rather than on
	// one that only starts when the submission lands, so the committed
	// timeline is monotonic by construction. The earlier version stamped each
	// sample before an await and against a start time that was still null on
	// the first iterations, which put a sample or two out of order in the
	// published artifacts and cost more to explain than to remove.
	const elapsed = since();
	const sinceSubmit = submittedAt === null ? null : performance.now() - submittedAt;
	const cleaned = lines.map((line) => line.replace(/\s+/g, ' ').trim()).filter(Boolean);
	timeline.push({
		at: Math.round(elapsed / 100) / 10,
		since_submit: sinceSubmit === null ? null : Math.round(sinceSubmit / 100) / 10,
		lines: cleaned
	});
	say(`run t+${sinceSubmit === null ? '?' : (sinceSubmit / 1000).toFixed(1)}s transcript shows ${cleaned.length} step line(s): ${JSON.stringify(cleaned)}`);
	if ([8, 14, 26].includes(++shots)) {
		await shoot(`04-running-${shots}`);
	}
}
await page.waitForTimeout(1500);
await shoot('05-finished');

const finalLines = timeline.at(-1)?.lines ?? [];
const peak = Math.max(...timeline.map((sample) => sample.lines.length), 0);
const whileRunning = timeline.filter(
	(sample) => sample.since_submit !== null && sample.since_submit < FINISHED_MS / 1000
);
const duringRun = whileRunning.filter((sample) => sample.lines.length > 0).length;

say('--- summary ---');
say(`most step lines on screen at once: ${peak}`);
say(`samples during the run that showed any step: ${duringRun} of ${whileRunning.length}`);
say(`final step lines: ${JSON.stringify(finalLines)}`);
say('--- agent API calls the front end made ---');
for (const line of requests) {
	log.push(`  ${line}`);
}

await context.close();
await browser.close();

const fs = await import('node:fs/promises');
await fs.writeFile(`${OUT}/capture-${LABEL}.log`, log.join('\n') + '\n');
await fs.writeFile(`${OUT}/timeline-${LABEL}.json`, JSON.stringify(timeline, null, 2) + '\n');
