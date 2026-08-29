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
 * Marks a consent landing URL as having already cost the user one sign-in.
 * The consent landing bounces a rejected bearer to /auth/sign-in exactly once;
 * this marker rides back in the `next` target so the landing can tell a first
 * rejection from a second one and paint an error instead of bouncing again.
 * Nothing authenticates on it: it only bounds the hop count.
 */
export const CONSENT_RETRIED_PARAM = "retried";

/**
 * Builds the /auth/sign-in?next=... URL that returns a visitor to one exact
 * consent request after a single credential entry. Lives here, next to the
 * allow-list that validates the value it produces, so the builder and the
 * validator cannot drift apart, and so the client consent panel can import it
 * without pulling the server-side lookup helpers into the browser bundle.
 * The optional reason is appended for network-trace observability only;
 * nothing reads it back.
 */
export function buildSignInRedirect(
  authorizationId: string,
  reason?: string,
): string {
  const returnPath =
    `/oauth/consent?authorization_id=${encodeURIComponent(authorizationId)}` +
    `&${CONSENT_RETRIED_PARAM}=1`;
  const next = encodeURIComponent(returnPath);
  if (reason === undefined) {
    return `/auth/sign-in?next=${next}`;
  }
  return `/auth/sign-in?next=${next}&reason=${encodeURIComponent(reason)}`;
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
