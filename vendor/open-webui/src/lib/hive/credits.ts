/*
 * The signed-in chat user's remaining credits, for the composer banner.
 */

export const DEFAULT_CREDITS_API_BASE_URL = '/api/v1/hive/credits';

export interface CreditBalance {
	/** Whole credits left on the tenant billing account (100000 credits = $1). */
	available_credits: number;
	/** Usage charges posted since midnight UTC today. */
	usage_today_credits: number;
}

export type CreditState = 'healthy' | 'low' | 'empty';

/** 50000 credits = $0.50 at CREDITS_PER_USD = 100000. Below this reads as low. */
export const LOW_CREDITS_THRESHOLD = 50_000;

export function creditState(available: number): CreditState {
	if (!Number.isFinite(available) || available <= 0) return 'empty';
	if (available < LOW_CREDITS_THRESHOLD) return 'low';
	return 'healthy';
}

export function formatCredits(amount: number): string {
	if (!Number.isFinite(amount)) return '0';
	return Math.round(amount).toLocaleString('en-US');
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
		return {
			available_credits: available,
			usage_today_credits: Number.isFinite(usageToday) ? usageToday : 0
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
