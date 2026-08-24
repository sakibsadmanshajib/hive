import { describe, expect, it, vi } from 'vitest';

import {
	creditState,
	CREDITS_DISMISS_KEY,
	creditsDismissed,
	dismissCredits,
	fetchCreditBalance
} from './credits';

describe('creditState', () => {
	it('empty at zero or below', () => {
		expect(creditState(0)).toBe('empty');
		expect(creditState(-5)).toBe('empty');
	});

	it('low below the threshold, healthy at and above it', () => {
		expect(creditState(49_999)).toBe('low');
		expect(creditState(50_000)).toBe('healthy');
	});
});

describe('dismissal', () => {
	it('persists for the session via sessionStorage', () => {
		const store: Record<string, string> = {};
		vi.stubGlobal('sessionStorage', {
			getItem: (k: string) => store[k] ?? null,
			setItem: (k: string, v: string) => (store[k] = v)
		});
		expect(creditsDismissed()).toBe(false);
		dismissCredits();
		expect(store[CREDITS_DISMISS_KEY]).toBe('1');
		expect(creditsDismissed()).toBe(true);
		vi.unstubAllGlobals();
	});

	it('degrades silently without storage', () => {
		vi.stubGlobal('sessionStorage', undefined);
		expect(creditsDismissed()).toBe(false);
		expect(() => dismissCredits()).not.toThrow();
		vi.unstubAllGlobals();
	});
});

describe('fetchCreditBalance', () => {
	it('returns the trimmed balance on 200', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () =>
				Response.json({ available_credits: 123456, usage_today_credits: 789 })
			)
		);
		const balance = await fetchCreditBalance('http://x');
		expect(balance).toEqual({ available_credits: 123456, usage_today_credits: 789 });
		vi.unstubAllGlobals();
	});

	it('returns null on any non-200, never throwing', async () => {
		for (const status of [401, 404, 500]) {
			vi.stubGlobal(
				'fetch',
				vi.fn(async () => new Response(null, { status }))
			);
			await expect(fetchCreditBalance('http://x')).resolves.toBeNull();
			vi.unstubAllGlobals();
		}
	});

	it('returns null when the network throws', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () => {
				throw new Error('offline');
			})
		);
		await expect(fetchCreditBalance('http://x')).resolves.toBeNull();
		vi.unstubAllGlobals();
	});
});
