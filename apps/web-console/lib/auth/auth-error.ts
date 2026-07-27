/**
 * Sanitization boundary for authentication error copy.
 *
 * web-console is the last hop before the browser. GoTrue's error strings are
 * produced upstream (and, when an auth hook fails, come straight out of
 * Postgres), so we cannot fix the wording at the source. This is where it gets
 * cleaned instead: every raw auth error that is rendered to a user passes
 * through toUserFacingAuthMessage first. Database and infrastructure internals
 * (function URIs, SQLSTATE codes, schema names, connection strings, panics)
 * must never reach a customer. They are meaningless to the reader and they
 * describe our internals.
 *
 * The filter is a deny-list plus a length cap rather than an allow-list, so
 * genuinely useful GoTrue copy such as "Invalid login credentials" or "Email
 * not confirmed" still reaches the user untouched.
 */

// Deliberately surface-neutral. This helper also guards the sign-up, password
// reset, and OAuth consent forms, so wording specific to signing in would be
// wrong on most of the surfaces that render it.
const GENERIC_MESSAGE =
  "Something went wrong on our end. Please try again in a moment, and contact support if it keeps happening.";

// Markers that mean the message is describing our internals rather than
// something the user can act on. Matched case insensitively. "://" catches any
// connection string or URI (pg-functions://, postgresql://, http://) that a
// lower layer leaked into the message.
const INTERNALS_MARKERS = [
  "pg-functions:",
  "error running hook",
  "hook uri",
  "no_active_membership",
  "postgres",
  "sqlstate",
  "p0001",
  "public.",
  "pgbouncer",
  "supabase_auth_admin",
  "panic",
  "dial tcp",
  "://",
];

// Longest plausible piece of user-facing auth copy. Anything longer is a stack
// trace or a dump, not a message worth showing.
const MAX_LENGTH = 200;

export function toUserFacingAuthMessage(
  raw: string | null | undefined,
): string {
  if (!raw) {
    return GENERIC_MESSAGE;
  }

  const trimmed = raw.trim();
  if (trimmed.length === 0 || trimmed.length > MAX_LENGTH) {
    return GENERIC_MESSAGE;
  }

  const lowered = trimmed.toLowerCase();
  for (const marker of INTERNALS_MARKERS) {
    if (lowered.includes(marker)) {
      return GENERIC_MESSAGE;
    }
  }

  return trimmed;
}
