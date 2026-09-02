import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import {
	COWORK_ATTACHMENT_MAX_COUNT,
	COWORK_ATTACHMENT_MAX_TOTAL_BYTES,
	attachmentFileName,
	collectCoworkAttachments,
	isCoworkAttachable
} from './coworkAttachments';

// Issue #1065. Work mode refused every attachment, so the most obvious thing a
// person tries in the composer had no path at all on that half of the surface.
// These pin the shape the run request carries, and the refusals that have to
// happen before the send rather than as a 400 the person cannot read.

const neverRead = async (): Promise<string> => {
	throw new Error('readContent should not have been called');
};

describe('isCoworkAttachable', () => {
	it('accepts an uploaded document and a temporary-chat extraction', () => {
		expect(isCoworkAttachable({ type: 'file' })).toBe(true);
		expect(isCoworkAttachable({ type: 'text' })).toBe(true);
	});

	// A collection, a note, another chat and a folder are references to rows in
	// Open WebUI's own database. The sandbox cannot resolve any of them, so
	// they keep the refusal they always had instead of being dropped quietly.
	it('rejects every reference the sandbox cannot resolve', () => {
		for (const type of ['image', 'collection', 'chat', 'note', 'folder', undefined]) {
			expect(isCoworkAttachable({ type })).toBe(false);
		}
		expect(isCoworkAttachable(null)).toBe(false);
	});
});

describe('attachmentFileName', () => {
	it('keeps an ordinary file name, spaces and all', () => {
		expect(attachmentFileName('Q3 inventory.txt')).toBe('Q3 inventory.txt');
		expect(attachmentFileName('  report.pdf  ')).toBe('report.pdf');
	});

	it('refuses anything that is a path rather than a file name', () => {
		for (const raw of ['', '   ', '.', '..', '../escape.txt', 'nested/file.txt', 'back\\slash.txt']) {
			expect(attachmentFileName(raw)).toBe('');
		}
	});

	// The engine measures the name in UTF-8 bytes and refuses DEL as well as
	// the C0 range. A browser check that disagreed would accept a name the
	// backend then refuses, after the composer has already been cleared, which
	// costs the person their message for no reason they can see.
	it('measures the name in UTF-8 bytes, the way the engine does', () => {
		// 100 four-byte characters: 100 UTF-16 code units, 400 UTF-8 bytes.
		expect(attachmentFileName('\u{1F642}'.repeat(100))).toBe('');
		// Comfortably under either count, and kept.
		expect(attachmentFileName('\u{1F642}.txt')).toBe('\u{1F642}.txt');
	});

	it('refuses DEL, which sits above the C0 range', () => {
		expect(attachmentFileName('a\u007fb.txt')).toBe('');
	});

	it('refuses control characters and over-long names', () => {
		expect(attachmentFileName('a\tb.txt')).toBe('');
		expect(attachmentFileName('a\nb.txt')).toBe('');
		expect(attachmentFileName(`${'a'.repeat(256)}.txt`)).toBe('');
	});
});

