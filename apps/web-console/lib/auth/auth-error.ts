/**
 * Sanitization boundary for authentication error copy.
 *
 * web-console is the last hop before the browser. GoTrue's error strings are
 * produced upstream (and, when an auth hook fails, come straight out of
 * Postgres), so we cannot fix the wording at the source. This is where it gets
 * cleaned instead: every raw auth error rendered to a user passes through
 * toUserFacingAuthMessage first.
 *
 * This is an ALLOW-LIST, not a deny-list, and the distinction is the whole
 * point. A deny-list fails open: it leaks every string nobody thought to add a
 * marker for, and it has to be extended each time somebody finds a new leak,
 * which is the same shape as the bug this file exists because of (a raw
 * `pg-functions://postgres/public/custom_access_token_hook` rendered to
 * unauthenticated visitors). An allow-list fails closed: an unrecognized
 * message degrades to generic copy, which is a worse experience and never a
 * disclosure. Constraint violations, syntax errors, relation and column names,
 * connection strings and panics are all handled by the same default, without
 * anyone having to predict them.
 *
 * The cost is real and accepted: a GoTrue message that is genuinely useful but
 * not listed below shows as generic copy until someone adds it. That is a UX
 * regression a user can report, rather than a disclosure a user will not.
 */

const GENERIC_MESSAGE =
  "Something went wrong on our end. Please try again in a moment, and contact support if it keeps happening.";

/**
 * Messages known to be safe to show verbatim: GoTrue's own user-facing auth
 * copy. Anchored and bounded on purpose, so a longer string that merely starts
 * with a safe prefix does not pass. Sourced from the GoTrue v2 error surface
 * the console actually exercises (password sign-in, sign-up, email
 * confirmation, password reset, OAuth consent).
 *
 * Add a pattern here only for text that is already meant for an end user. If it
 * names a database object, a host, a file, or an internal identifier, it does
 * not belong.
 */
const SAFE_MESSAGE_PATTERNS: readonly RegExp[] = [
  // Sign-in.
  /^invalid( login)? credentials\.?$/i,
  /^email not confirmed\.?$/i,
  /^user not found\.?$/i,
  /^anonymous sign-ins are disabled\.?$/i,
  // Sign-up.
  /^user already registered\.?$/i,
  /^a user with this email address has already been registered\.?$/i,
  /^signups not allowed for this instance\.?$/i,
  /^email address .{1,80} is invalid\.?$/i,
  /^unable to validate email address: invalid format\.?$/i,
  // Password policy.
  /^password should be at least \d{1,3} characters\.?$/i,
  /^password should contain [a-z ,.:-]{1,80}\.?$/i,
  /^new password should be different from the old password\.?$/i,
  // Tokens and links.
  /^token has expired or is invalid\.?$/i,
  /^email link is invalid or has expired\.?$/i,
  /^invalid or expired (otp|token)\.?$/i,
  // Throttling.
  /^email rate limit exceeded\.?$/i,
  /^over (email|sms) send rate limit\.?$/i,
  /^for security purposes, you can only request this after \d{1,4} seconds?\.?$/i,
  /^request rate limit reached\.?$/i,
  // Consent and OAuth, plus our own copy passed back through this helper.
  /^the authorization request (is invalid|has expired)\.?$/i,
  /^failed to load the authorization request\.?$/i,
];

// Second gate behind the allow-list. Every pattern above is already bounded, so
// this only matters if one is later written too loosely.
const MAX_LENGTH = 200;

/**
 * Formats a support reference from a timestamp.
 *
 * Deliberately derived from the clock rather than random. A random id would be
 * recorded nowhere: web-console has no client-side error sink (no Sentry, no
 * logging endpoint), so a random token would be unresolvable by whoever the
 * user quoted it to. A UTC second-precision stamp is directly correlatable
 * against the Supabase auth log, which is the log that actually recorded these
 * failures, using the timestamp plus the user's email address.
 */
function supportReference(now: Date): string {
  return `AUTH-${now.toISOString().replace(/[-:]/g, "").replace(/\..*$/, "Z")}`;
}

/**
 * Returns copy that is safe to render to any visitor, including an
 * unauthenticated one.
 *
 * `now` is injectable so the reference is deterministic under test.
 */
