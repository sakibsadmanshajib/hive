import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// Source-level guards for three pieces of noise the chat front end used to
// emit. The repo has no component-test harness for these upstream components,
// so, like settings-declutter.test.ts, this pins the surface by reading them.

const read = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

const component = (dir: string, file: string): string =>
	read(`../components/${dir}/${file}`);

const route = (file: string): string => read(`../../routes/${file}`);

describe('dead Integrations tab branches (issue #1258)', () => {
	const settingsModal = () => component('chat', 'SettingsModal.svelte');

	it('leaves no unreachable tools branch behind', () => {
		expect(settingsModal()).not.toContain("'tools'");
		expect(settingsModal()).not.toContain('direct_tool_servers');
		expect(settingsModal()).not.toContain('Integrations.svelte');
		expect(settingsModal()).not.toContain('WrenchAlt');
	});
});

describe('cancelled file picker (issue #847, partial)', () => {
	// A cancelled picker fires change with an empty FileList, which is
	// indistinguishable from selecting nothing, so reporting it as an error
	// told every user who backed out of the dialog that a file was missing.
	it.each(['chat', 'channel'])('the %s composer stays quiet on an empty selection', (dir) => {
		expect(component(dir, 'MessageInput.svelte')).not.toContain('File not found.');
	});
});

describe('anonymous socket connect (issue #557)', () => {
	it('does not warn about a token a signed-out visitor never has', () => {
		expect(route('+layout.svelte')).not.toContain('No token found in localStorage');
	});
});
