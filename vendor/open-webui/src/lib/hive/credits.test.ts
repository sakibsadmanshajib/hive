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
	formatCreditAmount,
	refreshCreditSnapshot,
	LOW_CREDITS_THRESHOLD
} from './credits';
import { CURRENCY_MARK } from './currency-mark';

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

/**
 * The guard for issue #1694, on the chat side.
 *
 * Both figures this front end shows, the balance and today's spend, render as
 * Hive credits with no currency at all (owner ruling, .wolf/decisions.md
 * D-070). They used to go through two currency formatters whose rounding
 * policies had to be kept apart by hand, which is how #1344 and #1345 shipped.
 */
describe('formatCreditAmount', () => {
	it('renders the exact credit count and its unit', () => {
		expect(formatCreditAmount(9_996_364_207)).toBe('9,996,364,207 credits');
		expect(formatCreditAmount(395_640)).toBe('395,640 credits');
		expect(formatCreditAmount(1)).toBe('1 credit');
	});

	it('never emits a currency mark, at any magnitude or sign', () => {
		const samples = [
			0, 1, 858, 999, 395_640, 499_999_999, 500_000_000, 9_996_364_207, 99_996_364_207,
			123_456_789_012, -8_295_000_000, Number.NaN, Number.POSITIVE_INFINITY
		];
		for (const credits of samples) {
			expect(formatCreditAmount(credits)).not.toMatch(CURRENCY_MARK);
		}
	});

	it('is exact, so no balance is overstated and no real figure reads as empty', () => {
		// #1345 in one line: the balance went through the price formatter's
		// round-to-nearest and claimed ten dollars the account did not hold.
		// An integer count of credits has no rounding to get wrong, and no
		// sub-cent case that has to be replaced by a bound.
		expect(formatCreditAmount(9_996_364_207)).not.toBe('10,000,000,000 credits');
		expect(formatCreditAmount(858)).toBe('858 credits');
	});

	it('renders an empty balance as zero credits, not as an absence', () => {
		expect(formatCreditAmount(0)).toBe('0 credits');
	});

	it('renders a negative balance in full, so an overdrawn account is not flattered', () => {
		// available_credits is posted minus reserved with no clamp
		// (apps/control-plane/internal/ledger/repository.go), so outstanding
		// holds above posted credits produce a negative balance.
		expect(formatCreditAmount(-8_295_000_000)).toBe('-8,295,000,000 credits');
	});

	it('renders a failed decode as an absence, never as an empty wallet', () => {
		expect(formatCreditAmount(Number.NaN)).toBe('—');
		expect(formatCreditAmount(Number.POSITIVE_INFINITY)).toBe('—');
	});

	it('agrees with creditState at the low-credit boundary', () => {
		// The second half of #1345: the pill and the number contradicted each
		// other by one credit. They cannot now, because the number is the
		// credit count creditState itself reads.
		expect(creditState(LOW_CREDITS_THRESHOLD - 1)).toBe('low');
		expect(formatCreditAmount(LOW_CREDITS_THRESHOLD - 1)).toBe('499,999,999 credits');
		expect(creditState(LOW_CREDITS_THRESHOLD)).toBe('healthy');
		expect(formatCreditAmount(LOW_CREDITS_THRESHOLD)).toBe('500,000,000 credits');
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

describe('the composer banner wiring', () => {
	it('formats both figures as credits, and imports no currency formatter', () => {
		// Source level rather than rendered: CreditsBanner takes no props and
		// only populates itself in onMount, which a server side render never
		// runs, so its rendered output holds no figure to assert. This pins the
		// mutation that reintroduces #1694, putting either figure back on a
		// currency formatter.
		const src = readFileSync(
			fileURLToPath(new URL('./CreditsBanner.svelte', import.meta.url)),
			'utf8'
		);
		expect(src).toContain('remaining: formatCreditAmount(balance?.available_credits ?? 0)');
		expect(src).toContain('used: formatCreditAmount(balance?.usage_today_credits ?? 0)');
		expect(src).not.toMatch(/formatUsd|Intl\.NumberFormat/);
		// A formatter swapped back in is not the only way the leak returns on
		// this component. A currency mark typed straight into the i18n message
		// passes every assertion above and the whole file, and this banner is
		// the one surface with no rendered guard at all, because it populates
		// in onMount and a server render holds no figure to assert. A blanket
		// scan of the source is not available (every `$i18n` and every store
		// reference would match), so the message literal is scanned on its own.
		// Both quote styles: this component uses double quotes for the two
		// messages that contain an apostrophe and single quotes elsewhere, and a
		// scan that saw only one style would silently skip the other.
		const messages = [
			...src.matchAll(/\$i18n\.t\(\s*(?:'([^']+)'|"([^"]+)")/g)
		].map((m) => m[1] ?? m[2]);
		// Anti vacuity: the banner has more than one message and the known one
		// must be among them, so a changed call shape cannot empty this list
		// and pass the scan below with nothing in it.
		expect(messages.length).toBeGreaterThan(1);
		expect(messages.some((m) => m.includes('remaining'))).toBe(true);
		for (const message of messages) {
			expect(message).not.toMatch(CURRENCY_MARK);
		}
	});

	it('leaves no currency formatter on the Usage tab either', () => {
		// The same guard on the other surface that reads this module. Both are
		// checked, because a fix applied to one and not the other is exactly
		// how the two front ends diverged before.
		const src = readFileSync(
			fileURLToPath(new URL('./SettingsUsage.svelte', import.meta.url)),
			'utf8'
		);
		expect(src).toContain('formatCreditAmount(balance.available_credits)');
		expect(src).toContain('formatCreditAmount(balance.usage_today_credits)');
		expect(src).not.toMatch(/formatUsd|Intl\.NumberFormat/);
	});
});