export function toUserFacingAuthMessage(
  raw: string | null | undefined,
  now: Date = new Date(),
): string {
  const generic = `${GENERIC_MESSAGE} Reference ${supportReference(now)}.`;

  if (!raw) {
    return generic;
  }

  const trimmed = raw.trim();
  if (trimmed.length === 0 || trimmed.length > MAX_LENGTH) {
    return generic;
  }

  for (const pattern of SAFE_MESSAGE_PATTERNS) {
    if (pattern.test(trimmed)) {
      return trimmed;
    }
  }

  return generic;
}

/**
 * Copy for a sign-up that the deployment refuses as policy rather than fails.
 *
 * True of both refusal shapes below, and it never blames an outage: a visitor
 * reading this knows the account exists to be created by someone else, not
 * that Hive is broken.
 */
export const SIGN_UP_UNAVAILABLE_MESSAGE =
  "Sign-up is not available on this deployment. If you were invited, sign in instead. Otherwise ask a workspace administrator to create your account.";

/**
 * The subset of an auth-js error this mapper reads. Declared structurally so a
 * caller can pass an AuthError straight through without a cast, and so a test
 * can build the shape without constructing an auth-js class.
 */
interface AuthErrorLike {
  message?: string | null;
  status?: number | null;
  name?: string | null;
}

/**
 * True when the auth origin refused account creation as policy.
 *
 * Three shapes, all observed or reachable on this stack:
 *
 *   - A 404 or 403 from the auth origin. deploy/docker/Caddyfile.supabase
 *     answers a bare 404 for /auth/v1/signup on the public listener, and that
 *     is a stated policy, not a fault.
 *   - GoTrue's own copy when GOTRUE_DISABLE_SIGNUP is set. That string is
 *     already allow-listed above and would render verbatim; mapping it here
 *     instead says the same thing in this console's words, with the next step
 *     the GoTrue string omits.
 *   - AuthUnknownError, which auth-js raises when the response body is not
 *     JSON at all. The Caddy refusal has content-length 0, so the JSON parse
 *     throws and the error arrives carrying no status to branch on. This is
 *     the exact shape reported live in issue #1328, and it is why a status
 *     check alone is not enough.
 *
 * The third rule does not swallow an outage, which is the obvious worry.
 * Measured against @supabase/auth-js 2.x, driving supabase.auth.signUp with a
 * stubbed fetch, on 2026-08-29:
 *
 *   500, 502, 503, 504 with an HTML body -> AuthRetryableFetchError, status
 *     preserved. A dead transport (DNS failure, offline) -> the same class,
 *     status 0.
 *   404 or 403 with an empty or non-JSON body -> AuthUnknownError, no status.
 *
 * Every shape auth-js recognises as an outage therefore lands in a different
 * class than the one below, and reaches the generic branch with its support
 * reference. AuthUnknownError means the auth origin answered a non-5xx status
 * with something that is not an API response at all, which on this stack is a
 * gateway refusal.
 */
function isSignUpRefusal(error: AuthErrorLike): boolean {
  if (error.status === 404 || error.status === 403) {
    return true;
  }
  if (error.name === "AuthUnknownError") {
    return true;
  }
  const message = (error.message ?? "").trim();
  return /^(signups? not allowed|email signups are disabled)/i.test(message);
}

/**
 * Same sanitization boundary as toUserFacingAuthMessage, with one extra rule
 * ahead of it: a deliberate refusal is reported as a refusal.
 *
 * Everything the allow-list already handles keeps its existing behaviour, and
 * an unrecognized message still degrades to the generic copy plus a support
 * reference. Only the refusal shapes above are pulled out of that default,
 * because reporting a policy as an outage sends the user to support for
 * something support cannot change.
 */
export function toUserFacingSignUpMessage(
  error: AuthErrorLike | null | undefined,
  now: Date = new Date(),
): string {
  if (!error) {
    return toUserFacingAuthMessage(null, now);
  }
  if (isSignUpRefusal(error)) {
    return SIGN_UP_UNAVAILABLE_MESSAGE;
  }
  return toUserFacingAuthMessage(error.message, now);
}
