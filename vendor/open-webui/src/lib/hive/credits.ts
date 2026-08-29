/*
 * The signed-in chat user's remaining credits, for the composer banner.
 */

export const DEFAULT_CREDITS_API_BASE_URL = '/api/v1/hive/credits';

export interface CreditBalance {
	/** Whole credits left on the tenant billing account (1,000,000,000 credits = $1, D-046). */
	available_credits: number;
	/** Usage charges posted since midnight UTC today. */
	usage_today_credits: number;
	/**
	 * Deployment-configured top-up destination; empty means the deployment
	 * named none and the banner shows the warning without a link.
	 */
	top_up_url?: string;
}

export type CreditState = 'healthy' | 'low' | 'empty';

/**
 * One US dollar equals one billion Hive credits (`.wolf/decisions.md`
 * D-046, migration `20260823_40_credit_unit_rescale_billion.sql`). Ported
 * from `apps/web-console/lib/format/model-pricing.ts`'s `CREDITS_PER_USD`,
 * the source of truth for this conversion: the two cannot share a module
 * across the Node/Next.js console and this separate SvelteKit chat build, so
 * keep any future rate change in sync by hand.
 */
export const CREDITS_PER_USD = 1_000_000_000;

/**
 * 500,000,000 credits = $0.50 at CREDITS_PER_USD above. Below this reads as
 * low. Was 50,000 (=$0.50 at the pre-D-046 rate of 100,000 credits per
 * dollar); left unscaled through the D-046 rescale, this threshold could
 * never trip again at the new credit magnitude, silently disabling the
 * low/empty banner states.
 */
export const LOW_CREDITS_THRESHOLD = 500_000_000;

/**
 * What a positive figure below one cent reads as. A bound, not a figure: at
 * two decimals such an amount would render "$0.00", which is what an empty
 * wallet renders, and the point of saying anything at all is that the wallet
 * is not empty. Twin of the console's constant of the same name in
 * `apps/web-console/lib/format/credits.ts`.
 */
export const SUB_CENT_BALANCE = '< $0.01';

export function creditState(available: number): CreditState {
	if (!Number.isFinite(available) || available <= 0) return 'empty';
	if (available < LOW_CREDITS_THRESHOLD) return 'low';
	return 'healthy';
}

/**
 * Render a credit figure that is NOT a balance (today's spend, on the banner
 * and in the Usage tab) as US dollars, rather than the raw billions-scale
 * integer the credit unit rescale (D-046) turned every figure into
 * ("9,789,478,244 remaining" told a customer nothing).
 *
 * Rounds to nearest, which is the one behavioural difference from
 * formatUsdBalanceFromCredits below: a spend figure is not a claim about what
 * is left to spend, so nothing is overstated by rounding it either way.
 *
 * Two decimals, always. This used to carry the console PRICE formatter's
 * per-value precision, which is right for a published per-million rate and
 * wrong here: a day whose spend was 858 credits printed "$0.000000858" on the
 * composer banner and again in Settings, Usage, nine significant figures in
 * permanent chrome that no reader can act on. What that precision was
 * protecting is kept: a real, non-zero figure still never renders as the
 * literal `$0` an explicit zero renders, because a sub-cent amount reads as
 * SUB_CENT_BALANCE, a bound rather than a figure.
 *
 * A BALANCE does not go through this. It floors instead, through
 * formatUsdBalanceFromCredits below, because a figure that rounds up tells a
 * customer they hold money they cannot spend.
 */
export function formatUsdFromCredits(credits: number): string {
	if (!Number.isFinite(credits) || credits === 0) {
		return '$0';
	}
	const creditsPerCent = CREDITS_PER_USD / 100;
	const cents = Math.round(credits / creditsPerCent);
	if (cents === 0) {
		// Only a sub-cent figure lands here, and usage is never negative, but
		// the sign is honoured rather than assumed: a bound pointing the wrong
		// way would be worse than the raw number it replaced.
		return credits > 0 ? SUB_CENT_BALANCE : '> -$0.01';
	}
	return new Intl.NumberFormat('en-US', {
		style: 'currency',
		currency: 'USD',
		minimumFractionDigits: 2,
		maximumFractionDigits: 2
	}).format(cents / 100);
}

/**
 * Render a credit BALANCE as US dollars for the composer banner and the
 * Usage tab.
 *
 * Rounded down, never up, which is the one behavioural difference from
 * formatUsdFromCredits above. A catalog rate is a published price and rounds
 * to the nearest cent; an available balance is spendable money, and rounding
 * 9,996,364,207 credits up to "$10.00" tells a customer they hold more than
 * they can spend. Down rather than toward zero, so that a balance driven
 * negative by reservations overstates the hole rather than flattering it:
 * `available = posted - reserved` has no clamp
 * (apps/control-plane/internal/ledger/repository.go), so an account whose
 * holds exceed its posted credits reads negative.
 *
 * Two decimals, always, for the reason given on formatUsdFromCredits above. A
 * real, non-zero balance still never renders as the "$0.00" an empty wallet
 * renders: a positive balance under one cent reads as SUB_CENT_BALANCE. A
 * negative one needs no such case, because flooring moves it away from zero,
 * so one credit overdrawn already floors to "-$0.01".
 *
 * This is a byte-identical twin of the console's function of the same name in
 * `apps/web-console/lib/format/credits.ts`. The two builds cannot share a
 * module (see the module comment above), so
 * tools/lint-credit-balance-formatter-parity.mjs fails the build when the two
 * stop matching, which is how they diverged into #1344 and #1345.
 */
