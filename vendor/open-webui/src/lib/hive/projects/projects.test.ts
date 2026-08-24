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
	updateProject
} from './projects';

/*
 * Runs in the scratch tree scripts/test-owui-hive-frontend.sh builds: no
 * aliases, no svelte, plain vitest over fetch stubs. The same file also runs
 * against the real tree inside the image build (npm run test:frontend), so it
 * must stay free of any import a bare vitest cannot resolve.
 */

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

	it('maps detail files and returns null on a missing project', async () => {
		const okFetch = (async (input: RequestInfo | URL) => {
			const url = String(input);
			if (url.endsWith('/knowledge/k1')) {
				return json({
					id: 'k1',
					name: 'P',
					description: '',
					updated_at: 9,
					files: [
						{ id: 'f1', meta: { name: 'resume.pdf' } },
						{ id: 'f2', filename: 'notes.txt' }
					]
				});
			}
			return new Response('{}', { status: 404 });
		}) as unknown as typeof fetch;

		const project = await getProject('tok', 'k1', '/api/v1', okFetch);
		expect(project?.files).toEqual([
			{ id: 'f1', name: 'resume.pdf' },
			{ id: 'f2', name: 'notes.txt' }
		]);

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

	it('surfaces error details and keeps the status code', async () => {
		const fetchImpl = (async () =>
			new Response(JSON.stringify({ detail: 'Knowledge not found' }), {
				status: 404,
				headers: { 'Content-Type': 'application/json' }
			})) as unknown as typeof fetch;

		await expect(listProjects('tok', '/api/v1', fetchImpl)).rejects.toMatchObject({
			name: 'ProjectError',
			status: 404
		});
	});

	it('exposes PROJECT_CHAT_KEY namespaced for blob safety', () => {
		expect(PROJECT_CHAT_KEY).toBe('hiveProject');
		expect(new ProjectError('x').name).toBe('ProjectError');
	});
});
