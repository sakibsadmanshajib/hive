import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { transcriptionErrorMessage } from './transcriptionError';

// Issue #1627. Every one of these used to produce an empty message, which the
// client turned into a null result and the caller read as "nothing was said".
describe('what a failed transcription tells the user', () => {
	it('uses the backend message when there is one', () => {
		expect(transcriptionErrorMessage({ detail: 'External: rate limited' })).toBe(
			'External: rate limited'
		);
	});

	it('names a timeout, which is the failure the user cannot otherwise explain', () => {
		// AbortSignal.timeout rejects with a DOMException. It carries no
		// `detail`, so this was silent: the microphone had been suppressed for
		// the whole wait and the call simply looked deaf.
		expect(transcriptionErrorMessage({ name: 'TimeoutError', message: 'signal timed out' })).toBe(
			'Transcription timed out. Please try again.'
		);
	});

	it('falls back to the transport error rather than saying nothing', () => {
		expect(transcriptionErrorMessage({ name: 'TypeError', message: 'Failed to fetch' })).toBe(
			'Failed to fetch'
		);
	});

	it('still says something when the failure carries nothing at all', () => {
		for (const empty of [null, undefined, {}, { detail: '   ' }, { message: '' }]) {
			expect(transcriptionErrorMessage(empty)).toBe('Transcription failed. Please try again.');
		}
	});

	it('keeps a non-string detail rather than dropping it', () => {
		// A backend that answers with a structured detail is still answering.
		expect(transcriptionErrorMessage({ detail: { code: 429 } })).toContain('429');
	});

	it('never returns an empty string', () => {
		const cases = [
			null,
			undefined,
			{},
			{ detail: '' },
			{ detail: '  ' },
			{ message: '' },
			{ name: 'TimeoutError' },
			{ detail: 'real' }
		];
		for (const c of cases) {
			expect(transcriptionErrorMessage(c).length).toBeGreaterThan(0);
		}
	});
});

describe('the audio client uses it (issue #1627)', () => {
	const audioApi = readFileSync(
		fileURLToPath(new URL('../apis/audio/index.ts', import.meta.url)),
		'utf8'
	);

	const transcribe = audioApi.slice(
		audioApi.indexOf('export const transcribeAudio'),
		audioApi.indexOf('export const synthesizeOpenAISpeech')
	);

	it('bounds the request, so a hung upload cannot suppress capture forever', () => {
		expect(transcribe).toMatch(/AbortSignal\.timeout\(/);
	});

	it('routes its failure through the shared decision rather than reading detail alone', () => {
		// `error = err.detail` is the bug: it is undefined for a timeout and for
		// a transport failure, and an undefined error is never thrown, so the
		// caller renders nothing.
		expect(transcribe).toContain('transcriptionErrorMessage');
		expect(transcribe).not.toMatch(/error\s*=\s*err\.detail\s*;/);
	});
});
