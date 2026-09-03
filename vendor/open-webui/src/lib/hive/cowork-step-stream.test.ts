/*
 * The per-run event subscription (issue #1622).
 *
 * PR #1709 made a run's steps land in the database while the run was still
 * going, and made the transcript render the whole chain. It left the transport
 * a cursor the browser re-asked every three seconds, so a step the agent took
 * half a second ago still could not appear for three. These cover the stream
 * that replaces that timer.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
	AgentTaskError,
	decodeStreamFrames,
	parseSSEFrames,
	streamTaskEvents,
	type RunStreamUpdate
} from './agentTasks';

const taskRow = {
	id: 'a0f0d4d2-0000-4000-8000-000000000001',
	pack: 'coding-pack',
	instructions: 'Audit the webhook handlers',
	status: 'running',
	engine_session_ref: 'session-1',
	result_summary_ref: '',
	error_message: '',
	created_at: '2026-09-02T10:00:00Z',
	updated_at: '2026-09-02T10:01:00Z',
	started_at: null,
	finished_at: null
};

const frame = (event: string, payload: unknown) =>
	`event: ${event}\ndata: ${JSON.stringify(payload)}\n\n`;

/** A body that yields the given chunks, one read at a time. */
const bodyOf = (chunks: string[]) => {
	const encoder = new TextEncoder();
	let i = 0;
	return {
		getReader: () => ({
			read: async () =>
				i < chunks.length
					? { done: false, value: encoder.encode(chunks[i++]) }
					: { done: true, value: undefined },
			cancel: async () => {}
		})
	};
};

const streamResponse = (chunks: string[]) => ({
	ok: true,
	status: 200,
	body: bodyOf(chunks)
});

describe('parseSSEFrames', () => {
	it('keeps the tail of a frame split across two chunks', () => {
		// The property the whole reader rests on. A chunk ends wherever the
		// network decided, and a parser that consumed its whole input would
		// drop the remainder of every split frame silently, which on this
		// surface is a step that never appears.
		const whole = frame('step', { seq: 1, kind: 'tool_call', payload: {} });
		const cut = Math.floor(whole.length / 2);

		const first = parseSSEFrames(whole.slice(0, cut));
		expect(first.frames).toHaveLength(0);

		const second = parseSSEFrames(first.rest + whole.slice(cut));
		expect(second.frames).toHaveLength(1);
		expect(second.frames[0].event).toBe('step');
		expect(second.rest).toBe('');
	});

	it('produces no frame for a heartbeat', () => {
		// A comment line exists so an idle connection is not closed by
		// something in the middle. Decoding one as an event would put a step
		// on screen that the run never took.
		const parsed = parseSSEFrames(': ping\n\n' + frame('step', { seq: 2 }));
		expect(parsed.frames).toHaveLength(1);
		expect(parsed.frames[0].event).toBe('step');
	});

	it('reads a frame whose lines end with CRLF', () => {
		const parsed = parseSSEFrames('event: end\r\ndata: {"status":"succeeded"}\r\n\r\n');
		expect(parsed.frames).toEqual([{ event: 'end', data: '{"status":"succeeded"}' }]);
	});
});

describe('decodeStreamFrames', () => {
	it('keeps every step in order and the newest status', () => {
		const update = decodeStreamFrames([
			{ event: 'status', data: JSON.stringify({ ...taskRow, status: 'running' }) },
			{ event: 'step', data: JSON.stringify({ seq: 1, kind: 'tool_call', payload: {} }) },
			{ event: 'step', data: JSON.stringify({ seq: 2, kind: 'tool_result', payload: {} }) },
			{ event: 'status', data: JSON.stringify({ ...taskRow, status: 'succeeded' }) }
		]);
		expect(update.events.map((e) => e.seq)).toEqual([1, 2]);
		expect(update.task?.status).toBe('succeeded');
		expect(update.ended).toBe(false);
	});

	it('skips an unreadable frame rather than ending the run', () => {
		const update = decodeStreamFrames([
			{ event: 'step', data: 'not json' },
			{ event: 'step', data: JSON.stringify({ seq: 9, kind: 'message', payload: {} }) }
		]);
		expect(update.events.map((e) => e.seq)).toEqual([9]);
		expect(update.ended).toBe(false);
	});

	it('does not treat a frame kind it cannot name as the end of the run', () => {
		// A newer server adding a frame must not truncate an older client's
		// run, which is the same reason TaskStatus keeps an `unknown` member.
		const update = decodeStreamFrames([{ event: 'thinking', data: '{}' }]);
		expect(update.ended).toBe(false);
	});
});

