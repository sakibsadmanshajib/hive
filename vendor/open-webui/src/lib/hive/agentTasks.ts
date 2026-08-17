/*
 * The agent-task lifecycle, from the chat frontend.
 *
 * Hive authored. Everything under src/lib/hive/ is ours, so a rebase against a
 * future upstream tag reads as a file list rather than an archaeology exercise.
 *
 * These four calls go to Open WebUI's own backend, not to edge-api, and that
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
 * including the two decisions in it that are easy to mistake for accidents and
 * are not: the `unknown` status and the two engine sentinels.
 */

import { WEBUI_API_BASE_URL } from '$lib/constants';

const AGENT_API_BASE_URL = `${WEBUI_API_BASE_URL}/hive/agent`;

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

/**
 * Whether a failure is a refusal that another attempt cannot change.
 *
 * 401 means this session cannot reach the agent service at all; 403 means the
 * tenant does not hold the Cowork gate. Everything else, a network blip, a 5xx,
 * an unreadable payload, is worth retrying. The two must not share copy,
 * because the retry message promises something a refusal can never deliver,
 * and a poll that keeps asking a settled question is a request every few
 * seconds forever.
 *
 * A function rather than an inline check in the component, so the decision has
 * a test. The component itself has no test harness in this tree.
 */
export const isRefusal = (error: unknown): error is AgentTaskError =>
	error instanceof AgentTaskError && (error.status === 401 || error.status === 403);

export const listTasks = async (token: string): Promise<AgentTask[]> => {
	const response = await fetch(`${AGENT_API_BASE_URL}/tasks`, {
		method: 'GET',
		headers: headers(token)
	});
	if (!response.ok) {
		await raise(response, 'Failed to load tasks');
	}
	const body = await readBody(response);
	if (!isRecord(body)) {
		throw new Error('Failed to parse tasks response');
	}
	/*
	 * `tasks` absent is a payload we cannot read; `tasks: null` is a real empty
	 * list, because edge-api answers `map[string]any{"tasks": tasks}` and Go
	 * marshals a nil slice as null (apps/edge-api/internal/agenttask/handler.go).
	 * Returning an empty list for the first case would tell the user they have
	 * no tasks when the truth is that we could not read the answer, which is the
	 * failure mode this whole surface exists to stop making.
	 */
	if (!('tasks' in body)) {
		throw new Error('Failed to parse tasks response');
	}
	const rows = body['tasks'];
	if (rows === null) {
		return [];
	}
	if (!Array.isArray(rows)) {
		throw new Error('Failed to parse tasks response');
	}
	const tasks: AgentTask[] = [];
	for (const row of rows) {
		const decoded = decodeTask(row);
		if (decoded) {
			tasks.push(decoded);
		}
	}
	return tasks;
};

