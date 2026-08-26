import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import {
	AgentTaskError,
	cancelTask,
	createTask,
	decodeTask,
	describeTask,
	canStartTask,
	describeRefusal,
	decodeEvent,
	ENGINE_UNAVAILABLE_MESSAGE,
	EVENT_PAGE_SIZE,
	getTask,
	getTaskEvents,
	listTasks
} from './agentTasks';

const row = (over: Record<string, unknown> = {}) => ({
	id: 'a0f0d4d2-0000-4000-8000-000000000001',
	pack: 'coding-pack',
	instructions: 'Audit the webhook handlers',
	status: 'running',
	engine_session_ref: '',
	result_summary_ref: '',
	error_message: '',
	created_at: '2026-08-17T10:00:00Z',
	updated_at: '2026-08-17T10:01:00Z',
	started_at: null,
	finished_at: null,
	...over
});

const jsonResponse = (body: unknown, status = 200) =>
	new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});

describe('decodeTask', () => {
	it('keeps a row whose status this build does not recognise', () => {
		// The alternative this replaced was worse than blank: the row was dropped
		// from the list, so a task the user submitted simply vanished.
		const decoded = decodeTask(row({ status: 'reticulating' }));
		expect(decoded?.status).toBe('unknown');
	});

	it('drops a row with no identity', () => {
		expect(decodeTask(row({ id: undefined }))).toBeNull();
		expect(decodeTask(row({ created_at: undefined }))).toBeNull();
		expect(decodeTask(row({ pack: 'no-such-pack' }))).toBeNull();
		expect(decodeTask('not an object')).toBeNull();
	});

	it('defaults the nullable text columns to empty strings', () => {
		const decoded = decodeTask(row({ instructions: undefined, error_message: undefined }));
		expect(decoded?.instructions).toBe('');
		expect(decoded?.error_message).toBe('');
	});
});

describe('describeTask', () => {
	const now = Date.parse('2026-08-17T10:05:00Z');

	it('renders a task with no recorded goal without pretending it has one', () => {
		// Three rows on the deployed list are in exactly this state. They must
		// read as deliberate, not broken.
		const decoded = decodeTask(row({ instructions: '' }));
		expect(decoded?.instructions).toBe('');
		expect(describeTask(decoded!, now).label).toBe('Running');
	});

	it('separates a deployment with no runtime from a task that really failed', () => {
		const blocked = decodeTask(
			row({ status: 'failed', error_message: ENGINE_UNAVAILABLE_MESSAGE })
		)!;
		const failed = decodeTask(row({ status: 'failed', error_message: 'its session was lost' }))!;

		expect(describeTask(blocked, now).label).toBe('Blocked');
		expect(describeTask(blocked, now).tone).toBe('warning');
		expect(describeTask(failed, now).label).toBe('Failed');
		// The #921 row. Its own sentence survives rather than being replaced by a
		// generic one, because the sentence is the only evidence the user gets.
		expect(describeTask(failed, now).detail).toBe('its session was lost');
	});

	it('says plainly when a queued task has been sitting there', () => {
		const stale = decodeTask(row({ status: 'queued', created_at: '2026-08-17T09:00:00Z' }))!;
		expect(describeTask(stale, now).detail).toContain('nothing picking it up');
	});
});

