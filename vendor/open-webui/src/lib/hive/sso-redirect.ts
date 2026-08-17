/**
 * Decides whether the sign in page should hand the visitor straight to the
 * single configured identity provider instead of rendering a provider picker
 * that offers exactly one choice.
 *
 * The decision is a pure function so the dangerous half of it, the part that
 * can bounce a user between two hosts forever, is testable without a browser.
 * `+page.svelte` owns the side effects: reading the attempt stamp, writing it,
 * and navigating.
 */

export type SsoRedirectConfig = {
	oauth?: {
		auto_redirect?: boolean;
		providers?: Record<string, unknown> | null;
	} | null;
	features?: {
		auth?: boolean;
		enable_login_form?: boolean;
		enable_ldap?: boolean;
		auth_trusted_header?: boolean;
	} | null;
	onboarding?: boolean;
} | null;

export type SsoRedirectContext = {
	/** `?form=` on the sign in URL, the deliberate escape hatch to the manual page. */
	form?: string | null;
	/** `?error=` on the sign in URL, set by the backend when a round trip failed. */
	error?: string | null;
	/**
	 * `?signed_out=` on the sign in URL. Signing out lands back here while the
	 * provider's own session is usually still live, so without this the page
	 * would send the user straight back in and signing out would be impossible.
	 */
	signedOut?: boolean;
	/** A token cookie or a stored token, meaning a round trip already produced a session. */
	hasSession?: boolean;
	/** When this tab last started a provider round trip, in epoch milliseconds. */
	lastAttemptAt?: number | null;
	now: number;
};

export type SsoRedirectBlockReason =
	| 'disabled'
	| 'manual-requested'
	| 'signed-out'
	| 'provider-error'
	| 'not-single-provider'
	| 'other-auth-mode'
	| 'onboarding'
	| 'already-signed-in'
	| 'recent-attempt';

export type SsoRedirectDecision =
	| { redirect: true; provider: string }
	| { redirect: false; reason: SsoRedirectBlockReason };

/**
 * How long a started round trip suppresses the next automatic one. A provider
 * hop that works takes a few seconds at most, so anything landing back here
 * inside this window came back without a session and would otherwise bounce
 * straight out again.
 */
export const SSO_RETRY_WINDOW_MS = 15000;

export const ssoAutoRedirectDecision = (
	config: SsoRedirectConfig,
	context: SsoRedirectContext
): SsoRedirectDecision => {
	if (!config?.oauth?.auto_redirect) {
		return { redirect: false, reason: 'disabled' };
	}

	if (context.form) {
		return { redirect: false, reason: 'manual-requested' };
	}

	if (context.signedOut) {
		return { redirect: false, reason: 'signed-out' };
	}

	if (context.error) {
		return { redirect: false, reason: 'provider-error' };
	}

	const providers = Object.keys(config?.oauth?.providers ?? {});
	if (providers.length !== 1) {
		return { redirect: false, reason: 'not-single-provider' };
	}

	// Any other way in stays a real choice, so the page must keep offering it.
	const features = config?.features ?? {};
	if (
		features.auth === false ||
		features.enable_login_form !== false ||
		features.enable_ldap ||
		features.auth_trusted_header
	) {
		return { redirect: false, reason: 'other-auth-mode' };
	}

	if (config?.onboarding) {
		return { redirect: false, reason: 'onboarding' };
	}

	if (context.hasSession) {
		return { redirect: false, reason: 'already-signed-in' };
	}

	const lastAttemptAt = context.lastAttemptAt;
	if (
		typeof lastAttemptAt === 'number' &&
		Number.isFinite(lastAttemptAt) &&
		context.now - lastAttemptAt >= 0 &&
		context.now - lastAttemptAt < SSO_RETRY_WINDOW_MS
	) {
		return { redirect: false, reason: 'recent-attempt' };
	}

	return { redirect: true, provider: providers[0] };
};

const ATTEMPT_KEY = 'hive:sso-attempt-at';

/** Reads the attempt stamp. Storage is unavailable in some privacy modes, and an
 * unreadable stamp must never be the reason a sign in cannot start. */
export const readSsoAttemptAt = (): number | null => {
	try {
		const raw = sessionStorage.getItem(ATTEMPT_KEY);
		if (!raw) {
			return null;
		}
		const parsed = Number.parseInt(raw, 10);
		return Number.isFinite(parsed) ? parsed : null;
	} catch {
		return null;
	}
};

export const markSsoAttempt = (now: number = Date.now()): void => {
	try {
		sessionStorage.setItem(ATTEMPT_KEY, String(now));
	} catch {
		// ponytail: a tab that cannot store the stamp loses only the loop guard,
		// and the guard is a safety net, not a precondition for signing in.
	}
};

export const clearSsoAttempt = (): void => {
	try {
		sessionStorage.removeItem(ATTEMPT_KEY);
	} catch {
		// Nothing to clear if storage is unavailable.
	}
};
