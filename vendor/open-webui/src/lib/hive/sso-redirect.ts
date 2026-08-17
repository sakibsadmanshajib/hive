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
} | null | undefined;

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
const SIGNED_OUT_KEY = 'hive:signed-out';

const readStorage = (key: string): string | null => {
	try {
		return sessionStorage.getItem(key);
	} catch {
		return null;
	}
};

/** Writes and reads back, because a browser that refuses storage does not always
 * throw; some silently drop the value. A write that cannot be proved is a
 * failure, since the caller uses the answer to decide whether it is safe to
 * redirect. */
const writeStorage = (key: string, value: string): boolean => {
	try {
		sessionStorage.setItem(key, value);
		return sessionStorage.getItem(key) === value;
	} catch {
		return false;
	}
};

const clearStorage = (key: string): void => {
	try {
		sessionStorage.removeItem(key);
	} catch {
		// Nothing to clear if storage is unavailable.
	}
};

/** Reads the attempt stamp. `null` means "no usable stamp", which includes
 * storage being unreadable, so callers must treat it together with the result of
 * {@link markSsoAttempt} rather than as proof that no attempt was made. */
export const readSsoAttemptAt = (): number | null => {
	const raw = readStorage(ATTEMPT_KEY);
	if (!raw) {
		return null;
	}
	const parsed = Number.parseInt(raw, 10);
	return Number.isFinite(parsed) ? parsed : null;
};

/** Records that a round trip is starting. Returns false when the stamp could not
 * be stored, which means the loop guard is blind. The automatic path must not
 * redirect in that case: an unrecorded attempt is exactly how an endless bounce
 * starts. An explicit click may still proceed, because a person deciding to
 * retry is the bound the guard would otherwise supply. */
export const markSsoAttempt = (now: number = Date.now()): boolean =>
	writeStorage(ATTEMPT_KEY, String(now));

export const clearSsoAttempt = (): void => clearStorage(ATTEMPT_KEY);

/** Signing out has to outlive the one page load it lands on. The provider's own
 * session usually survives ours, and this provider publishes no end session
 * endpoint, so without a sticky marker the next navigation would silently sign
 * the same person back in and signing out would be unreachable. Cleared by an
 * explicit sign in, so it never blocks anyone who wants back in. */
export const markSignedOut = (): boolean => writeStorage(SIGNED_OUT_KEY, '1');

export const readSignedOut = (): boolean => readStorage(SIGNED_OUT_KEY) === '1';

export const clearSignedOut = (): void => clearStorage(SIGNED_OUT_KEY);

/** Accepts only a path inside this application. A protocol relative value such
 * as `//evil.example` or `/\\evil.example` is a different origin wearing a
 * path's clothes, and this parameter is now reachable with no interaction at
 * all. */
export const safeRedirectPath = (value: string | null | undefined): string | null => {
	if (!value || !value.startsWith('/')) {
		return null;
	}
	if (value.startsWith('//') || value.startsWith('/\\')) {
		return null;
	}
	return value;
};