describe('streamTaskEvents', () => {
	let fetchMock: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('delivers a chunk written after the response began, and stops at the end frame', async () => {
		fetchMock.mockResolvedValue(
			streamResponse([
				frame('status', taskRow),
				frame('step', { seq: 1, kind: 'tool_call', payload: { text_preview: 'Using bash' } }),
				frame('end', { status: 'succeeded' }),
				// Nothing after the end frame may be read: the server has
				// hung up and anything here is a test artefact.
				frame('step', { seq: 99, kind: 'message', payload: {} })
			])
		);

		const updates: RunStreamUpdate[] = [];
		await streamTaskEvents('tok', taskRow.id, 0, (u) => {
			updates.push(u);
		});

		expect(updates).toHaveLength(3);
		expect(updates[0].task?.status).toBe('running');
		expect(updates[1].events[0].seq).toBe(1);
		expect(updates[2].ended).toBe(true);
		expect(updates.flatMap((u) => u.events.map((e) => e.seq))).not.toContain(99);
	});

	it('carries the cursor so a reconnect does not replay what is already rendered', async () => {
		fetchMock.mockResolvedValue(streamResponse([frame('end', { status: 'failed' })]));
		await streamTaskEvents('tok', taskRow.id, 12, () => {});

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toContain('/events/stream?after_seq=12');
		expect(init.headers.Accept).toBe('text/event-stream');
		expect(init.headers.authorization).toBe('Bearer tok');
	});

	it('awaits the handler, so two batches cannot write the turn out of order', async () => {
		// The handler persists the transcript. Firing the next batch without
		// waiting would let a later save land under an earlier one, which on
		// screen is a step chain that goes backwards.
		fetchMock.mockResolvedValue(
			streamResponse([
				frame('step', { seq: 1, kind: 'tool_call', payload: {} }),
				frame('step', { seq: 2, kind: 'tool_result', payload: {} })
			])
		);

		const order: string[] = [];
		await streamTaskEvents('tok', taskRow.id, 0, async (u) => {
			order.push(`start ${u.events[0].seq}`);
			await new Promise((resolve) => setTimeout(resolve, 5));
			order.push(`end ${u.events[0].seq}`);
		});

		expect(order).toEqual(['start 1', 'end 1', 'start 2', 'end 2']);
	});

	it('raises a refusal the caller can tell apart from a blip', async () => {
		fetchMock.mockResolvedValue({
			ok: false,
			status: 403,
			json: async () => ({ error: { message: 'not enabled' } })
		});

		await expect(streamTaskEvents('tok', taskRow.id, 0, () => {})).rejects.toBeInstanceOf(
			AgentTaskError
		);
	});

	it('raises rather than hanging when the response carries no readable body', async () => {
		// Some environments answer 200 with no stream. Resolving quietly would
		// leave the caller believing it is following a run it is not.
		fetchMock.mockResolvedValue({ ok: true, status: 200, body: null });
		await expect(streamTaskEvents('tok', taskRow.id, 0, () => {})).rejects.toThrow();
	});
});

/*
 * Source level guards on the follower.
 *
 * Chat.svelte's imports cannot be resolved in this scratch tree, so there is
 * no way to render it here and the shipped source is what gets pinned. Same
 * pattern as cowork-step-chain.test.ts and chat-noise-guards.test.ts.
 */

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const chat = (): string =>
	readFileSync(fileURLToPath(new URL('../components/chat/Chat.svelte', import.meta.url)), 'utf8');

describe('the transcript follows a run over the stream', () => {
	it('opens the stream rather than only polling', () => {
		const source = chat();
		expect(source).toContain("streamTaskEvents");
		expect(source).toMatch(/await streamTaskEvents\(/);
	});

	it('resumes from the cursor already on the turn', () => {
		// Reconnecting from zero would replay a whole run into a chain that
		// already holds it, which reads as the run starting over.
		expect(chat()).toMatch(/streamTaskEvents\(\s*localStorage\.token,\s*taskId,\s*latestStepSeq\(steps\)/);
	});

	it('falls back to the poll when the stream cannot be opened', () => {
		// The cursor read still works and is still correct. A browser that
		// cannot hold a connection open must see a slower run, never a
		// stranded one, and this is the only thing standing between those two
		// outcomes.
		expect(chat()).toMatch(/await pollCoworkRun\(_chatId, messageId, taskId, deadline\)/);
	});

	it('keeps the read order that closes the #1504 race in the fallback', () => {
		// readCoworkRun reads the status and only then the events, and the
		// comment on it says why. A Promise.all over the two reopens the bug
		// with every server side test still green.
		const source = chat();
		expect(source).not.toMatch(/Promise\.all\(\[\s*getTask\(/);
		expect(source.indexOf('const task = await getTask(')).toBeLessThan(
			source.indexOf('const events = await getTaskEvents(')
		);
	});

	it('hangs up when the conversation moves out from under it', () => {
		// A held connection for a transcript nobody is looking at is a
		// connection per abandoned run, on a server that has to hold one open
		// for every viewer.
		expect(chat()).toMatch(/controller\.abort\(\)/);
	});
});
