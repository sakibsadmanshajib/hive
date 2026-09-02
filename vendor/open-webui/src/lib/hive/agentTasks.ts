/*
 * The agent-task lifecycle, from the chat frontend.
 *
 * Hive authored. Everything under src/lib/hive/ is ours, so a rebase against a
 * future upstream tag reads as a file list rather than an archaeology exercise.
 *
 * These three calls go to Open WebUI's own backend, not to edge-api, and that
 * is the whole reason the agent surface can be native here at all. The browser
 * has no credential edge-api accepts: it holds Open WebUI's session token,
 * which edge-api has never heard of, and the Supabase OAuth-server token it
 * would need is not in the browser at all. The server-side proxy
 * (deploy/docker/owui-patches/hive_agent_proxy.py) resolves that token per
 * request and forwards to /v1/agent/* on the same mechanism this deployment
 * already runs for chat completions.
 *
 * The wire shape below is edge-api's, unchanged, because the proxy returns its
 * JSON verbatim. Ported from apps/agent-console/lib/edge-api/tasks.ts,
 * including the decision in it that is easiest to mistake for an accident and
 * is not: the `unknown` status.
 *
 * WHAT THIS MODULE STOPPED CARRYING, AND WHY (issue #1501)
 * -------------------------------------------------------
 * It used to serve two callers: the chat composer's Cowork mode, and the
 * `/agents` page, which was a second agent destination with its own submit
 * form, pack selector and task list. D-045 rules that the agent surface is a
 * mode of the composer rather than a place you go, so that page is retired and
 * everything only it used went with it rather than being left unreachable:
 * `listTasks`, `cancelTask`, `canStartTask`, `describeTask` and its `TaskView`,
 * `elapsed`, `IN_FLIGHT_STATUSES`, and the two engine sentinels with their
 * `isEngineUnavailable` and `isEngineLaunchFailure` predicates.
 *
 * Two of those are worth naming so nobody restores them as scaffolding. A run
 * IS a conversation now, so the conversation list is the task list and nothing
 * needs `listTasks`. And the engine sentinels existed to give a deployment
 * misconfiguration its own "Blocked" label in a status column that no longer
 * exists; `describeRefusal` still carries the two refusals a person can act on.
 */

// The base is a parameter with a production default rather than an import of
// `$lib/constants`, and that is load-bearing rather than stylistic.
// `$lib/constants` reaches `$app/environment` for `browser` and `dev`, which
// only SvelteKit's resolver can satisfy. scripts/test-owui-hive-frontend.sh,
// the one runner that covers this front end, copies lib/hive/*.ts into a
// scratch directory and runs plain vitest there with no alias resolution at
// all, so while this module imported that path its own 203 line test file
// could not be loaded: it reported no failures because it never ran, and it
// would have turned that required check red the moment the runner reached it.
//
// The default is what `${WEBUI_API_BASE_URL}/hive/agent` evaluates to in every
// built bundle, since `WEBUI_BASE_URL` is the empty string outside dev. The
// caller passes the dev-aware value, so `npm run dev` against the chat front
// end still reaches the backend on port 8080 exactly as before.
export const DEFAULT_AGENT_API_BASE_URL = '/api/v1/hive/agent';

export type TaskPack = 'coding-pack' | 'knowledge-work-pack';

/*
 * The five wire statuses, plus a local sixth.
 *
 * `unknown` is never sent by the server. It is what any status this build does
 * not recognise decodes to, so that a row still reaches the list. A console
 * that filtered the row out would make a task the user submitted vanish with
 * no error, which reads as deletion.
 */
export type TaskStatus =
	| 'queued'
	| 'running'
	| 'succeeded'
	| 'failed'
	| 'cancelled'
	| 'unknown';

export interface AgentTask {
	id: string;
	pack: TaskPack;
	instructions: string;
	status: TaskStatus;
	engine_session_ref: string;
	result_summary_ref: string;
	error_message: string;
	created_at: string;
	updated_at: string;
	started_at: string | null;
	finished_at: string | null;
}

