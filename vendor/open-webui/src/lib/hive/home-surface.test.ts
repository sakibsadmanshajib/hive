import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

/*
 * Hive has one home, and this file is the guard on that.
 *
 * The defect it was written for: Chat.svelte's landing branch read
 * `$settings?.landingPageMode === 'chat' || <messages exist>`, so an account
 * that had ever flipped an upstream personalisation toggle skipped
 * Placeholder.svelte entirely and got upstream's ChatPlaceholder instead. The
 * serif greeting and the four quick-start chips shipped in #1161, deployed,
 * and were invisible on any such account. Nothing was broken, nothing logged,
 * and the components were correct: a stored setting was quietly deleting two
 * features.
 *
 * These are source pins because there is no component harness in this tree.
 * They are cheap and they fail loudly on exactly the shape that caused it.
 */
const here = dirname(fileURLToPath(import.meta.url));
const read = (relative: string): string => readFileSync(resolve(here, relative), 'utf8');

describe('the chat home', () => {
	const chat = read('../components/chat/Chat.svelte');

	it('decides the landing branch on messages alone, never on a stored setting', () => {
		expect(chat).toContain('{#if createMessagesList(history, history.currentId).length > 0}');
		expect(chat).not.toContain("$settings?.landingPageMode === 'chat'");
	});

	it('still mounts the Placeholder that carries the greeting and the chips', () => {
		// The other half of the same defect: a branch reached by nobody and a
		// component mounted by nobody look identical from the outside.
		expect(chat).toContain("import Placeholder from './Placeholder.svelte'");
		expect(chat).toContain('<Placeholder');
	});
});

describe('the landing surface itself', () => {
	const placeholder = read('../components/chat/Placeholder.svelte');

	it('carries the greeting and the quick-start chips by the selectors a check can find', () => {
		// Asserted by selector, not by prose: a re-score of the deployed box
		// queried `[data-hive-quickstart]` and `.hv-greeting` and found both
		// null, which is how the defect above was caught at all. Renaming
		// either without noticing would put the next check back in the dark.
		expect(placeholder).toContain('class="hv-greeting');
		expect(placeholder).toContain('<QuickStartChips');
	});

	it('seeds the composer from a chip rather than sending it', () => {
		// A chip that sent on one click spends credits on a label the user was
		// reading, and leaves no way to edit the prompt it stood for.
		expect(placeholder).toContain('messageInput?.setText(e.detail)');
	});
});

describe('the settings surface', () => {
	const settings = read('../components/chat/Settings/Interface.svelte');

	it('offers no control for a landing mode that no longer exists', () => {
		// A control that changes nothing is worse than no control. This one
		// changed something: it removed the home.
		// Asserted on the control and its state, not on the words: the comment
		// left in that file explaining the removal legitimately says the name.
		expect(settings).not.toContain('toggleLandingPageMode');
		expect(settings).not.toContain('landing-page-mode-label');
		expect(settings).not.toContain('$settings?.landingPageMode');
	});
});