export const createTask = async (
	token: string,
	pack: TaskPack,
	instructions: string
): Promise<AgentTask> => {
	const response = await fetch(`${AGENT_API_BASE_URL}/tasks`, {
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

export const cancelTask = async (token: string, id: string): Promise<AgentTask> => {
	const response = await fetch(
		`${AGENT_API_BASE_URL}/tasks/${encodeURIComponent(id)}/cancel`,
		{
			method: 'POST',
			headers: headers(token)
		}
	);
	if (!response.ok) {
		await raise(response, 'Failed to cancel task');
	}
	const decoded = decodeTask(await readBody(response));
	if (!decoded) {
		throw new Error('Failed to parse task response');
	}
	return decoded;
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

// The states the server can move a task out of on its own, and therefore the
// only reason to keep polling. Deliberately not the complement of
// TERMINAL_STATUSES: `unknown` is neither finished nor known to be moving, so
// it keeps its row but does not hold the poll timer open forever on a guess.
export const IN_FLIGHT_STATUSES: ReadonlySet<TaskStatus> = new Set(['queued', 'running']);

/*
 * Deployment-configuration sentinels.
 *
 * Both strings are consts in apps/control-plane/internal/agenttask/service.go
 * (`engineUnavailableMessage`, `engineLaunchFailedMessage`), persisted verbatim
 * into `error_message` when Engine.Launch fails. They are deliberately
 * provider-blind and generic, which also makes them stable enough to key
 * presentation off: a task carrying one of them did not fail because of
 * anything the user wrote, it never started at all.
 *
 * A drifted string degrades to the plain "failed" treatment, which is still
 * honest, so this is a soft dependency rather than a coupling that can break.
 */
export const ENGINE_UNAVAILABLE_MESSAGE = 'agent engine is not available on this deployment';
export const ENGINE_LAUNCH_FAILED_MESSAGE = 'agent engine could not start the task';

export const isEngineUnavailable = (task: AgentTask): boolean =>
	task.status === 'failed' && task.error_message === ENGINE_UNAVAILABLE_MESSAGE;

export const isEngineLaunchFailure = (task: AgentTask): boolean =>
	task.status === 'failed' && task.error_message === ENGINE_LAUNCH_FAILED_MESSAGE;

export interface TaskView {
	label: string;
	detail: string;
	/** Running is the only state whose dot is filled and live. */
	live: boolean;
	tone: 'neutral' | 'accent' | 'success' | 'warning' | 'danger';
}

// A queued task only moves when something on the far side of the engine seam
// picks it up. Past this threshold the row says plainly that it is not
// progressing rather than implying imminent work.
const STALE_QUEUE_AFTER_MS = 10 * 60 * 1000;

/** Coarse elapsed label: "4m", "3h", "6d". */
export const elapsed = (iso: string, nowMs: number): string => {
	const then = Date.parse(iso);
	if (Number.isNaN(then)) {
		return '';
	}
	const minutes = Math.floor(Math.max(0, nowMs - then) / 60_000);
	if (minutes < 1) return 'less than a minute';
	if (minutes < 60) return `${minutes}m`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h`;
	return `${Math.floor(hours / 24)}d`;
};

/**
 * Maps a wire task onto what a person should read.
 *
 * The two engine sentinels are the reason this function exists. Both are
 * deployment configuration, not user error and not really a task failure:
 * nothing ran, nothing was wrong with the request. They get their own
 * "Blocked" label in the warning register so they never sit next to a genuine
 * failure wearing the same red.
 */
export const describeTask = (task: AgentTask, nowMs: number): TaskView => {
	switch (task.status) {
		case 'queued': {
			const createdMs = Date.parse(task.created_at);
			const stale = !Number.isNaN(createdMs) && nowMs - createdMs > STALE_QUEUE_AFTER_MS;
			return {
				label: 'Queued',
				tone: 'neutral',
				live: false,
				detail: stale
					? `Queued for ${elapsed(task.created_at, nowMs)} with nothing picking it up. Cancel it to clear it from the list.`
					: 'Waiting for a sandbox to pick it up.'
			};
		}
		case 'running':
			return {
				label: 'Running',
				tone: 'accent',
				live: true,
				detail:
					'Working now. There is no live transcript yet, so this view checks for a result every few seconds.'
			};
		case 'succeeded':
			return {
				label: 'Done',
				tone: 'success',
				live: false,
				detail: task.result_summary_ref ? 'Finished. The result reference is below.' : 'Finished.'
			};
		case 'cancelled':
			return {
				label: 'Cancelled',
				tone: 'neutral',
				live: false,
				detail: 'You stopped this task before it finished.'
			};
		case 'unknown':
			return {
				label: 'Unknown',
				tone: 'neutral',
				live: false,
				detail:
					'This task is in a state this page does not recognise, so there is nothing reliable to say about it yet. It is still recorded against your account. Reload to check it again.'
			};
		case 'failed':
		default: {
			if (isEngineUnavailable(task)) {
				return {
					label: 'Blocked',
					tone: 'warning',
					live: false,
					detail:
						'This deployment has no agent runtime configured, so the task was recorded but never started. Nothing was wrong with what you asked for.'
				};
			}
			if (isEngineLaunchFailure(task)) {
				return {
					label: 'Blocked',
					tone: 'warning',
					live: false,
					detail:
						'The agent runtime refused to start this task, so nothing ran. Trying again may work; if it keeps happening it is a deployment problem, not your task.'
				};
			}
			return {
				label: 'Failed',
				tone: 'danger',
				live: false,
				detail: task.error_message || 'The task stopped before producing a result.'
			};
		}
	}
};
