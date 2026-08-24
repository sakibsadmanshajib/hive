/*
 * The Projects destination's data layer, from the chat frontend.
 *
 * Hive authored. Everything under src/lib/hive/ is ours, so a rebase against a
 * future upstream tag reads as a file like this one rather than an archaeology
 * exercise.
 *
 * Storage binding (the seam, stated plainly): a Project IS an Open WebUI
 * knowledge collection on the pinned backend image. name/description come from
 * the collection row; the files bound to the project are the files attached to
 * that collection, which is exactly where RAG retrieval looks; and the
 * conversations bound to a project live in each chat's own persisted JSON blob
 * under the `hiveProject` key, written through the existing chat-update merge.
 * No new table, no backend edit, no owui-patches rewrite tonight. If a real
 * projects table ever lands backend side, this module is the only file that
 * has to change: every call below is one fetch against /api/v1 and the rest of
 * the surface renders from these functions' return values.
 *
 * Like agentTasks.ts before it, the base URL is a parameter with a production
 * default rather than an import of `$lib/constants`: `$lib/constants` reaches
 * `$app/environment`, which only SvelteKit's resolver can satisfy, while
 * scripts/test-owui-hive-frontend.sh runs plain vitest in a scratch tree with
 * no alias resolution at all. A `$lib` import here would make this module's
 * own test file unloadable, which reports as no failures because nothing ran.
 */

export interface HiveProject {
	id: string;
	name: string;
	description: string;
	/** Epoch seconds, straight off the knowledge row. */
	updatedAt: number;
}

export interface HiveProjectFile {
	id: string;
	name: string;
}

export interface HiveProjectConversation {
	id: string;
	title: string;
	updatedAt: number;
	hiveProjectId: string;
}

export class ProjectError extends Error {
	status?: number;

	constructor(message: string, status?: number) {
		super(message);
		this.name = 'ProjectError';
		this.status = status;
	}
}

const DEFAULT_API_BASE = '/api/v1';

/**
 * The marker key written into each bound chat's JSON blob. Namespaced so it
 * can never collide with anything upstream puts in the same object.
 */
export const PROJECT_CHAT_KEY = 'hiveProject';

const headers = (token: string): Record<string, string> => ({
	Accept: 'application/json',
	'Content-Type': 'application/json',
	authorization: `Bearer ${token}`
});

async function requestJson<T>(
	url: string,
	init: RequestInit,
	apiBase: string,
	fetchImpl: typeof fetch
): Promise<T> {
	let res: Response;
	try {
		res = await fetchImpl(`${apiBase}${url}`, init);
	} catch (err) {
		throw new ProjectError(err instanceof Error ? err.message : 'Network error');
	}
	if (!res.ok) {
		let detail = res.statusText;
		try {
			const body = await res.json();
			detail = body?.detail ?? detail;
		} catch {
			// Non-JSON error body: fall back to the status text.
		}
		throw new ProjectError(typeof detail === 'string' ? detail : 'Request failed', res.status);
	}
	return (await res.json()) as T;
}

/* ------------------------------------------------------------------ *
 * Projects (knowledge collections, re-skinned)
 * ------------------------------------------------------------------ */

interface RawKnowledgeRow {
	id: string;
	name: string;
	description: string;
	updated_at: number;
	meta?: Record<string, unknown> | null;
}

function isProjectRow(row: RawKnowledgeRow): boolean {
	// External connections ride the same table with meta.source === 'external';
	// those are not projects and must never appear in the list.
	return (row.meta ?? {})['source'] !== 'external';
}

const toProject = (row: RawKnowledgeRow): HiveProject => ({
	id: row.id,
	name: row.name,
	description: row.description,
	updatedAt: Number(row.updated_at ?? 0)
});