describe('the four calls', () => {
	let fetchMock: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('lists through Open WebUI, never through edge-api directly', async () => {
		// Load bearing. The browser has no credential edge-api accepts, so a call
		// that went straight there would 401 for every signed-in user.
		fetchMock.mockResolvedValue(jsonResponse({ tasks: [row()] }));

		const tasks = await listTasks('owui-session-token');

		expect(tasks).toHaveLength(1);
		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/v1/hive/agent/tasks');
		expect(init.method).toBe('GET');
		expect(init.headers.authorization).toBe('Bearer owui-session-token');
	});

	it('sends the pack and the goal, and nothing else', async () => {
		fetchMock.mockResolvedValue(jsonResponse(row()));

		await createTask('owui-session-token', 'knowledge-work-pack', 'Summarise the policy');

		const [url, init] = fetchMock.mock.calls[0];
		expect(url).toBe('/api/v1/hive/agent/tasks');
		expect(init.method).toBe('POST');
		expect(JSON.parse(init.body)).toEqual({
			pack: 'knowledge-work-pack',
			instructions: 'Summarise the policy'
		});
	});

	it('escapes the task id into the cancel path', async () => {
		fetchMock.mockResolvedValue(jsonResponse(row({ status: 'cancelled' })));

		await cancelTask('owui-session-token', 'a b/../c');

		expect(fetchMock.mock.calls[0][0]).toBe('/api/v1/hive/agent/tasks/a%20b%2F..%2Fc/cancel');
	});

	it('surfaces the server sentence from either error vocabulary', async () => {
		fetchMock.mockResolvedValue(
			jsonResponse({ error: { message: 'Cowork is not enabled for this tenant.' } }, 403)
		);
		await expect(listTasks('t')).rejects.toThrow('Cowork is not enabled for this tenant.');

		fetchMock.mockResolvedValue(
			jsonResponse({ detail: 'Your Hive sign-in could not be confirmed.' }, 401)
		);
		// The sentence, not only the type: extraction regressing to the generic
		// fallback would still throw the right class with the wrong text.
		await expect(listTasks('t')).rejects.toThrow('Your Hive sign-in could not be confirmed.');
		await expect(listTasks('t')).rejects.toBeInstanceOf(AgentTaskError);
	});

	it('separates a refusal from a failure worth retrying', () => {
		// The list stops polling on a refusal and keeps polling on anything else,
		// so this decides whether the screen promises a retry it cannot deliver.
		expect(describeRefusal(new AgentTaskError(401, 'access denied'))?.kind).toBe('signin');
		expect(describeRefusal(new AgentTaskError(403, 'access denied'))?.kind).toBe('not-enabled');
		expect(describeRefusal(new AgentTaskError(500, 'boom'))).toBeNull();
		expect(describeRefusal(new AgentTaskError(429, 'slow down'))).toBeNull();
		expect(describeRefusal(new Error('network'))).toBeNull();
		expect(describeRefusal(null)).toBeNull();
	});

	it('replaces the raw server sentence with copy a person can act on', () => {
		// edge-api's own words for a closed gate are accurate and useless: they
		// say access was denied without saying who can change it.
		const gated = describeRefusal(new AgentTaskError(403, 'access denied'))!;
		expect(gated.message).not.toContain('access denied');
		expect(gated.message).toContain('administrator');
	});

	it('reads a null tasks array as an empty list, because Go marshals nil that way', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ tasks: null }));
		await expect(listTasks('t')).resolves.toEqual([]);
	});

	it('refuses to report an unreadable payload as an empty list', async () => {
		// Saying "no tasks" when the truth is "could not read the answer" is the
		// exact failure this surface exists to stop making.
		for (const body of [{}, { tasks: 'nope' }, { tasks: { a: 1 } }]) {
			fetchMock.mockResolvedValue(jsonResponse(body));
			await expect(listTasks('t')).rejects.toThrow('Failed to parse tasks response');
		}
	});
});

describe('canStartTask', () => {
	const base = { instructions: 'do a thing', submitting: false, blocked: null };

	it('refuses to submit into a surface that is refused', () => {
		// The rough edge this closes: a tenant without the feature could type a
		// brief and press send into a surface that could only answer no.
		expect(canStartTask(base)).toBe(true);
		expect(
			canStartTask({ ...base, blocked: { kind: 'not-enabled', message: 'x' } })
		).toBe(false);
		expect(canStartTask({ ...base, blocked: { kind: 'signin', message: 'x' } })).toBe(false);
	});

	it('still refuses an empty brief and a submission already in flight', () => {
		expect(canStartTask({ ...base, instructions: '   ' })).toBe(false);
		expect(canStartTask({ ...base, submitting: true })).toBe(false);
	});
});

