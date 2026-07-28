// Shared allow-list for the `?next=` redirect param, honored by both
// /auth/sign-in (post-login) and /auth/callback (post email-confirmation).
// One list, one set of rules -- keeps the open-redirect surface to a short,
// explicit list of known-safe relative paths instead of trusting whatever
// the caller passes, and stops the two call sites drifting out of sync.
const ALLOWED_NEXT_EXACT = new Set<string>([
  "/console/settings/profile",
  "/auth/reset-password",
]);

// Paths allowed with their own query string attached. The match is either the
// bare path or the path followed by "?", so "/oauth/consent-evil" and
// "/invitations/accept-evil" are still rejected -- a prefix is only honoured at
// a real path/query boundary.
//
// /invitations/accept needs its query preserved because the acceptance token
// travels in it: an invitee who is not signed in is bounced to /auth/sign-in
// with next=/invitations/accept?token=..., and dropping the query made
// acceptance impossible for anyone without an existing account (issue #534).
const ALLOWED_NEXT_PREFIXES = ["/oauth/consent", "/invitations/accept"];

const DEFAULT_NEXT_TARGET = "/console";

/**
 * Resolves a `next` query param into a safe relative redirect target. Falls
 * back to /console for anything not on the allow-list, which also rejects
 * protocol-relative ("//evil.com") and absolute URLs since neither can match
 * an allow-listed prefix.
 */
export function resolveNextTarget(next: string | null): string {
  if (!next) return DEFAULT_NEXT_TARGET;

  if (ALLOWED_NEXT_EXACT.has(next)) return next;

  const isAllowedPrefix = ALLOWED_NEXT_PREFIXES.some(
    (prefix) => next === prefix || next.startsWith(`${prefix}?`),
  );

  return isAllowedPrefix ? next : DEFAULT_NEXT_TARGET;
}

/**
 * Appends `next` as a query param onto `path`, URI-encoded. Used to carry
 * the current `?next=` value across the sign-in <-> sign-up cross-links so
 * a user bounced in from an OAuth consent request (or any other allow-listed
 * target) doesn't lose their way back after switching forms. Returns `path`
 * unchanged when there is no next value to carry.
 */
export function appendNextParam(path: string, next: string | null): string {
  if (!next) return path;
  const separator = path.includes("?") ? "&" : "?";
  return `${path}${separator}next=${encodeURIComponent(next)}`;
}
