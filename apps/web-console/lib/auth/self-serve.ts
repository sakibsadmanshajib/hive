/**
 * Whether this deployment accepts self-serve account creation.
 *
 * Two locks already refuse POST /auth/v1/signup on every deployment this repo
 * ships: deploy/docker/Caddyfile.supabase answers 404 for that path on the
 * public listener, and GoTrue runs with GOTRUE_DISABLE_SIGNUP set from
 * ENTERPRISE_DISABLE_SIGNUP, which defaults to true. The console had no third
 * source of truth, so it kept shipping a sign-up page that cannot complete,
 * and the gateway's 404 surfaced as "Something went wrong on our end" (issue
 * #1328). This flag is that third source: it does not change the policy, it
 * lets the UI state it.
 *
 * Driven by the same variable that drives the GoTrue flag, in the same
 * polarity, so re-enabling signup stays one value in one place rather than two
 * that can disagree. Wired as a build arg in deploy/docker/docker-compose.yml,
 * because next build inlines NEXT_PUBLIC_* into the client bundle.
 *
 * Fails closed on purpose. Unset and empty both mean disabled: an unset build
 * arg reaches Next.js as an empty string, and reading that as "signup works"
 * is the state this exists to stop. Self-serve is enabled only when a
 * deployment says so explicitly, with false or 0.
 */
export function isSelfServeSignupEnabled(): boolean {
  const raw = (process.env.NEXT_PUBLIC_DISABLE_SELF_SERVE_SIGNUP ?? "")
    .trim()
    .toLowerCase();
  return raw === "false" || raw === "0";
}