export function formatUsdBalanceFromCredits(credits: number): string {
	// Zero is a real, readable balance. A non-finite value is not: it can only
	// come from a decode that failed, and rendering that as "$0.00" would assert
	// an empty wallet where nothing was read at all. Same policy, and the same
	// em dash, as formatPercent above.
	if (!Number.isFinite(credits)) {
		return '—';
	}
	if (credits === 0) {
		return '$0.00';
	}
	// Floor in credits rather than in dollars. The dollar product is a float:
	// 8,290,000,000 credits is 8.29 dollars, 8.29 times 100 is
	// 828.9999999999999, and flooring that prints $8.28, understating a real
	// balance by a cent. CREDITS_PER_USD is a power of ten, so creditsPerCent is
	// an exact integer and this is exact for every integer balance.
	const creditsPerCent = CREDITS_PER_USD / 100;
	const cents = Math.floor(credits / creditsPerCent);
	if (cents === 0) {
		return SUB_CENT_BALANCE;
	}
	return new Intl.NumberFormat('en-US', {
		style: 'currency',
		currency: 'USD',
		minimumFractionDigits: 2,
		maximumFractionDigits: 2
	}).format(cents / 100);
}

/**
 * One fetch, silent on every failure: a null return is the "no banner"
 * answer, never an error surface. The browser holds no credential any Hive
 * service accepts, so this goes to Open WebUI's own backend, which resolves
 * the signed-in principal server side (deploy/docker/owui-patches/hive_credits.py).
 */
export async function fetchCreditBalance(
	baseUrl: string = DEFAULT_CREDITS_API_BASE_URL
): Promise<CreditBalance | null> {
	try {
		const response = await fetch(`${baseUrl}/balance`, { credentials: 'same-origin' });
		if (!response.ok) return null;
		const data = await response.json();
		const available = Number(data?.available_credits);
		if (!Number.isFinite(available)) return null;
		const usageToday = Number(data?.usage_today_credits);
		const topUpUrl = typeof data?.top_up_url === 'string' ? data.top_up_url : '';
		return {
			available_credits: available,
			usage_today_credits: Number.isFinite(usageToday) ? usageToday : 0,
			top_up_url: topUpUrl || undefined
		};
	} catch {
		// Network failure, offline, session expired: degrade to no banner.
		return null;
	}
}

/** Dismissal lasts for the browsing session only; sessionStorage. */
export const CREDITS_DISMISS_KEY = 'hive.credits.banner.dismissed';

export function creditsDismissed(): boolean {
	try {
		return sessionStorage.getItem(CREDITS_DISMISS_KEY) === '1';
	} catch {
		return false;
	}
}

export function dismissCredits(): void {
	try {
		sessionStorage.setItem(CREDITS_DISMISS_KEY, '1');
	} catch {
		/* storage unavailable: banner simply reappears next mount */
	}
}

/**
 * What the Usage tab holds between refreshes: the last balance that actually
 * arrived, and the moment it arrived.
 */
export interface CreditSnapshot {
	balance: CreditBalance | null;
	lastUpdated: Date | null;
}

/**
 * The Usage tab's refresh policy, kept in this module rather than inline in
 * the component so it is executable in a test rather than only readable in a
 * diff.
 *
 * Two invariants, both learned the hard way on a money surface:
 *
 *   * A failed refresh must never wipe a balance already on screen.
 *     fetchCreditBalance returns null for every failure, including a network
 *     blip and an expired session, and blanking a real number on a transient
 *     failure tells the customer their credits vanished.
 *   * The last-updated stamp must never advance on a refresh that learned
 *     nothing. A stamp that moves on failure is a lie about how fresh the
 *     number beside it is, which is worse than a visibly old stamp.
 *
 * fetchBalance and now are injectable purely so the two invariants above can
 * be asserted without a network or a clock; the app always takes the
 * defaults.
 */
export async function refreshCreditSnapshot(
	previous: CreditSnapshot,
	fetchBalance: () => Promise<CreditBalance | null> = fetchCreditBalance,
	now: () => Date = () => new Date()
): Promise<CreditSnapshot> {
	const fetched = await fetchBalance();
	if (fetched === null) return previous;
	return { balance: fetched, lastUpdated: now() };
}
