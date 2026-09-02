import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { afterEach, describe, expect, it, vi } from 'vitest';

import {
	PROJECT_CHAT_KEY,
	ProjectError,
	addFileToProject,
	bindChatToProject,
	createBoundChat,
	createProject,
	deleteProject,
	getProject,
	listProjects,
	removeFileFromProject,
	resolveProjectConversations,
	updateProject,
	withProjectFiles
} from './projects';

/*
 * Runs in the scratch tree scripts/test-owui-hive-frontend.sh builds: no
 * aliases, no svelte, plain vitest over fetch stubs. The same file also runs
 * against the real tree inside the image build (npm run test:frontend), so it
 * must stay free of any import a bare vitest cannot resolve.
 */

const here = dirname(fileURLToPath(import.meta.url));
const chatSource = readFileSync(resolve(here, '../../components/chat/Chat.svelte'), 'utf8');

const json = (body: unknown) =>
	new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });

describe('projects data layer', () => {
	afterEach(() => {
		vi.unstubAllGlobals();
	});

	it('lists projects from the knowledge collection list, dropping external connections', async () => {
		const calls: string[] = [];
		const fetchImpl = (async (input: RequestInfo | URL) => {
			calls.push(String(input));
			return json({
				items: [
					{ id: 'k1', name: 'Resume', description: 'Job hunt docs', updated_at: 100 },
					{
						id: 'k2',
						name: 'External conn',
						description: '',
						updated_at: 90,
						meta: { source: 'external' }
					}
				],
				total: 2
			});
		}) as unknown as typeof fetch;

		const rows = await listProjects('tok', '/api/v1', fetchImpl);

		expect(rows).toEqual([{ id: 'k1', name: 'Resume', description: 'Job hunt docs', updatedAt: 100 }]);
		expect(calls[0]).toBe('/api/v1/knowledge/');
	});

	it('creates a project through the knowledge create endpoint with empty grants', async () => {
		let body: Record<string, unknown> = {};
		const fetchImpl = (async (_input: RequestInfo | URL, init: RequestInit = {}) => {
			body = JSON.parse(String(init.body ?? '{}'));
			return json({ id: 'new1', name: body.name, description: body.description, updated_at: 5 });
		}) as unknown as typeof fetch;

		const project = await createProject('tok', { name: 'AI Lending Lab', description: 'd' }, '/api/v1', fetchImpl);

		expect(project.id).toBe('new1');
		expect(body.name).toBe('AI Lending Lab');
		expect(body.access_grants).toEqual([]);
	});

	it('updates a project by sending the full KnowledgeForm the backend requires', async () => {
		let body: Record<string, unknown> = {};
		const fetchImpl = (async (_i: RequestInfo | URL, init: RequestInit = {}) => {
			body = JSON.parse(String(init.body ?? '{}'));
			return json({ id: 'k1', name: body.name, description: body.description, updated_at: 6 });
		}) as unknown as typeof fetch;

		await updateProject('tok', 'k1', { name: 'Renamed', description: 'new desc' }, '/api/v1', fetchImpl);

		expect(body).toEqual({ name: 'Renamed', description: 'new desc' });
	});

	it('deletes a project with DELETE on the knowledge delete endpoint', async () => {
		let method = '';
		let url = '';
		const fetchImpl = (async (input: RequestInfo | URL, init: RequestInit = {}) => {
			url = String(input);
			method = String(init.method ?? '');
			return json(true);
		}) as unknown as typeof fetch;

		await expect(deleteProject('tok', 'k1', '/api/v1', fetchImpl)).resolves.toBe(true);
		expect(method).toBe('DELETE');
		expect(url).toContain('/api/v1/knowledge/k1/delete');
	});

	it('loads detail files from the per-collection listing endpoint, null on a missing project', async () => {
		const okFetch = (async (input: RequestInfo | URL) => {
			const url = String(input);
			if (url.endsWith('/knowledge/k1')) {
				return json({
					id: 'k1',
					name: 'P',
					description: '',
					updated_at: 9,
					files: null,
					write_access: true
				});
			}
			if (url.includes('/knowledge/k1/files')) {
				return json({
					items: [
						{ id: 'f1', meta: { name: 'resume.pdf' } },
						{ id: 'f2', filename: 'notes.txt' }
					],
					total: 2
				});
			}
			return new Response('{}', { status: 404 });
		}) as unknown as typeof fetch;

		const project = await getProject('tok', 'k1', '/api/v1', okFetch);
		expect(project?.files).toEqual([
			{ id: 'f1', name: 'resume.pdf' },
			{ id: 'f2', name: 'notes.txt' }
		]);
		expect(project?.writeAccess).toBe(true);

		const missing = await getProject('tok', 'gone', '/api/v1', okFetch);
		expect(missing).toBeNull();
	});

	it('adds and removes files through the knowledge file endpoints', async () => {
		const seen: Array<{ url: string; body?: Record<string, unknown> }> = [];
		const fetchImpl = (async (input: RequestInfo | URL, init: RequestInit = {}) => {
			seen.push({
				url: String(input),
				body: init.body ? JSON.parse(String(init.body)) : undefined
			});
			return json(true);
		}) as unknown as typeof fetch;

		await addFileToProject('tok', 'k1', 'file-1', '/api/v1', fetchImpl);
		await removeFileFromProject('tok', 'k1', 'file-1', '/api/v1', fetchImpl);

		expect(seen[0].url).toContain('/knowledge/k1/file/add');
		expect(seen[0].body).toEqual({ file_id: 'file-1' });
		expect(seen[1].url).toContain('/knowledge/k1/file/remove');
	});

	it('binds and unbinds a chat by writing only the namespaced key into the chat blob', async () => {
		const seen: Array<{ url: string; body?: Record<string, unknown> }> = [];
		const fetchImpl = (async (input: RequestInfo | URL, init: RequestInit = {}) => {
			seen.push({
				url: String(input),
				body: init.body ? JSON.parse(String(init.body)) : undefined
			});
			return json({ id: 'c1' });
		}) as unknown as typeof fetch;

		await bindChatToProject('tok', 'c1', 'k1', '/api/v1', fetchImpl);
		await bindChatToProject('tok', 'c1', null, '/api/v1', fetchImpl);

		expect(seen[0]).toEqual({
			url: '/api/v1/chats/c1',
			body: { chat: { [PROJECT_CHAT_KEY]: 'k1' } }
		});
		// Unbind writes an explicit null rather than omitting the key, so the
		// merge actually clears it server side.
		expect(seen[1].body).toEqual({ chat: { [PROJECT_CHAT_KEY]: null } });
	});

	it('creates a chat already bound at birth via chats/new', async () => {
		let body: Record<string, unknown> = {};
		const fetchImpl = (async (_i: RequestInfo | URL, init: RequestInit = {}) => {
			body = JSON.parse(String(init.body ?? '{}'));
			return json({ id: 'c-new' });
		}) as unknown as typeof fetch;

		const chat = await createBoundChat('tok', 'k1', '/api/v1', fetchImpl);
		expect(chat.id).toBe('c-new');
		expect((body.chat as Record<string, unknown>)[PROJECT_CHAT_KEY]).toBe('k1');
	});

	it('resolves bound conversations by scanning the chat list and reading blobs', async () => {
		const chatsById: Record<string, unknown> = {
			c1: { id: 'c1', title: 'T1', chat: { [PROJECT_CHAT_KEY]: 'k1' }, updated_at: 10 },
			c2: { id: 'c2', title: 'Other', chat: {}, updated_at: 11 },
			c3: { id: 'c3', title: 'T3', chat: { [PROJECT_CHAT_KEY]: 'k1' }, updated_at: 30 }
		};
		const fetchImpl = (async (input: RequestInfo | URL) => {
			const url = String(input);
			if (url.startsWith('/api/v1/chats/list')) {
				return json([{ id: 'c1', title: 'T1', updated_at: 10 }, { id: 'c2', title: 'O', updated_at: 11 }, { id: 'c3', title: 'T3', updated_at: 30 }]);
			}
			const id = url.split('/chats/')[1];
			if (id === 'ghost') return new Response('{}', { status: 404 });
			return json(chatsById[id]);
		}) as unknown as typeof fetch;

		const convos = await resolveProjectConversations('tok', 'k1', 100, '/api/v1', fetchImpl);
		expect(convos.map((c) => c.id)).toEqual(['c3', 'c1']);
		expect(convos[0].title).toBe('T3');
	});

	it('keeps the status code but never renders backend detail text', async () => {
		const fetchImpl = (async () =>
			new Response(JSON.stringify({ detail: 'Knowledge not found: provider groq said x' }), {
				status: 404,
				headers: { 'Content-Type': 'application/json' }
			})) as unknown as typeof fetch;

		await expect(listProjects('tok', '/api/v1', fetchImpl)).rejects.toSatisfy((err: ProjectError) => {
			expect(err.name).toBe('ProjectError');
			expect(err.status).toBe(404);
			expect(err.message).not.toContain('groq');
			expect(err.message).not.toContain('Knowledge not found');
			return true;
		});
	});

	it('propagates a detail-read failure that is not a deletion', async () => {
		const chatsById: Record<string, unknown> = {
			c1: { id: 'c1', title: 'T1', chat: { [PROJECT_CHAT_KEY]: 'k1' }, updated_at: 10 }
		};
		let c1Reads = 0;
		const fetchImpl = (async (input: RequestInfo | URL) => {
			const url = String(input);
			if (url.startsWith('/api/v1/chats/list')) {
				return json([{ id: 'c1', title: 'T1', updated_at: 10 }]);
			}
			const id = url.split('/chats/')[1];
			if (id === 'c1') {
				c1Reads++;
				if (c1Reads === 1) {
					return new Response('{}', { status: 500 });
				}
				return json(chatsById[id]);
			}
			return new Response('{}', { status: 404 });
		}) as unknown as typeof fetch;

		// First read 500s: the scan must reject rather than silently shrink.
		await expect(resolveProjectConversations('tok', 'k1', 60, '/api/v1', fetchImpl)).rejects.toMatchObject({
			status: 500
		});
		// Second read succeeds (404s and 403s stay skipped, not thrown).
		await expect(resolveProjectConversations('tok', 'k1', 60, '/api/v1', fetchImpl)).resolves.toHaveLength(1);
	});

	it('exposes PROJECT_CHAT_KEY namespaced for blob safety', () => {
		expect(PROJECT_CHAT_KEY).toBe('hiveProject');
		expect(new ProjectError('x').name).toBe('ProjectError');
	});
});

