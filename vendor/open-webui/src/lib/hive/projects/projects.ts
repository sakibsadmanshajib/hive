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
		// The backend's detail text never reaches a customer-bound string:
		// provider names and internal wording ride in `detail`, so it is kept
		// off the rendered message (provider-blind errors convention) and the
		// HTTP status is the stable code the UI classifies on.
		throw new ProjectError(`Request failed (${res.status})`, res.status);
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
 * Delivering the project's files to a conversation
 * ------------------------------------------------------------------ */

/** One entry of a chat completion request's `files` array. */
export interface ChatRequestFile {
	type: string;
	id: string;
	[key: string]: unknown;
}

/**
 * Put the project's document scope on an outgoing chat request (issue #1358).
 *
 * `{ type: 'collection', id }` is the item Open WebUI's own retrieval already
 * understands: `get_sources_from_items` in
 * backend/open_webui/retrieval/utils.py resolves it to the collection's files
 * and applies the caller's read access check before any of them are searched.
 * A project IS a collection on this backend, per the header above, so binding
 * needs no new retrieval path and no new permission: this is the same item the
 * composer's plus menu produces when the same project is attached by hand,
 * minus every optional key, so it reaches the same documents and no others.
 *
 * Minus every optional key is the load bearing half, not a detail. That
 * function reads `context`, `legacy` and `collection_names` off the item and
 * each one selects a weaker branch: `context: 'full'` reads every file's
 * content directly and never reaches `filter_accessible_collections` at all,
 * and `legacy` admits a client supplied `collection_names` list. Emitting two
 * keys forfeits all three and takes the single strictest path,
 * `collection_names.append(item['id'])` followed by that choke point. Do not
 * add a key here without re-reading which branch it opens.
 *
 * A reference rather than a list of file ids, deliberately. The claim the
 * project page makes is that its files reach EVERY conversation in the
 * project, which includes conversations that already existed when a file was
 * uploaded; the collection is resolved on each request, so a file added later
 * is included, while a snapshot taken at bind time would not be.
 *
 * Called at request assembly rather than written onto the chat blob, also
 * deliberately: Chat.svelte prunes chat level files down to those some message
 * in the branch references before it builds the request, so an attachment
 * persisted at chat creation time is deleted before it is ever sent.
 */
export const withProjectFiles = <T extends ChatRequestFile>(
	files: T[],
	projectId: string | null | undefined
): (T | ChatRequestFile)[] => {
	if (!projectId) return files;
	// Already on the turn, because the person attached it by hand.
	if (files.some((file) => file?.id === projectId)) return files;
	return [...files, { type: 'collection', id: projectId }];
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

/**
 * Collect the project's bound conversations by walking the user's chat list
 * and reading each chat blob for our marker key.
 *
 * ponytail: one fetch per candidate chat, no server-side filter exists on the
 * pinned image, and the list endpoint serves 60 rows per page (the pageSize
 * default matches it so the loop does not stop a page early). Fine at demo
 * scale (tens of chats); if it ever hurts, a real projects table with a
 * chat.project_id column is the documented upgrade path.
 */
export const resolveProjectConversations = async (
	token: string,
	projectId: string,
	pageSize = 60,
	apiBase: string = DEFAULT_API_BASE,
	fetchImpl: typeof fetch = fetch
): Promise<HiveProjectConversation[]> => {
	// Walk every list page first (a short page ends the walk), then read the
	// candidate blobs with bounded concurrency so the detail page does not wait
	// on one serial request per chat.
	const candidates: RawChatListRow[] = [];
	for (let page = 1; page <= 50; page++) {
		const params = new URLSearchParams();
		params.append('page', String(page));
		const rows = await requestJson<RawChatListRow[]>(
			`/chats/list?${params.toString()}`,
			{ method: 'GET', headers: headers(token) },
			apiBase,
			fetchImpl
		);
		candidates.push(...rows);
		if (rows.length < pageSize) break;
	}

	const out: HiveProjectConversation[] = [];
	const CONCURRENCY = 8;
	for (let i = 0; i < candidates.length; i += CONCURRENCY) {
		const batch = candidates.slice(i, i + CONCURRENCY);
		const settled = await Promise.all(
			batch.map(async (row) => {
				try {
					const full = await requestJson<{
						chat?: { [key: string]: unknown };
						title?: string;
						updated_at?: number;
					}>(`/chats/${row.id}`, { method: 'GET', headers: headers(token) }, apiBase, fetchImpl);
					if (full?.chat?.[PROJECT_CHAT_KEY] === projectId) {
						return {
							id: row.id,
							title: full.title || row.title || 'Untitled',
							updatedAt: Number(full.updated_at ?? row.updated_at ?? 0),
							hiveProjectId: projectId
						};
					}
					return null;
				} catch (err) {
					// Gone or inaccessible between listing and read: skip. Anything
					// else (a 5xx, a network failure) propagates rather than
					// silently shrinking the conversation list.
					if (
						err instanceof ProjectError &&
						(err.status === 404 || err.status === 403)
					) {
						return null;
					}
					throw err;
				}
			})
		);
		for (const convo of settled) {
			if (convo) out.push(convo);
		}
	}
	out.sort((a, b) => b.updatedAt - a.updatedAt);
	return out;
};