const eventRow = (over: Record<string, unknown> = {}) => ({
	seq: 7,
	source_event_id: 'evt-7',
	kind: 'tool_call',
	payload: { tool_name: 'bash', tool_call_id: 'call-1', preview: 'ls -la' },
	created_at: '2026-08-25T10:00:03Z',
	...over
});

describe('decodeEvent', () => {
	it('keeps a kind this build does not recognise', () => {
		// The backend deliberately refuses to drop an unmapped upstream event
		// class (it lands as `status` carrying the raw payload). Dropping it here
		// would undo that at the last hop.
		expect(decodeEvent(eventRow({ kind: 'reticulating' }))?.kind).toBe('unknown');
	});

	it('drops a row with no usable cursor position', () => {
		// seq is the cursor. A row that cannot be acknowledged would be re-read
		// on every poll forever.
		expect(decodeEvent(eventRow({ seq: undefined }))).toBeNull();
		expect(decodeEvent(eventRow({ seq: '7' }))).toBeNull();
		expect(decodeEvent(eventRow({ seq: -1 }))).toBeNull();
		expect(decodeEvent('not an object')).toBeNull();
	});

	it('keeps the event when the payload is not an object', () => {
		const decoded = decodeEvent(eventRow({ payload: 'raw text' }));
		expect(decoded?.payload).toEqual({});
		expect(decoded?.kind).toBe('tool_call');
	});
});

describe('the by-id reads', () => {
	let fetchMock: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		fetchMock = vi.fn();
		vi.stubGlobal('fetch', fetchMock);
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('reads one task by id instead of filtering the whole list', async () => {
		fetchMock.mockResolvedValue(jsonResponse(row()));

		const task = await getTask('owui-session-token', 'a0f0d4d2-0000-4000-8000-000000000001');

		expect(task.id).toBe('a0f0d4d2-0000-4000-8000-000000000001');
		expect(fetchMock.mock.calls[0][0]).toBe(
			'/api/v1/hive/agent/tasks/a0f0d4d2-0000-4000-8000-000000000001'
		);
	});

	it('reports a refused task read rather than returning nothing', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ error: { message: 'task not found' } }, 404));

		await expect(getTask('t', 'a0f0d4d2-0000-4000-8000-000000000001')).rejects.toBeInstanceOf(
			AgentTaskError
		);
	});

	it('sends the cursor, so a follower asks only for what it has not seen', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ events: [eventRow()] }));

		const events = await getTaskEvents('t', 'a0f0d4d2-0000-4000-8000-000000000001', 12);

		expect(events).toHaveLength(1);
		expect(fetchMock.mock.calls[0][0]).toBe(
			`/api/v1/hive/agent/tasks/a0f0d4d2-0000-4000-8000-000000000001/events?after_seq=12&limit=${EVENT_PAGE_SIZE}`
		);
	});

	it('floors the cursor to what the proxy accepts, which is a plain integer', async () => {
		// hive_agent_proxy.py answers anything else with a 400 before it reaches
		// a URL, so sending it costs a round trip and returns no events.
		fetchMock.mockResolvedValue(jsonResponse({ events: [] }));

		await getTaskEvents('t', 'a0f0d4d2-0000-4000-8000-000000000001', -5, 3.7);

		expect(fetchMock.mock.calls[0][0]).toContain('after_seq=0&limit=3');
	});

	it('reads an empty feed as no progress yet, and an unreadable one as a failure', async () => {
		fetchMock.mockResolvedValue(jsonResponse({ events: [] }));
		await expect(getTaskEvents('t', 'a0f0d4d2-0000-4000-8000-000000000001')).resolves.toEqual([]);

		fetchMock.mockResolvedValue(jsonResponse({ events: null }));
		await expect(getTaskEvents('t', 'a0f0d4d2-0000-4000-8000-000000000001')).resolves.toEqual([]);

		// No `events` key at all is a payload we could not read. Returning [] here
		// would report "nothing has happened" for a broken response.
		fetchMock.mockResolvedValue(jsonResponse({ tasks: [] }));
		await expect(getTaskEvents('t', 'a0f0d4d2-0000-4000-8000-000000000001')).rejects.toThrow(
			'Failed to parse task events response'
		);
	});
});
