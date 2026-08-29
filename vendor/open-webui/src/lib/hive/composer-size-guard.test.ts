import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

// Regression guard for #1108 (composer submit silently no-ops on large
// pastes / attachments). The repo has no component-test harness for the chat
// frontend, so like settings-declutter.test.ts this pins behavior at source
// level:
//
//   1. uploadFileHandler must enforce $config.file.max_size itself. Large
//      pasted text and drag-dropped files reach it directly, bypassing
//      inputFilesHandler's guard, so an oversized attachment used to land as
//      a chip that could never send.
//   2. The chat:message:error socket handler must toast the error content.
//      Setting message state alone read as a silently no-op send.

const component = (rel: string): string =>
	readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

const between = (src: string, startMarker: string, endMarker: string): string => {
	const start = src.indexOf(startMarker);
	const end = src.indexOf(endMarker, start);
	if (start === -1 || end === -1) {
		throw new Error(`markers not found: ${startMarker}, ${endMarker}`);
	}
	return src.slice(start, end);
};

describe('attachment size guard (#1108)', () => {
	it('uploadFileHandler enforces the configured max_size before a chip is created', () => {
		const handler = between(
			component('../components/chat/MessageInput.svelte'),
			'const uploadFileHandler',
			'const inputFilesHandler'
		);

		expect(handler).toContain('$config?.file?.max_size');
		expect(handler).toContain('file.size > ($config?.file?.max_size ?? 0) * 1024 * 1024');
		expect(handler).toContain('toast.error');
	});

	it('the paste path routes through uploadFileHandler, which now carries the guard', () => {
		const src = component('../components/chat/MessageInput.svelte');
		expect(src).toContain('await uploadFileHandler(file, true, { context: \'full\' })');
	});
});

describe('the composer control row wraps rather than overlapping (#1349)', () => {
	// At 375px the Chat/Cowork toggle overflowed the left group and painted
	// over the model chip: "Cowork" occupied x124 to x199 and "Hive Auto"
	// x145 to x272, fifty-four pixels of overlap with both labels drawn on
	// top of each other. The group shrank (`flex-1 min-w-0`) while its own
	// children could not (`shrink-0` on the plus button, `flex-shrink: 0` on
	// `.hv-mode` in hive.css), so the shrinking moved the content out of the
	// box rather than making it narrower.
	//
	// Source level rather than rendered, like the guards above: the row's
	// geometry only exists in a browser, and the repo has no layout harness
	// for the chat frontend. What is pinned here is the pair of declarations
	// that make wrapping possible at all, either of which reverting alone
	// brings the overlap back. The measured proof is the 375px screenshot on
	// the pull request.
	const row = (): string =>
		between(
			component('../components/chat/MessageInput.svelte'),
			'hive (#1349): this row wraps',
			'<ComposerModeToggle />'
		);

	it('the control row is allowed to wrap', () => {
		// The parent's own class list, whole. A substring match anywhere in the
		// block would still pass if `flex-wrap` were moved onto a child, which
		// wraps nothing here and brings the overlap straight back.
		expect(row()).toContain(
			'class=" flex flex-wrap gap-y-1.5 justify-between mt-0.5 mb-2.5 mx-0.5 max-w-full"'
		);
	});

	it('the left group takes a full line below sm, so the model chip wraps under it', () => {
		// The whole class list, not a substring of it. `flex-1` is
		// `flex: 1 1 0%`, and a zero basis never forces a wrap: it shrinks the
		// group to nothing instead and the overflow returns, which is the one
		// mutation that reintroduces the defect.
		expect(row()).toContain('grow shrink basis-full sm:basis-0 min-w-0');
	});
});

describe('completion errors are visible (#1108)', () => {
	it('chat:message:error toasts the error content instead of only setting state', () => {
		const branch = between(
			component('../components/chat/Chat.svelte'),
			"type === 'chat:message:error'",
			"chat:message:follow_ups"
		);

		expect(branch).toContain('message.error = data.error;');
		expect(branch).toContain('toast.error');
	});
});
