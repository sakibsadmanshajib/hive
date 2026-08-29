import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it, vi } from 'vitest';

import {
	creditState,
	CREDITS_DISMISS_KEY,
	CREDITS_PER_USD,
	creditsDismissed,
	dismissCredits,
	fetchCreditBalance,
	formatUsdBalanceFromCredits,
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

describe('formatUsdBalanceFromCredits', () => {
	it('rounds a balance down, so the figure never claims more money than the account holds', () => {
		// The regression (#1345): ported from the PRICE formatter, this
		// rendered ten dollars flat through round-to-nearest, and the account
		// does not hold ten dollars. A price rounds to nearest; a balance floors.
		expect(formatUsdBalanceFromCredits(9_996_364_207)).toBe('$9.99');
	});

	it('agrees with creditState at the low-credit boundary', () => {
		// The second half of #1345: 499,999,999 rendered exactly the low
		// threshold figure while creditState called the same balance low, so
		// the pill and the number contradicted each other by one credit.
		expect(creditState(LOW_CREDITS_THRESHOLD - 1)).toBe('low');
		expect(formatUsdBalanceFromCredits(LOW_CREDITS_THRESHOLD - 1)).toBe('$0.499');
		expect(creditState(LOW_CREDITS_THRESHOLD)).toBe('healthy');
		expect(formatUsdBalanceFromCredits(LOW_CREDITS_THRESHOLD)).toBe('$0.50');
	});

	it('never renders more dollars than the balance holds, at any magnitude', () => {
		// The boundary above is one instance of the general rule. Asserted
		// across magnitudes so a future precision change cannot reintroduce an
		// overstatement somewhere the named cases happen not to look, which is
		// how the first port shipped: every value it pinned divided exactly.
		const samples = [
			1, 999, 395_640, 499_999_999, 500_000_000, 9_996_364_207, 99_996_364_207,
			123_456_789_012
		];
		for (const credits of samples) {
			const shown = Number(formatUsdBalanceFromCredits(credits).replace(/[$,]/g, ''));
			expect(shown).toBeLessThanOrEqual(credits / CREDITS_PER_USD);
		}
	});

	it('rounds a negative balance down, so an overdrawn account is not flattered', () => {
		// available_credits is posted minus reserved with no clamp
		// (apps/control-plane/internal/ledger/repository.go), so outstanding
		// holds above posted credits produce a negative balance. Rounding
		// toward zero would show less of the hole than is there. Same case and
		// same expected string as the console twin's guard in
		// apps/web-console/lib/format/format.test.ts.
		expect(formatUsdBalanceFromCredits(-8_295_000_000)).toBe('-$8.30');
	});

	it('renders an empty balance as zero dollars, not as an absence', () => {
		expect(formatUsdBalanceFromCredits(0)).toBe('$0.00');
	});

	it('renders a failed decode as an absence, never as an empty wallet', () => {
		expect(formatUsdBalanceFromCredits(Number.NaN)).toBe('—');
		expect(formatUsdBalanceFromCredits(Number.POSITIVE_INFINITY)).toBe('—');
	});

	it('keeps a tiny real balance visible rather than collapsing it to zero', () => {
		// Nine decimals is the exact width of one credit, so no non-zero
		// balance can floor to the same string an empty wallet renders.
		expect(formatUsdBalanceFromCredits(1)).toBe('$0.000000001');
	});
});

describe('the composer banner wiring', () => {
	it('formats remaining with the balance formatter and spend with the price formatter', () => {
		// Source level rather than rendered: CreditsBanner takes no props and
		// only populates itself in onMount, which a server side render never
		// runs, so its rendered output holds no figure to assert. This pins the
		// one mutation that reintroduces #1345, putting the remaining figure
		// back on the round-to-nearest price formatter.
		const src = readFileSync(
			fileURLToPath(new URL('./CreditsBanner.svelte', import.meta.url)),
			'utf8'
		);
		expect(src).toContain('remaining: formatUsdBalanceFromCredits(balance?.available_credits ?? 0)');
		expect(src).toContain('used: formatUsdFromCredits(balance?.usage_today_credits ?? 0)');
	});
});