export const listProjects = async (
	token: string,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<HiveProject[]> => {
	const data = await requestJson<{ items?: RawKnowledgeRow[] } | RawKnowledgeRow[]>(
		'/knowledge/',
		{ method: 'GET', headers: headers(token) },
		apiBase,
		fetchImpl
	);
	const rows = Array.isArray(data) ? data : (data.items ?? []);
	return rows.filter(isProjectRow).map(toProject);
};

export const getProject = async (
	token: string,
	id: string,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<(HiveProject & { files: HiveProjectFile[]; writeAccess: boolean }) | null> => {
	try {
		const row = await requestJson<
			RawKnowledgeRow & {
				files?: Array<Record<string, unknown>> | null;
				write_access?: boolean;
			}
		>(
			`/knowledge/${id}`,
			{ method: 'GET', headers: headers(token) },
			apiBase,
			fetchImpl
		);
		// The pinned image's detail response leaves `files` null even when the
		// collection has them; the per-collection listing endpoint is where the
		// real rows live.
		const fileList = await requestJson<{
			items?: Array<{ id: string; meta?: { name?: string } | null; filename?: string | null }>;
		}>(
			`/knowledge/${id}/files?page=1`,
			{ method: 'GET', headers: headers(token) },
			apiBase,
			fetchImpl
		);
		return {
			...toProject(row),
			files: (fileList.items ?? []).map((f) => ({
				id: String(f.id),
				name: String(f.meta?.name ?? f.filename ?? f.id)
			})),
			writeAccess: row.write_access !== false
		};
	} catch (err) {
		if (err instanceof ProjectError && err.status === 404) {
			return null;
		}
		throw err;
	}
};

export interface NewProjectForm {
	name: string;
	description: string;
}

export const createProject = async (
	token: string,
	form: NewProjectForm,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<HiveProject> => {
	const row = await requestJson<RawKnowledgeRow>(
		'/knowledge/create',
		{
			method: 'POST',
			headers: headers(token),
			body: JSON.stringify({
				name: form.name,
				description: form.description,
				access_grants: []
			})
		},
		apiBase,
		fetchImpl
	);
	return toProject(row);
};

/**
 * The backend's KnowledgeForm requires name and description on every update,
 * so callers pass the full object; components always hold it in hand.
 */
export const updateProject = async (
	token: string,
	id: string,
	form: NewProjectForm,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<HiveProject> => {
	const row = await requestJson<RawKnowledgeRow>(
		`/knowledge/${id}/update`,
		{
			method: 'POST',
			headers: headers(token),
			body: JSON.stringify({
				name: form.name,
				description: form.description
			})
		},
		apiBase,
		fetchImpl
	);
	return toProject(row);
};

export const deleteProject = async (
	token: string,
	id: string,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<boolean> => {
	await requestJson<unknown>(
		`/knowledge/${id}/delete`,
		{ method: 'DELETE', headers: headers(token) },
		apiBase,
		fetchImpl
	);
	return true;
};

/* ------------------------------------------------------------------ *
 * Files bound to the project scope
 * ------------------------------------------------------------------ */

export const addFileToProject = async (
	token: string,
	projectId: string,
	fileId: string,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<boolean> => {
	await requestJson<unknown>(
		`/knowledge/${projectId}/file/add`,
		{
			method: 'POST',
			headers: headers(token),
			body: JSON.stringify({ file_id: fileId })
		},
		apiBase,
		fetchImpl
	);
	return true;
};

export const removeFileFromProject = async (
	token: string,
	projectId: string,
	fileId: string,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<boolean> => {
	await requestJson<unknown>(
		`/knowledge/${projectId}/file/remove`,
		{
			method: 'POST',
			headers: headers(token),
			body: JSON.stringify({ file_id: fileId })
		},
		apiBase,
		fetchImpl
	);
	return true;
};

/* ------------------------------------------------------------------ *
 * Conversations bound to the project
 * ------------------------------------------------------------------ */

interface RawChatListRow {
	id: string;
	title: string;
	updated_at: number;
}

/**
 * Write the binding onto one chat's persisted blob. The chat-update route
 * merges top-level keys of `chat`, so this only ever touches our namespaced
 * key and leaves messages, history and every other field alone.
 */
export const bindChatToProject = async (
	token: string,
	chatId: string,
	projectId: string | null,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<boolean> => {
	await requestJson<unknown>(
		`/chats/${chatId}`,
		{
			method: 'POST',
			headers: headers(token),
			body: JSON.stringify({ chat: { [PROJECT_CHAT_KEY]: projectId } })
		},
		apiBase,
		fetchImpl
	);
	return true;
};

export const createBoundChat = async (
	token: string,
	projectId: string,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<{ id: string }> => {
	const chat = await requestJson<{ id: string }>(
		'/chats/new',
		{
			method: 'POST',
			headers: headers(token),
			body: JSON.stringify({ chat: { [PROJECT_CHAT_KEY]: projectId }, folder_id: null })
		},
		apiBase,
		fetchImpl
	);
	return { id: chat.id };
};

/**
 * Collect the project's bound conversations by walking the user's chat list
 * and reading each chat blob for our marker key.
 *
 * ponytail: one fetch per candidate chat, no server-side filter exists on the
 * pinned image. Fine at demo scale (tens of chats); if it ever hurts, a real
 * projects table with a chat.project_id column is the documented upgrade path.
 */
export const resolveProjectConversations = async (
	token: string,
	projectId: string,
	pageSize = 100,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<HiveProjectConversation[]> => {
	const out: HiveProjectConversation[] = [];
	for (let page = 1; page <= 50; page++) {
		const params = new URLSearchParams();
		params.append('page', String(page));
		const rows = await requestJson<RawChatListRow[]>(
			`/chats/list?${params.toString()}`,
			{ method: 'GET', headers: headers(token) },
			apiBase,
			fetchImpl
		);
		for (const row of rows) {
			try {
				const full = await requestJson<{
					chat?: { [key: string]: unknown };
					title?: string;
					updated_at?: number;
				}>(`/chats/${row.id}`, { method: 'GET', headers: headers(token) }, apiBase, fetchImpl);
				if (full?.chat?.[PROJECT_CHAT_KEY] === projectId) {
					out.push({
						id: row.id,
						title: full.title || row.title || 'Untitled',
						updatedAt: Number(full.updated_at ?? row.updated_at ?? 0)
					});
				}
			} catch {
				// Deleted between listing and read: skip, do not fail the page.
			}
		}
		if (rows.length < pageSize) break;
	}
	out.sort((a, b) => b.updatedAt - a.updatedAt);
	return out;
};