export class AgentTaskError extends Error {
	public readonly status: number;
	constructor(status: number, message: string) {
		super(message);
		this.name = 'AgentTaskError';
		this.status = status;
	}
}

const PACKS: ReadonlySet<string> = new Set(['coding-pack', 'knowledge-work-pack']);
const STATUSES: ReadonlySet<string> = new Set([
	'queued',
	'running',
	'succeeded',
	'failed',
	'cancelled'
]);

export const isTaskPack = (value: unknown): value is TaskPack =>
	typeof value === 'string' && PACKS.has(value);

const isWireStatus = (value: unknown): value is TaskStatus =>
	typeof value === 'string' && STATUSES.has(value);

const readString = (value: Record<string, unknown>, key: string): string | null => {
	const raw = value[key];
	return typeof raw === 'string' ? raw : null;
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
	typeof value === 'object' && value !== null && !Array.isArray(value);

/**
 * Decodes one wire row, or null when it carries no identity.
 *
 * Identity fields only. A row without one of these is a malformed payload
 * rather than a task in a state we have not met, and there is nothing honest to
 * render for it. The status deliberately is not in this guard: see TaskStatus.
 */
export const decodeTask = (value: unknown): AgentTask | null => {
	if (!isRecord(value)) {
		return null;
	}
	const id = readString(value, 'id');
	const pack = readString(value, 'pack');
	const createdAt = readString(value, 'created_at');
	const updatedAt = readString(value, 'updated_at');

	if (!id || !isTaskPack(pack) || !createdAt || !updatedAt) {
		return null;
	}

	const status = readString(value, 'status');

	return {
		id,
		pack,
		// Nullable column server side; the contract promises '' rather than null
		// on read, but decode defensively so an older row cannot crash the list.
		instructions: readString(value, 'instructions') ?? '',
		status: isWireStatus(status) ? status : 'unknown',
		engine_session_ref: readString(value, 'engine_session_ref') ?? '',
		result_summary_ref: readString(value, 'result_summary_ref') ?? '',
		error_message: readString(value, 'error_message') ?? '',
		created_at: createdAt,
		updated_at: updatedAt,
		started_at: readString(value, 'started_at'),
		finished_at: readString(value, 'finished_at')
	};
};

const headers = (token: string): Record<string, string> => ({
	Accept: 'application/json',
	'Content-Type': 'application/json',
	authorization: `Bearer ${token}`
});

const readBody = async (response: Response): Promise<unknown> => {
	try {
		return await response.json();
	} catch {
		return null;
	}
};

/**
 * Turns a non-2xx into an AgentTaskError carrying the server's own sentence.
 *
 * Two error vocabularies reach here: edge-api's `{error: {message}}`, returned
 * verbatim by the proxy, and FastAPI's `{detail}` for the proxy's own
 * refusals. Both are already written for a customer to read and neither names
 * a provider, so both are surfaced rather than replaced with a generic line
 * that would throw away the only useful thing in the response.
 */
const raise = async (response: Response, fallback: string): Promise<never> => {
	const body = await readBody(response);
	let message: string | null = null;
	if (isRecord(body)) {
		const error = body['error'];
		if (isRecord(error)) {
			message = readString(error, 'message');
		}
		if (!message) {
			message = readString(body, 'detail');
		}
	}
	throw new AgentTaskError(response.status, message ?? `${fallback}: ${response.status}`);
};

export interface Refusal {
	kind: 'signin' | 'not-enabled';
	message: string;
}

/**
 * A failure that another attempt cannot change, in words a person can act on.
 *
 * 401 means this session cannot reach the agent service at all; 403 means the
 * feature is not enabled for this organization. Everything else, a network
 * blip, a 5xx, an unreadable payload, is worth retrying and is not a refusal.
 * The two must not share copy: the retry message promises something a refusal
 * can never deliver, and a poll that keeps asking a settled question is a
 * request every few seconds forever.
 *
 * The copy is ours, not the server's. edge-api's own sentence for a closed
 * feature gate is accurate and useless to the person reading it: it says access
 * was denied without saying who can change that or what is safe to do
 * meanwhile. This is the same register the surface being replaced used for a
 * deployment with no agent runtime, and for the same reason.
 *
 * A function rather than an inline check in the component, so the decision has
 * a test; the component itself has no test harness in this tree.
 */
export const describeRefusal = (error: unknown): Refusal | null => {
	if (!(error instanceof AgentTaskError)) {
		return null;
	}
	if (error.status === 401) {
		return {
			kind: 'signin',
			message:
				'Your sign-in could not be confirmed for the agent service, so tasks cannot be listed or started. Sign in again, and if it keeps happening contact your administrator.'
		};
	}
	if (error.status === 403) {
		return {
			kind: 'not-enabled',
			message:
				'The agent service is not enabled for this organization, so there are no tasks to show and none can be started. An administrator can turn it on. Nothing is wrong with your account, and the rest of Hive is unaffected.'
		};
	}
	return null;
};

export const createTask = async (
	token: string,
	pack: TaskPack,
	instructions: string,
	apiBase: string = DEFAULT_AGENT_API_BASE_URL
): Promise<AgentTask> => {
	const response = await fetch(`${apiBase}/tasks`, {
		method: 'POST',
		headers: headers(token),
		body: JSON.stringify({ pack, instructions })
	});
	if (!response.ok) {
		await raise(response, 'Failed to create task');
	}
	const decoded = decodeTask(await readBody(response));
	if (!decoded) {
		throw new Error('Failed to parse task response');
	}
	return decoded;
};

/**
 * One task, read by id.
 *
 * The list read this replaced returned every task the user owns in order to
 * find one of them, which is the wrong request at any volume and was only ever
 * written because this endpoint did not exist yet. It does: control-plane
 * serves it, edge-api exposes it as GET /v1/agent/tasks/{id}, and the proxy
 * routes it (deploy/docker/owui-patches/hive_agent_proxy.py, `get_task`).
 *
 * A task that is not this user's is a 404 here, not a filtered-out row, so a
 * caller gets an AgentTaskError rather than a silent "not found in the list".
 */
export const getTask = async (
	token: string,
	id: string,
	apiBase: string = DEFAULT_AGENT_API_BASE_URL
): Promise<AgentTask> => {
	const response = await fetch(`${apiBase}/tasks/${encodeURIComponent(id)}`, {
		method: 'GET',
		headers: headers(token)
	});
	if (!response.ok) {
		await raise(response, 'Failed to load task');
	}
	const decoded = decodeTask(await readBody(response));
	if (!decoded) {
		throw new Error('Failed to parse task response');
	}
	return decoded;
};

/*
 * The per-step event feed.
 *
 * The six kinds are the CHECK constraint on public.agent_task_events, mirrored
 * in apps/control-plane/internal/agenttask/events.go. `unknown` is the same
 * local seventh that TaskStatus carries and for the same reason: a kind this
 * build cannot name still reaches the caller, which can say so, rather than
 * being dropped on the floor. The backend itself already refuses to drop an
 * unmapped OpenHands class (it lands as `status` carrying the raw payload), so
 * discarding it here would undo that deliberately.
 */
export type TaskEventKind =
	| 'status'
	| 'tool_call'
	| 'tool_result'
	| 'message'
	| 'error'
	| 'file'
	| 'unknown';

const EVENT_KINDS: ReadonlySet<string> = new Set([
	'status',
	'tool_call',
	'tool_result',
	'message',
	'error',
	'file'
]);

export interface TaskEvent {
	seq: number;
	kind: TaskEventKind;
	/**
	 * Whatever JSONB the syncer stored. Every layer between here and the
	 * database passes it through without parsing it, so this is the first place
	 * that reads inside it, and it reads defensively: see runSteps() in
	 * coworkMode.ts for the per-kind shapes and what happens to a payload that
	 * does not match one.
	 */
	payload: Record<string, unknown>;
	created_at: string;
}

/**
 * Decodes one event row, or null when it carries no cursor position.
 *
 * `seq` is the only truly load-bearing field: it is the cursor, and a row
 * without a usable one cannot be acknowledged, so accepting it would mean
 * re-reading it forever. A non-object payload (nothing this backend writes
 * today, but the column is raw JSONB) degrades to an empty object rather than
 * discarding the event, because the kind alone is still worth something.
 */
export const decodeEvent = (value: unknown): TaskEvent | null => {
	if (!isRecord(value)) {
		return null;
	}
	const seq = value['seq'];
	if (typeof seq !== 'number' || !Number.isFinite(seq) || seq < 0) {
		return null;
	}
	const kind = readString(value, 'kind');
	const payload = value['payload'];
	return {
		seq,
		kind: kind !== null && EVENT_KINDS.has(kind) ? (kind as TaskEventKind) : 'unknown',
		payload: isRecord(payload) ? payload : {},
		created_at: readString(value, 'created_at') ?? ''
	};
};

/*
 * The page size this front end asks for.
 *
 * edge-api clamps `limit` to 500 (maxEventsLimit in
 * apps/edge-api/internal/agenttask/handler.go) and defaults it to 100. 200 is
 * a follower's page, not a backlog dump: a live run produces a handful of
 * events between polls, and the only time a full page comes back is the first
 * read of a conversation reopened after a long run, which pages.
 */
export const EVENT_PAGE_SIZE = 200;

/**
 * Events strictly newer than `afterSeq`, oldest first.
 *
 * The cursor is the whole point. A follower that re-read the full event list
 * every few seconds would repeat the mistake this call was added to fix, one
 * layer down: control-plane's read is `seq > $2 ORDER BY seq ASC LIMIT $4`
 * (repository.go), so passing the highest seq already seen costs one small
 * page per poll no matter how long the run has been going.
 *
 * A returned page of exactly `limit` rows means there may be more behind it.
 * The caller advances the cursor and asks again rather than assuming the run
 * has produced nothing since.
 */
export const getTaskEvents = async (
	token: string,
	id: string,
	afterSeq: number = 0,
	limit: number = EVENT_PAGE_SIZE,
	apiBase: string = DEFAULT_AGENT_API_BASE_URL
): Promise<TaskEvent[]> => {
	// The proxy validates both as plain non-negative integers before they reach
	// a URL and answers anything else with a 400, so they are floored to that
	// shape here rather than sent as-is and refused a round trip later.
	const cursor = Math.max(0, Math.floor(afterSeq));
	const page = Math.max(1, Math.floor(limit));
	const response = await fetch(
		`${apiBase}/tasks/${encodeURIComponent(id)}/events?after_seq=${cursor}&limit=${page}`,
		{
			method: 'GET',
			headers: headers(token)
		}
	);
	if (!response.ok) {
		await raise(response, 'Failed to load task events');
	}
	const body = await readBody(response);
	if (!isRecord(body) || !('events' in body)) {
		// A missing key is a payload we could not read, and reporting it as
		// "no progress yet" would be the exact silent failure this surface
		// exists to stop. (listTasks drew the same distinction until issue
		// #1501 removed it with the /agents page, its only caller.)
		throw new Error('Failed to parse task events response');
	}
	const rows = body['events'];
	if (rows === null) {
		return [];
	}
	if (!Array.isArray(rows)) {
		throw new Error('Failed to parse task events response');
	}
	const events: TaskEvent[] = [];
	for (const row of rows) {
		const decoded = decodeEvent(row);
		if (decoded) {
			events.push(decoded);
		}
	}
	return events;
};

// Polling and the cancel button both stop once a task reaches one of these
// (matches apps/control-plane/internal/agenttask/SYNC_CONTRACT.md's state
// machine). `unknown` is deliberately absent: a state we cannot name is not a
// state we can call finished.
export const TERMINAL_STATUSES: ReadonlySet<TaskStatus> = new Set([
	'succeeded',
	'failed',
	'cancelled'
]);
