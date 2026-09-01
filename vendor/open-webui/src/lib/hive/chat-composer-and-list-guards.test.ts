import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// Source-level guards for the three chat-surface defects fixed together in
// issues #1619, #1625 and #1626. The repo has no component-test harness for
// these upstream components, so, like chat-noise-guards.test.ts next door, this
// pins the surface by reading them.
//
// Every file named here is mirrored into the scratch tree by
// scripts/test-owui-hive-frontend.sh; adding one to this list means adding it
// there too, or the read throws instead of asserting.

const read = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

const component = (rel: string): string => read(`../components/${rel}`);

const route = (rel: string): string => read(`../../routes/${rel}`);

describe('one conversation, one model (issue #1626)', () => {
	const modelSelector = () => component('chat/ModelSelector.svelte');

	// The plus button dispatched a single prompt to several models and rendered
	// the answers side by side. Not a Hive capability, and confusing next to a
	// picker that otherwise switches one model.
	it('the composer chip offers no way to add a second model', () => {
		const source = modelSelector();
		expect(source).not.toContain('Add Model');
		expect(source).not.toContain('Remove Model');
		expect(source).not.toContain('M12 6v12m6-6H6');
	});

	// Removing the button alone would have left the capability half present:
	// a `models=` query parameter, a folder carrying several model ids, and any
	// conversation saved while the button existed all still arrive as a longer
	// list. The clamp is what makes the drawn model the dispatched model.
	it('clamps the bound selection to a single model', () => {
		expect(modelSelector()).toContain('selectedModels.length !== 1');
	});

	it('drops the multi-model permission branch that gated the button', () => {
		expect(modelSelector()).not.toContain('multiple_models');
	});

	it('the composer mounts exactly one selector', () => {
		const mounts = component('chat/MessageInput.svelte').match(/<ModelSelector\b/g) ?? [];
		expect(mounts).toHaveLength(1);
	});
});

describe('the sidebar chat list loads page one (issue #1625)', () => {
	// `chats.set(await getChatList(...))` REPLACES the whole list, so it has to
	// name page 1 outright. Passing the live cursor made the replacement depend
	// on how far the infinite scroll had walked, and the scroll sentinel is
	// visible from the first frame on any account short of a full page, so the
	// cursor was already 2 by the time the first title update fired: the list
	// was replaced with an empty page 2 and the nav went blank.
	const STALE = 'getChatList(localStorage.token, $currentChatPage)';

	it.each([
		['chat/Chat.svelte'],
		['chat/Messages.svelte'],
		['chat/Placeholder.svelte'],
		['chat/Settings/DataControls.svelte'],
		['layout/SearchModal.svelte'],
		['layout/Sidebar/ChatItem.svelte']
	])('%s replaces the list with page one', (rel) => {
		expect(component(rel)).not.toContain(STALE);
	});

	it('the root layout replaces the list with page one', () => {
		expect(route('+layout.svelte')).not.toContain(STALE);
	});

	it('only loadMoreChats asks for a later page', () => {
		const source = component('layout/Sidebar.svelte');
		const [beforeLoadMore, ...rest] = source.split('const loadMoreChats = async () => {');
		expect(rest).toHaveLength(1);
		// initChatList and everything above it replaces the list, so none of it
		// may carry the cursor.
		expect(beforeLoadMore).not.toContain(STALE);
		// loadMoreChats is the one caller for which the cursor is the point.
		expect(rest[0].split(STALE)).toHaveLength(2);
	});

	// A short page is the last page. Testing only for an empty one made the
	// final request of every session a guaranteed miss, which on an account
	// with fewer than sixty conversations meant a page 2 request fired the
	// moment the list rendered: the request the issue was filed about.
	it('stops paginating on a short page, not only an empty one', () => {
		const source = component('layout/Sidebar.svelte');
		expect(source).toContain('allChatsLoaded = _chats.length < CHAT_LIST_PAGE_SIZE');
		expect(source).toContain('allChatsLoaded = newChatList.length < CHAT_LIST_PAGE_SIZE');
		expect(source).not.toContain('allChatsLoaded = newChatList.length === 0');
	});
});

describe('Enter sends on every viewport (issue #1619)', () => {
	const messageInput = () => component('chat/MessageInput.svelte');

	// Upstream wrapped the whole send branch in a narrow-viewport test ANDed
	// with a touch-capability test. A phone satisfies both, so the branch was
	// unreachable there and Enter only ever inserted a newline.
	it('does not gate the send path on touch capability', () => {
		const source = messageInput();
		expect(source).not.toContain('ontouchstart');
		expect(source).not.toContain('maxTouchPoints');
	});

	it('keeps the IME guard, which is a different thing entirely', () => {
		expect(messageInput()).toContain('if (inOrNearComposition(e)) {');
	});

	it('leaves Shift+Enter inserting a line break on every viewport', () => {
		expect(messageInput()).toContain(
			'shiftEnter={!($settings?.ctrlEnterToSend ?? false)}'
		);
	});
});
