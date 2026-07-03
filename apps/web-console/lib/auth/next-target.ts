// Shared allow-list for the `?next=` redirect param that /auth/sign-in
// honors after a successful login. Keeps the open-redirect surface to a
// short, explicit list of known-safe relative paths instead of trusting
// whatever the caller passes.
const ALLOWED_NEXT_EXACT = new Set<string>(["/invitations/accept"]);

const ALLOWED_NEXT_PREFIXES = ["/oauth/consent"];

const DEFAULT_NEXT_TARGET = "/console";

/**
 * Resolves the `next` query param captured on /auth/sign-in into a safe
 * relative redirect target. Falls back to /console for anything not on the
 * allow-list, which also rejects protocol-relative ("//evil.com") and
 * absolute URLs since neither can match an allow-listed prefix.
 */
export function resolveNextTarget(next: string | null): string {
  if (!next) return DEFAULT_NEXT_TARGET;

  if (ALLOWED_NEXT_EXACT.has(next)) return next;

  const isAllowedPrefix = ALLOWED_NEXT_PREFIXES.some(
    (prefix) => next === prefix || next.startsWith(`${prefix}?`),
  );

  return isAllowedPrefix ? next : DEFAULT_NEXT_TARGET;
}
