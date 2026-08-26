/*
 * The name a signed-in user's session should actually render, everywhere the
 * app shows one: the chat greeting, the sidebar, the account menu, the
 * composer.
 *
 * `deploy/docker/owui-patches/hive_display_name.py` already derives a display
 * name from the email local part at OAuth signup, when the identity provider
 * sends no username claim (source of truth for that derivation: keep the two
 * in sync by hand, they cannot share a module across the Python backend /
 * Svelte frontend build boundary). That patch only fires once, in the
 * `handle_callback` signup branch, so it does not help:
 *
 *   - an account already provisioned before the patch shipped, whose `name`
 *     column already holds the raw email address;
 *   - a local email/password account, which never goes through `oauth.py`'s
 *     `handle_callback` at all.
 *
 * `qa-tester@hive.test` reaching the chat greeting ("Good morning,
 * qa-tester@hive.test") on 2026-08-26 was exactly the first case: an
 * already-provisioned fixture account.
 *
 * This is the belt-and-braces frontend guard: it runs wherever a session
 * user is written to the `user` store (see the wrapped `set`/`update` in
 * stores/index.ts, the one place every sign-in, sign-up, LDAP, and
 * session-refresh path converges), so a raw email can never reach a rendered
 * display name regardless of how or when the account was provisioned.
 */

const SEPARATORS = /[._-]+/;
const MAX_LENGTH = 64;

// Control, format, private-use, surrogate, line-separator, and
// paragraph-separator categories: the same bidirectional-override-capable
// characters hive_display_name.py's `_sanitize` strips via
// `unicodedata.category`, expressed as a Unicode property escape plus the
// two literal separator code points (Zl/Zp aren't in the Cc/Cf/Co/Cs set).
const UNSAFE_CHARACTERS = /[\p{Cc}\p{Cf}\p{Co}\p{Cs}\u2028\u2029]/gu;

function sanitize(value: string): string {
	return value.replace(UNSAFE_CHARACTERS, '');
}

function capitalize(word: string): string {
	return word.length === 0 ? word : word[0].toUpperCase() + word.slice(1).toLowerCase();
}

/**
 * Best-effort human name derived from an email address's local part.
 * `sakib.shajib@example.com` becomes "Sakib Shajib". Never throws, and never
 * returns an empty string for a non-empty address: a bad display name is a
 * cosmetic problem, and refusing to render one would not be.
 */
export function displayNameFromEmail(email: string): string {
	const trimmed = (email ?? '').trim();
	if (!trimmed) return '';

	let local = trimmed.split('@', 1)[0];
	// A quoted local part is legal but rare; the quotes are syntax, not a name.
	local = local.replace(/^"+|"+$/g, '');
	// Plus addressing is a routing tag, not part of the person's name.
	local = local.split('+', 1)[0];

	const words = sanitize(local)
		.split(SEPARATORS)
		.filter((word) => word.length > 0);

	if (words.length === 0) {
		// A local part made only of separator/control characters (".", "___",
		// a bidi override with nothing else) leaves nothing to build a word
		// from. Falls back to the sanitized LOCAL PART only, deliberately
		// diverging here from hive_display_name.py's Python original, which
		// falls back to the full sanitized address. That file's own docstring
		// accepts "falls back to the address itself" as a lesser evil for a
		// cosmetic display name; this module's contract is stricter, per the
		// module doc above: it must NEVER hand back a string containing '@',
		// and the full address always does. If even the local part sanitizes
		// away entirely, a neutral literal is what is left.
		const fallback = sanitize(local).slice(0, MAX_LENGTH).trim();
		return /[a-zA-Z0-9]/.test(fallback) ? fallback : 'User';
	}

	return words
		.map(capitalize)
		.join(' ')
		.slice(0, MAX_LENGTH)
		.trim();
}

/**
 * The display name a session should render: the server-provided name when it
 * looks like a real name, or one derived from an email address when it does
 * not.
 *
 * "Does not" covers two cases, both root-caused back to the same rule (never
 * render a raw email address as a name): the name field is empty, and the
 * name field itself looks like an email address (contains `@`) -- which
 * covers both "the server sent back the account's own login email" and any
 * other email-shaped string a future backend regression might send. In
 * either case, derive from whichever string looks like the email: the name
 * field if it is the email-shaped one, otherwise the email field.
 */
export function resolveDisplayName(
	name: string | null | undefined,
	email: string | null | undefined
): string {
	const trimmedName = sanitize((name ?? '').trim()).trim();
	const trimmedEmail = (email ?? '').trim();

	const nameLooksLikeEmail = trimmedName.includes('@');
	if (trimmedName && !nameLooksLikeEmail) {
		return trimmedName;
	}

	const derived = displayNameFromEmail(nameLooksLikeEmail ? trimmedName : trimmedEmail);
	return derived || trimmedName || 'User';
}
