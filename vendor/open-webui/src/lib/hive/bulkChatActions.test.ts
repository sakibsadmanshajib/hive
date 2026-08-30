import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

import { runBulkChatAction, type BulkChatAction } from './bulkChatActions';

const here = dirname(fileURLToPath(import.meta.url));
const readComponent = (relative: string): string =>
	readFileSync(resolve(here, relative), 'utf8');

const dataControls = (): string =>
	readComponent('../components/chat/Settings/DataControls.svelte');

type Journal = {
	events: string[];
	errors: string[];
	successes: string[];
};

/*
 * One action, with every collaborator recorded in a single ordered log.
 *
 * Ordered rather than a set of counters because the ordering is the property
 * under test: a write that failed must not be followed by a navigation, a list
 * refresh or a success message, and counters alone cannot tell "did not run"
 * from "ran in the wrong place".
 */
const action = (
	write: () => Promise<unknown>,
	overrides: Partial<BulkChatAction> = {}
): { action: BulkChatAction; journal: Journal } => {
	const journal: Journal = { events: [], errors: [], successes: [] };
	return {
		journal,
		action: {
			write: async () => {
				journal.events.push('write');
				return await write();
			},
			navigateHome: async () => {
				journal.events.push('navigate');
			},
			refresh: async () => {
				journal.events.push('refresh');
			},
			notifyError: (message: string) => {
				journal.events.push('error');
				journal.errors.push(message);
			},
			notifySuccess: (message: string) => {
				journal.events.push('success');
				journal.successes.push(message);
			},
			successMessage: 'Deleted all chats.',
			failureMessage: 'Failed to delete all chats.',
			...overrides
		}
	};
};

describe('runBulkChatAction, on a write the server accepted', () => {
	it('refreshes the chat lists, leaves the open chat behind and says so', async () => {
		const { action: subject, journal } = action(async () => true);

		await expect(runBulkChatAction(subject)).resolves.toBe(true);

		expect(journal.events).toEqual(['write', 'navigate', 'refresh', 'success']);
		expect(journal.successes).toEqual(['Deleted all chats.']);
		expect(journal.errors).toEqual([]);
	});

	/*
	 * The refresh is the half that keeps the sidebar honest, and it can fail on
	 * its own. Reporting that as a failed delete would be a lie in the other
	 * direction, so both facts are told: the write landed, and the view behind
	 * it may not have caught up.
	 */
	it('still reports the write when the refetch behind it fails', async () => {
		const { action: subject, journal } = action(async () => true, {
			refresh: async () => {
				throw 'Network Problem';
			}
		});

		await expect(runBulkChatAction(subject)).resolves.toBe(true);

		expect(journal.events).toEqual(['write', 'navigate', 'error', 'success']);
		expect(journal.errors).toEqual(['Network Problem']);
	});
});

/*
 * The reason this module exists.
 *
 * `POST /api/v1/chats/archive/all` and `DELETE /api/v1/chats/` are declared
 * `response_model=bool`, and both model functions behind them
 * (`archive_all_chats_by_user_id`, `delete_chats_by_user_id` in
 * backend/open_webui/models/chats.py) catch their own exception and return
 * False. A bulk write that did nothing therefore arrives as HTTP 200 with
 * `false` in the body, which the fetch wrapper in $lib/apis/chats hands back as
 * an ordinary result. A caller that ignores the body cannot tell that apart
 * from a write that emptied the account.
 */
describe('runBulkChatAction, on a write the server refused', () => {
	it('treats a false body as the failure it is', async () => {
		const { action: subject, journal } = action(async () => false);

		await expect(runBulkChatAction(subject)).resolves.toBe(false);

		expect(journal.events).toEqual(['write', 'error']);
		expect(journal.errors).toEqual(['Failed to delete all chats.']);
	});

	it('treats an empty body the same way', async () => {
		const nulled = action(async () => null);
		await expect(runBulkChatAction(nulled.action)).resolves.toBe(false);
		expect(nulled.journal.events).toEqual(['write', 'error']);

		const undefined_ = action(async () => undefined);
		await expect(runBulkChatAction(undefined_.action)).resolves.toBe(false);
		expect(undefined_.journal.events).toEqual(['write', 'error']);
	});

	it('never navigates away or claims success', async () => {
		const { action: subject, journal } = action(async () => false);

		await runBulkChatAction(subject);

		expect(journal.events).not.toContain('navigate');
		expect(journal.events).not.toContain('refresh');
		expect(journal.successes).toEqual([]);
	});
});

/*
 * The fetch wrapper throws `err.detail`, which is a string on every refusal the
 * chat backend raises (401 ACCESS_PROHIBITED when the caller lacks the
 * `chat.delete` permission is the one a real user meets), and can be anything
 * at all when the failure came from somewhere else.
 */
