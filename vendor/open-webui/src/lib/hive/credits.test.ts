import { describe, expect, it, vi } from 'vitest';

import {
	creditState,
	CREDITS_DISMISS_KEY,
	CREDITS_PER_USD,
	creditsDismissed,
	dismissCredits,
	fetchCreditBalance,
	formatUsdFromCredits,
	refreshCreditSnapshot,
	LOW_CREDITS_THRESHOLD
} from './credits';

describe('creditState', () => {
	it('empty at zero or below', () => {
		expect(creditState(0)).toBe('empty');
		expect(creditState(-5)).toBe('empty');
	});

	it('low below the threshold, healthy at and above it', () => {
		expect(creditState(LOW_CREDITS_THRESHOLD - 1)).toBe('low');
		expect(creditState(LOW_CREDITS_THRESHOLD)).toBe('healthy');
	});

	it('threshold is $0.50 at the D-046 credit unit (1 USD = 1e9 credits)', () => {
		// Pinned to the actual scale rather than a bare literal, so a future
		// credit-unit rescale that forgets to update this threshold shows up as
		// a failing assertion here instead of a banner that never goes low.
		expect(LOW_CREDITS_THRESHOLD).toBe(0.5 * CREDITS_PER_USD);
	});
});

describe('formatUsdFromCredits', () => {
	it('renders the literal $0 for an explicit zero, never an absence', () => {
		expect(formatUsdFromCredits(0)).toBe('$0');
	});

	it('renders a whole-dollar balance cleanly', () => {
		expect(formatUsdFromCredits(9_789_478_244)).toBe('$9.79');
	});

	it('never rounds a real non-zero balance down to $0.00', () => {
		// The exact regression this replaces: a raw integer credit count. A
		// tiny but real balance must still read as a non-zero dollar figure.
		const result = formatUsdFromCredits(395_640);
		expect(result).not.toBe('$0.00');
		expect(result).not.toBe('$0');
	});

	it('never throws on non-finite input, and treats it as $0', () => {
		expect(formatUsdFromCredits(Number.NaN)).toBe('$0');
		expect(formatUsdFromCredits(Number.POSITIVE_INFINITY)).toBe('$0');
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

	it('passes through a configured top-up URL and drops empty ones', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(async () =>
				Response.json({
					available_credits: 5,
					usage_today_credits: 0,
					top_up_url: 'https://console.example/console/billing'
				})
			)
		);
		await expect(fetchCreditBalance('http://x')).resolves.toEqual({
			available_credits: 5,
			usage_today_credits: 0,
			top_up_url: 'https://console.example/console/billing'
		});
		vi.unstubAllGlobals();

		vi.stubGlobal(
			'fetch',
			vi.fn(async () => Response.json({ available_credits: 5, usage_today_credits: 0 }))
		);
		const without = await fetchCreditBalance('http://x');
		expect(without?.top_up_url).toBeUndefined();
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

describe('refreshCreditSnapshot', () => {
	const balance = { available_credits: 12_500_000_000, usage_today_credits: 340_000_000 };
	const older = new Date('2026-08-28T10:00:00Z');
	const newer = new Date('2026-08-28T11:00:00Z');

	it('takes the fetched balance and stamps the time it arrived', async () => {
		const previous = { balance: null, lastUpdated: null };
		const next = await refreshCreditSnapshot(previous, async () => balance, () => newer);
		expect(next.balance).toEqual(balance);
		expect(next.lastUpdated).toBe(newer);
	});

	it('keeps the last known good balance when a refresh fails', async () => {
		// The regression: a transient network blip blanking a real number the
		// customer is reading, which reads as credits having vanished.
		const previous = { balance, lastUpdated: older };
		const next = await refreshCreditSnapshot(previous, async () => null, () => newer);
		expect(next.balance).toEqual(balance);
	});

	it('never advances the last-updated stamp on a failed refresh', async () => {
		// A stamp that moves on failure lies about how fresh the number beside
		// it is, which is worse than a visibly old stamp.
		const previous = { balance, lastUpdated: older };
		const next = await refreshCreditSnapshot(previous, async () => null, () => newer);
		expect(next.lastUpdated).toBe(older);
	});

	it('stays empty when the very first load fails', async () => {
		const previous = { balance: null, lastUpdated: null };
		const next = await refreshCreditSnapshot(previous, async () => null, () => newer);
		expect(next.balance).toBeNull();
		expect(next.lastUpdated).toBeNull();
	});
});
