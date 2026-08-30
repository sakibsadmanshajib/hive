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

describe('cancelled file picker (issue #847)', () => {
	// A cancelled picker fires change with an empty FileList, which is
	// indistinguishable from selecting nothing, so reporting it as an error
	// told every user who backed out of the dialog that a file was missing.
	//
	// Every file picker in the product carried the same copy-pasted handler,
	// six occurrences across the five files below, so the two composers were
	// fixed first and the rest listed here. Pinning all five files together is
	// what stops the next copy of the pattern from being pasted back in on
	// whichever surface was left out.
	it.each([
		['chat', 'MessageInput.svelte'],
		['channel', 'MessageInput.svelte'],
		['workspace/Knowledge', 'KnowledgeBase.svelte'],
		['workspace/Models', 'Knowledge.svelte'],
		['admin/Users/UserList', 'AddUserModal.svelte']
	])('%s/%s stays quiet on an empty selection', (dir, file) => {
		expect(component(dir, file)).not.toContain('File not found.');
	});

	// The admin CSV import is the one site where the branch is reachable by
	// pressing Submit with nothing chosen, so it keeps a message. It has to
	// name that state rather than a lookup that never happened, otherwise
	// deleting the toast would leave Submit doing nothing at all.
	it('the admin CSV import names the empty picker instead', () => {
		expect(component('admin/Users/UserList', 'AddUserModal.svelte')).toContain(
			"$i18n.t('No file selected')"
		);
	});
});

describe('anonymous socket connect (issue #557)', () => {
	it('does not warn about a token a signed-out visitor never has', () => {
		expect(route('+layout.svelte')).not.toContain('No token found in localStorage');
	});
});