describe('runBulkChatAction, on a write that threw', () => {
	it('shows the reason the server gave', async () => {
		const { action: subject, journal } = action(async () => {
			throw 'Access prohibited';
		});

		await expect(runBulkChatAction(subject)).resolves.toBe(false);
		expect(journal.errors).toEqual(['Access prohibited']);
	});

	it('unwraps an Error and an undetailed rejection alike', async () => {
		const thrownError = action(async () => {
			throw new Error('Failed to fetch');
		});
		await runBulkChatAction(thrownError.action);
		expect(thrownError.journal.errors).toEqual(['Failed to fetch']);

		const thrownObject = action(async () => {
			throw { detail: 'Not authenticated' };
		});
		await runBulkChatAction(thrownObject.action);
		expect(thrownObject.journal.errors).toEqual(['Not authenticated']);

		const thrownNothing = action(async () => {
			throw undefined;
		});
		await runBulkChatAction(thrownNothing.action);
		expect(thrownNothing.journal.errors).toEqual(['Failed to delete all chats.']);
	});
});

/*
 * The wiring, read from the component source.
 *
 * DataControls.svelte imports $lib stores, $app/navigation and the chat API
 * client, none of which resolve in the scratch tree
 * (scripts/test-owui-hive-frontend.sh), so it cannot be rendered here the way a
 * lib/hive component can. What is asserted is the wiring the tested function
 * above depends on: that the two destructive controls open a confirmation
 * instead of firing, and that both handlers go through this module.
 */
describe('Settings > Data Controls wiring (issue #866)', () => {
	it('opens a confirmation instead of firing, for both bulk controls', () => {
		const source = dataControls();

		for (const [label, flag] of [
			['Archive All', 'showArchiveConfirmDialog'],
			['Delete All', 'showDeleteConfirmDialog']
		]) {
			// lastIndexOf, not indexOf: the same `<Label> Chats` string titles the
			// ConfirmDialog declared above the markup, and starting there would
			// swallow the dialog's own `on:confirm` binding into the slice and
			// pass the assertion below for the wrong reason.
			const button = source.slice(
				source.lastIndexOf(`$i18n.t('${label} Chats')`),
				source.indexOf(`$i18n.t('${label}')`)
			);
			expect(button).not.toBe('');
			expect(button).toContain(`${flag} = true`);
			expect(button).not.toMatch(/AllChatsHandler|archiveAllChats\(|deleteAllChats\(/);
		}
	});

	it('names what each confirmation destroys and offers a way out', () => {
		const source = dataControls();

		expect(source).toContain(
			"$i18n.t('Are you sure you want to archive all chats? This action cannot be undone.')"
		);
		expect(source).toContain(
			"$i18n.t('Are you sure you want to delete all chats? This action cannot be undone.')"
		);
		expect(source).toContain('on:confirm={archiveAllChatsHandler}');
		expect(source).toContain('on:confirm={deleteAllChatsHandler}');
		expect(source.match(/on:cancel=\{\(\) => \{/g)?.length ?? 0).toBeGreaterThanOrEqual(2);
	});

	it('routes both handlers through the checked outcome', () => {
		const source = dataControls();

		expect(source).toContain("import { runBulkChatAction } from '$lib/hive/bulkChatActions'");
		expect(source.match(/runBulkChatAction\(/g)?.length ?? 0).toBe(2);
	});

	/*
	 * `DELETE /api/v1/chats/` answers 401 to an ordinary user without the
	 * `chat.delete` permission, so the control is offered on the same terms
	 * Import and Export in this panel already use. Archive All has no such
	 * backend gate and deliberately grows no frontend one.
	 */
	it('offers Delete All only where the backend would accept it', () => {
		const source = dataControls();

		expect(source).toContain(
			"{#if $user?.role === 'admin' || ($user?.permissions?.chat?.delete ?? true)}"
		);
		const archiveRow = source.slice(
			source.lastIndexOf("$i18n.t('Archive All Chats')") - 400,
			source.lastIndexOf("$i18n.t('Archive All Chats')")
		);
		expect(archiveRow).not.toContain('permissions?.chat');
	});

	/*
	 * Delete All used to leave the pinned list alone. `getPinnedChatList` is a
	 * separate fetch feeding a separate sidebar section, so every pinned
	 * conversation stayed on screen, named and clickable, after its row had
	 * been deleted from the database.
	 */
	it('refetches the pinned list rather than emptying it by hand', () => {
		const source = dataControls();

		expect(source).toContain('pinnedChats.set(await getPinnedChatList(localStorage.token))');
		expect(source).not.toContain('pinnedChats.set([])');
	});
});
