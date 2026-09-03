import { describe, expect, it } from 'vitest';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import {
	DEFAULT_INSTRUCTIONS_API_BASE_URL,
	MAX_INSTRUCTIONS_LENGTH,
	getCustomInstructions,
	saveCustomInstructions
} from './customInstructions';

const jsonResponse = (status: number, body: unknown): Response =>
	new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});

describe('reading instructions', () => {
	it('returns the stored text', async () => {
		const stub = async () => jsonResponse(200, { content: 'Answer in British English.' });
		expect(await getCustomInstructions(DEFAULT_INSTRUCTIONS_API_BASE_URL, stub)).toBe(
			'Answer in British English.'
		);
	});

	it('reads with credentials, so the Open WebUI session cookie travels', async () => {
		let seen: RequestInit | undefined;
		const stub = async (_url: string, init?: RequestInit) => {
			seen = init;
			return jsonResponse(200, { content: '' });
		};
		await getCustomInstructions(DEFAULT_INSTRUCTIONS_API_BASE_URL, stub as unknown as typeof fetch);
		expect(seen?.credentials).toBe('include');
		expect(seen?.method).toBe('GET');
	});

	it('treats absence and unavailability alike, because both render an empty box', async () => {
		const cases: Array<() => Promise<Response>> = [
			async () => jsonResponse(200, { content: '' }),
			async () => jsonResponse(200, {}),
			async () => jsonResponse(404, { detail: 'not configured' }),
			async () => jsonResponse(503, { detail: 'unavailable' }),
			async () => new Response('not json at all', { status: 200 })
		];
		for (const stub of cases) {
			expect(await getCustomInstructions(DEFAULT_INSTRUCTIONS_API_BASE_URL, stub)).toBe('');
		}
	});

	it('survives a network failure without throwing at the settings pane', async () => {
		const stub = async () => {
			throw new TypeError('Failed to fetch');
		};
		expect(await getCustomInstructions(DEFAULT_INSTRUCTIONS_API_BASE_URL, stub)).toBe('');
	});

	it('ignores a non-string content field rather than rendering an object', async () => {
		const stub = async () => jsonResponse(200, { content: { nested: true } });
		expect(await getCustomInstructions(DEFAULT_INSTRUCTIONS_API_BASE_URL, stub)).toBe('');
	});
});

describe('saving instructions', () => {
	it('PUTs the content and returns what the server stored', async () => {
		let sent: RequestInit | undefined;
		const stub = async (_url: string, init?: RequestInit) => {
			sent = init;
			return jsonResponse(200, { content: 'Be concise.' });
		};
		const stored = await saveCustomInstructions(
			'  Be concise.  ',
			DEFAULT_INSTRUCTIONS_API_BASE_URL,
			stub as unknown as typeof fetch
		);
		expect(stored).toBe('Be concise.');
		expect(sent?.method).toBe('PUT');
		expect(JSON.parse(String(sent?.body))).toEqual({ content: '  Be concise.  ' });
	});

	it('sends an empty string to clear, which is a legal request', async () => {
		let sent: RequestInit | undefined;
		const stub = async (_url: string, init?: RequestInit) => {
			sent = init;
			return jsonResponse(200, { content: '' });
		};
		expect(
			await saveCustomInstructions(
				'',
				DEFAULT_INSTRUCTIONS_API_BASE_URL,
				stub as unknown as typeof fetch
			)
		).toBe('');
		expect(JSON.parse(String(sent?.body))).toEqual({ content: '' });
	});

	it('refuses over-long text locally without a round trip', async () => {
		let called = false;
		const stub = async () => {
			called = true;
			return jsonResponse(200, { content: '' });
		};
		await expect(
			saveCustomInstructions(
				'x'.repeat(MAX_INSTRUCTIONS_LENGTH + 1),
				DEFAULT_INSTRUCTIONS_API_BASE_URL,
				stub
			)
		).rejects.toThrow(/4000 characters/);
		expect(called).toBe(false);
	});

	it('throws on failure rather than letting the pane close as though it saved', async () => {
		const stub = async () => jsonResponse(500, {});
		await expect(
			saveCustomInstructions('hello', DEFAULT_INSTRUCTIONS_API_BASE_URL, stub)
		).rejects.toThrow(/could not be saved/);
	});

	it("reads edge-api's error envelope", async () => {
		const stub = async () =>
			jsonResponse(400, {
				error: { code: 'INVALID_REQUEST', message: 'custom instructions are limited to 4000 characters' }
			});
		await expect(
			saveCustomInstructions('hello', DEFAULT_INSTRUCTIONS_API_BASE_URL, stub)
		).rejects.toThrow(/limited to 4000 characters/);
	});

	it("reads the shim's own FastAPI refusal shape", async () => {
		const stub = async () =>
			jsonResponse(401, { detail: 'Your Hive sign-in could not be confirmed. Sign in again and retry.' });
		await expect(
			saveCustomInstructions('hello', DEFAULT_INSTRUCTIONS_API_BASE_URL, stub)
		).rejects.toThrow(/Sign in again/);
	});
});

// Source-level guard, the same posture as settings-declutter.test.ts: the chat
// front end has no component-test harness, so the rendered surface is pinned by
// reading the component that renders it.
const component = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

/**
 * The component with its comments removed.
 *
 * The absence guards below are about what the code READS, and both components
 * carry a comment naming the retired store so the next reader knows why it is
 * gone. Asserting against raw source would fail on the explanation and pass
 * only if the explanation were deleted, which is the wrong incentive
 * exactly. Line comments and block comments both go; string contents are not
 * at risk here because no assertion below looks for one.
 */
const codeOnly = (rel: string): string =>
	component(rel)
		.replace(/\/\*[\s\S]*?\*\//g, '')
		.replace(/(^|[^:])\/\/[^\n]*/g, '$1');

describe('the settings pane carries exactly one instructions control', () => {
	it('renders the Hive control, wired to the Hive client', () => {
		const src = component('../components/chat/Settings/General.svelte');
		expect(src).toContain("from '$lib/hive/customInstructions'");
		expect(src).toContain('Custom instructions');
		expect(src).toContain('saveCustomInstructions');
		expect(src).toContain('getCustomInstructions');
	});

	it("no longer writes a second copy into Open WebUI's own settings", () => {
		// `$settings.system` is the store this replaces. Left in place it would
		// be a second system prompt, written to Open WebUI's database and
		// attached to requests by the browser, silently doubling or
		// contradicting what edge-api injects.
		const src = codeOnly('../components/chat/Settings/General.svelte');
		expect(src).not.toContain('$settings.system');
		expect(src).not.toContain('system: system !== ');
	});

	it('stops the chat surface attaching that store to outgoing requests', () => {
		// The settings field is only half of it. Chat.svelte built a system
		// message from the same store on every turn and sent it on two chat
		// records, so leaving those in place would keep a browser-attached
		// system prompt alive with no surface left to set it from.
		const src = codeOnly('../components/chat/Chat.svelte');
		expect(src).not.toContain('$settings.system');
		expect(src).not.toContain('$settings?.system');
	});

	it('keeps the client where the scratch-tree runner can load it', () => {
		expect(existsSync(fileURLToPath(new URL('./customInstructions.ts', import.meta.url)))).toBe(
			true
		);
	});
});
