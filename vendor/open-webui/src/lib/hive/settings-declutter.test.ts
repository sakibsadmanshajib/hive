import { describe, expect, it } from 'vitest';
import { existsSync } from 'node:fs';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// Regression guard for the P0 settings declutter wave. The repo has no
// component-test harness for the chat frontend, so these tests pin the
// rendered surface at source level: removed controls must not appear in any
// component that renders them, and surviving controls must stay.

const component = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

const settingsModal = () => component('../components/chat/SettingsModal.svelte');
const account = () => component('../components/chat/Settings/Account.svelte');
const advancedParams = () =>
	component('../components/chat/Settings/Advanced/AdvancedParams.svelte');

describe('connections tab removal', () => {
	it('drops the tab from the settings modal rail, search index and content pane', () => {
		const src = settingsModal();
		expect(src).not.toContain("from './Settings/Connections.svelte'");
		expect(src).not.toContain('<Connections');
		expect(src).not.toContain("'connections'");
		expect(src).not.toContain('enable_direct_connections');
	});

	it('deletes the user connections components outright', () => {
		const base = new URL('../components/chat/Settings/', import.meta.url);
		expect(existsSync(fileURLToPath(new URL('Connections.svelte', base)))).toBe(false);
		expect(existsSync(fileURLToPath(new URL('Connections/Connection.svelte', base)))).toBe(false);
	});

	it('stops user surfaces from forwarding stored direct connections', () => {
		for (const rel of [
			'../components/chat/SettingsModal.svelte',
			'../components/chat/ModelSelector/Selector.svelte'
		]) {
			expect(component(rel)).not.toContain('$settings?.directConnections');
			expect(component(rel)).not.toContain('enable_direct_connections');
		}
	});
});

describe('account page stubs and field removal', () => {
	it('removes the password change form', () => {
		const src = account();
		expect(src).not.toContain('UpdatePassword');
		expect(
			existsSync(
				fileURLToPath(
					new URL('../components/chat/Settings/Account/UpdatePassword.svelte', import.meta.url)
				)
			)
		).toBe(false);
	});

	it('removes the OWUI API key and session JWT sections', () => {
		const src = account();
		for (const marker of ['createAPIKey', 'getAPIKey', 'JWT Token', 'Create new secret key']) {
			expect(src).not.toContain(marker);
		}
	});

	it('keeps only name plus avatar among profile fields', () => {
		const src = account();
		for (const marker of ["t('Bio')", "t('Gender')", "t('Birth Date')"]) {
			expect(src).not.toContain(marker);
		}
		expect(src).toContain("t('Name')");
		expect(src).toContain('<UserProfileImage');
	});
});

describe('advanced params local-engine purge', () => {
	const purged = [
		'num_ctx',
		'num_batch',
		'num_keep',
		'num_gpu',
		'num_thread',
		'use_mmap',
		'use_mlock',
		'keep_alive',
		'mirostat',
		'tfs_z',
		'min_p',
		'repeat_penalty'
	];

	it('removes every local-engine parameter from defaults and template', () => {
		const src = advancedParams();
		const withoutComments = src
			.split('\n')
			.filter((line) => !line.trim().startsWith('//'))
			.join('\n');
		for (const key of purged) {
			expect(withoutComments).not.toContain(key);
		}
	});

	it('keeps the OpenAI family parameters intact', () => {
		const src = advancedParams();
		for (const key of [
			'stream_response',
			'seed',
			'stop',
			'temperature',
			'reasoning_effort',
			'logit_bias',
			'max_tokens',
			'top_k',
			'top_p',
			'frequency_penalty',
			'presence_penalty'
		]) {
			expect(src).toContain(key);
		}
	});
});
