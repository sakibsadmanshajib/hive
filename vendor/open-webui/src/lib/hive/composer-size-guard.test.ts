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
