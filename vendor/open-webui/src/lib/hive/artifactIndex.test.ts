import { describe, expect, it } from 'vitest';

import { buildIframeDoc, withTimeout } from './artifactIndex';

describe('buildIframeDoc', () => {
	it('places each group piece in its own section of the document', () => {
		const doc = buildIframeDoc({ html: '<p>hi</p>', css: 'p { color: red; }', js: 'x();' });
		expect(doc).toContain('<p>hi</p>');
		expect(doc).toContain('p { color: red; }');
		expect(doc).toContain('x();');
		// css inside <style>, js inside <script>
		expect(doc).toContain('<style>');
		expect(doc).toContain('<script>');
	});

	it('renders an empty group as a still-valid document', () => {
		const doc = buildIframeDoc({ html: '', css: '', js: '' });
		expect(doc).toContain('<!DOCTYPE html>');
	});
});

describe('withTimeout', () => {
	it('resolves the work when it settles first', async () => {
		await expect(withTimeout(Promise.resolve('ok'), 100)).resolves.toBe('ok');
	});

	it('rejects with timeout when the work hangs', async () => {
		await expect(
			withTimeout(
				new Promise(() => {}),
				20
			)
		).rejects.toThrow('timeout');
	});
});