describe('collectCoworkAttachments', () => {
	it('carries nothing when nothing is attached', async () => {
		await expect(collectCoworkAttachments([], neverRead)).resolves.toEqual({
			ok: true,
			attachments: []
		});
	});

	// The content is what has to arrive. A result carrying a name and an id
	// would pass a naive assertion and still hand the sandbox nothing.
	it('reads the extracted text back for an uploaded file', async () => {
		const result = await collectCoworkAttachments(
			[{ type: 'file', id: 'file-1', name: 'inventory.txt' }],
			async (id) => (id === 'file-1' ? 'QAFILE7731' : '')
		);
		expect(result).toEqual({
			ok: true,
			attachments: [{ name: 'inventory.txt', content: 'QAFILE7731' }]
		});
	});

	it('prefers text already on the item over a second read', async () => {
		const result = await collectCoworkAttachments(
			[{ type: 'text', id: 'local-1', name: 'pasted.txt', content: 'already here' }],
			neverRead
		);
		expect(result).toEqual({
			ok: true,
			attachments: [{ name: 'pasted.txt', content: 'already here' }]
		});
	});

	it('uses the content the upload response already carried', async () => {
		const result = await collectCoworkAttachments(
			[{ type: 'file', id: 'file-2', name: 'notes.md', file: { data: { content: 'from upload' } } }],
			neverRead
		);
		expect(result).toEqual({
			ok: true,
			attachments: [{ name: 'notes.md', content: 'from upload' }]
		});
	});

	// Refusing beats sending an empty document: an agent handed nothing answers
	// from nothing, and the person reads that as a bad model rather than a
	// missing file.
	it('refuses a file whose text never arrived', async () => {
		const result = await collectCoworkAttachments(
			[{ type: 'file', id: 'file-3', name: 'pending.pdf' }],
			async () => ''
		);
		expect(result).toEqual({ ok: false, reason: 'empty', name: 'pending.pdf' });
	});

	it('refuses an attachment the sandbox cannot resolve', async () => {
		const result = await collectCoworkAttachments(
			[{ type: 'collection', id: 'kb-1', name: 'Policies' }],
			neverRead
		);
		expect(result).toEqual({ ok: false, reason: 'unsupported', name: 'Policies' });
	});

	it('refuses a name that is a path', async () => {
		const result = await collectCoworkAttachments(
			[{ type: 'file', id: 'file-4', name: '../escape.txt' }],
			neverRead
		);
		expect(result).toEqual({ ok: false, reason: 'unsupported', name: '../escape.txt' });
	});

	it('refuses more attachments than a run carries', async () => {
		const files = Array.from({ length: COWORK_ATTACHMENT_MAX_COUNT + 1 }, (_, i) => ({
			type: 'file',
			id: `f${i}`,
			name: `f${i}.txt`
		}));
		await expect(collectCoworkAttachments(files, async () => 'x')).resolves.toEqual({
			ok: false,
			reason: 'too-many'
		});
	});

	// The cap is on the combined text, so two files that each fit and together
	// do not is the case that matters.
	it('refuses when the combined text passes the cap', async () => {
		const half = 'a'.repeat(COWORK_ATTACHMENT_MAX_TOTAL_BYTES / 2 + 1);
		const result = await collectCoworkAttachments(
			[
				{ type: 'file', id: 'a', name: 'a.txt' },
				{ type: 'file', id: 'b', name: 'b.txt' }
			],
			async () => half
		);
		expect(result).toEqual({ ok: false, reason: 'too-large' });
	});

	// Multi-byte characters are why the cap is measured in bytes here and in
	// Go rather than in JavaScript string length, which would let a Bengali or
	// emoji-heavy document through at three times the size.
	it('measures the cap in bytes, not code units', async () => {
		const emoji = '🙂'.repeat(COWORK_ATTACHMENT_MAX_TOTAL_BYTES / 4);
		expect(emoji.length).toBeLessThan(COWORK_ATTACHMENT_MAX_TOTAL_BYTES);
		const result = await collectCoworkAttachments(
			[
				{ type: 'file', id: 'a', name: 'a.txt' },
				{ type: 'file', id: 'b', name: 'b.txt' }
			],
			async () => emoji
		);
		expect(result).toEqual({ ok: false, reason: 'too-large' });
	});
});

// Source-level guard, the same device settings-declutter.test.ts uses: this
// repo has no component-test harness for Chat.svelte, and the whole defect is
// that the composer refused the send before any of the above ran.
describe('the composer no longer refuses every attachment in Work mode', () => {
	const chat = readFileSync(
		fileURLToPath(new URL('../components/chat/Chat.svelte', import.meta.url)),
		'utf8'
	);

	it('keeps the blanket refusal out of the cowork branch', () => {
		expect(chat).not.toContain('Attachments are not supported in Work mode yet');
	});

	it('collects the attachments and hands them to the run', () => {
		expect(chat).toContain('collectCoworkAttachments');
		expect(chat).toContain('createTask(localStorage.token, $composerPack, userPrompt, attachments)');
	});
});