/* ------------------------------------------------------------------ *
 * Issue #1358: the project's files must reach the model, not just the
 * project page.
 * ------------------------------------------------------------------ */

describe('a project bound chat carries the project scope into its request', () => {
	it('attaches the project as a retrievable collection when the chat is bound', () => {
		// This is the state a project bound conversation is actually in at send
		// time. Chat.svelte prunes chatFiles down to the files some message in
		// the branch references (Chat.svelte, sendMessageSocket), so a chat that
		// was merely created inside a project reaches this point with nothing:
		// an attachment written onto the chat blob at creation time is deleted
		// before it is ever sent. The binding has to be resolved here instead.
		expect(withProjectFiles([], 'proj-1')).toEqual([{ type: 'collection', id: 'proj-1' }]);
	});

	it('keeps the turn\'s own attachments alongside the project', () => {
		const uploaded = { type: 'file', id: 'f1', name: 'invoice.pdf' };
		expect(withProjectFiles([uploaded], 'proj-1')).toEqual([
			uploaded,
			{ type: 'collection', id: 'proj-1' }
		]);
	});

	it('leaves an unbound chat exactly as it was, same array', () => {
		const files = [{ type: 'file', id: 'f1' }];
		expect(withProjectFiles(files, null)).toBe(files);
		expect(withProjectFiles(files, '')).toBe(files);
	});

	it('does not attach the project twice when it is already on the turn', () => {
		// The person can attach the same project by hand from the plus menu.
		const manual = { type: 'collection', id: 'proj-1', name: 'Tax 2026' };
		expect(withProjectFiles([manual], 'proj-1')).toEqual([manual]);
	});

	it('sends a reference rather than a snapshot, so a file added later still arrives', () => {
		// The whole claim on ProjectDetail.svelte is that files land in EVERY
		// conversation in the project, including conversations that already
		// existed when the file was uploaded. A collection id is resolved to its
		// current file set by the retrieval layer on every request, so this
		// holds; a list of file ids captured at bind time would not.
		const attached = withProjectFiles([], 'proj-1');
		expect(attached[0]).not.toHaveProperty('collection_names');
		expect(Object.keys(attached[0])).toEqual(['type', 'id']);
	});
});

describe('Chat.svelte actually reads the binding it is handed', () => {
	it('loads the project marker off the chat blob and clears it on a new chat', () => {
		expect(chatSource).toContain('hiveProjectId = chatContent?.[PROJECT_CHAT_KEY] ?? null');
		expect(chatSource).toContain('hiveProjectId = null');
	});

	it('resolves the binding at request assembly, where every entry point passes', () => {
		// sendMessageSocket is the one function submitPrompt, regeneration and
		// continue all reach, and `files` is the field it puts on the wire. A
		// call anywhere earlier would be pruned away again.
		const assembly = chatSource.slice(chatSource.indexOf('const sendMessageSocket'));
		expect(assembly).toContain('files = withProjectFiles(files, hiveProjectId)');
	});
});
