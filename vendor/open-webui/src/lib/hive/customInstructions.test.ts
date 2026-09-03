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

	it('reads a person who has written none as the empty string, not as a failure', async () => {
		const stub = async () => jsonResponse(200, { content: '' });
		expect(await getCustomInstructions(DEFAULT_INSTRUCTIONS_API_BASE_URL, stub)).toBe('');
	});

	it('reports every unreadable answer as null, never as an empty box', async () => {
		// This is the guard, and it replaces a test that pinned the defect by
		// asserting the opposite. Returning '' here meant the pane marked
		// itself loaded and the person's next Save posted that empty string,
		// which deletes the row: one 502 on load erased their instructions.
		const unreadable: Array<[string, () => Promise<Response>]> = [
			['a 404', async () => jsonResponse(404, { detail: 'not configured' })],
			['a 502', async () => jsonResponse(502, { detail: 'bad gateway' })],
			['a 503', async () => jsonResponse(503, { detail: 'unavailable' })],
			['a 504', async () => jsonResponse(504, { detail: 'timed out' })],
			[
				'a network failure',
				async () => {
					throw new TypeError('Failed to fetch');
				}
			],
			['a 200 carrying no JSON', async () => new Response('not json at all', { status: 200 })],
			['a 200 with no content field', async () => jsonResponse(200, {})],
			['a 200 whose content is not text', async () => jsonResponse(200, { content: { a: 1 } })]
		];
		for (const [name, stub] of unreadable) {
			const got = await getCustomInstructions(DEFAULT_INSTRUCTIONS_API_BASE_URL, stub);
			expect(got, `${name} must read as unreadable, not as empty`).toBeNull();
		}
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

	it('only marks the box loaded when the read actually succeeded', () => {
		// The deletion this guards is one assignment away: `instructionsLoaded
		// = true` unconditionally, which is what shipped in the first
		// revision. The pane then saves an empty string over text it never
		// read, and empty content deletes the row.
		const src = codeOnly('../components/chat/Settings/General.svelte');
		expect(src).toContain('instructionsLoaded = stored !== null');
		expect(src).not.toContain('instructionsLoaded = true');
		// The save is gated on the same flag, so an unreadable load skips the
		// write entirely instead of posting over it.
		expect(src).toContain('if (instructionsLoaded)');
	});

	it('tells the person their instructions could not be loaded', () => {
		const src = component('../components/chat/Settings/General.svelte');
		expect(src).toContain('instructionsUnreadable');
		expect(src).toContain('could not be loaded');
	});

	it('keeps the client where the scratch-tree runner can load it', () => {
		expect(existsSync(fileURLToPath(new URL('./customInstructions.ts', import.meta.url)))).toBe(
			true
		);
	});
});
