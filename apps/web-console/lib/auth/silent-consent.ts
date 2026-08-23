/**
 * Server-side silent consent completion (SSO wave 1, spec 2026-08-23).
 *
 * The /oauth/consent landing used to paint a client-side panel that fetched
 * authorization details in the browser and auto-navigated on an
 * already-consented request. Wave 1 moves that decision server-side: a Server
 * Component reads the @supabase/ssr session from cookies, calls
 * GET /auth/v1/oauth/authorizations/{id} with the session access token, and
 * responds with a bare redirect when GoTrue auto-approve applies. The
 * interactive Approve/Deny panel renders only for genuine consent cases.
 *
 * The redirect target is validated here because the landing page is itself a
 * redirect consumer: a self-referential or non-http(s) target would risk a
 * redirect loop. The decision function refuses any target whose path is the
 * consent landing itself and falls to a painted error state instead. That is
 * the one-hop bound: at most one server-side redirect leaves this page per
 * request and it never points back at this page.
 */

export const CONSENT_LANDING_PATH = "/oauth/consent";

/** A GoTrue auto-approve hit: the user already consented to these scopes. */
export interface AutoApprovedAuthorization {
  kind: "auto-approved";
  redirectUrl: string;
}

/** A genuine approve/deny case: render the interactive panel. */
export interface ConsentRequiredAuthorization {
  kind: "needs-consent";
  clientName: string;
  scope: string;
}

/**
 * The two shapes GET /auth/v1/oauth/authorizations/{id} returns:
 * `{redirect_url}` when an active consent row covers the requested scopes,
 * otherwise a client name plus scope list awaiting an approve/deny.
 */
export type GoTrueAuthorization =
  | AutoApprovedAuthorization
  | ConsentRequiredAuthorization;

export type GoTrueLookup =
  | { status: "ok"; authorization: GoTrueAuthorization }
  | { status: "rejected" } // 401/403: GoTrue refused the bearer token.
  | { status: "failed" }; // Any other HTTP status, network error, or malformed body.

export type ConsentLandingDecision =
  | { action: "silent-redirect"; url: string }
  | { action: "sign-in"; url: string }
  | { action: "render-panel" }
  | { action: "error"; message: string };

export interface ConsentLandingInput {
  hasSession: boolean;
  authorizationId: string | null;
  lookup: GoTrueLookup | null;
}

/**
 * Pure decision for the consent landing page. Unit-tested directly; the page
 * component maps each action onto a redirect, the existing client panel, or a
 * painted error state.
 */
export function decideConsentLanding(
  input: ConsentLandingInput,
): ConsentLandingDecision {
  const { hasSession, authorizationId, lookup } = input;

  // No session (or no request id): fall through to the existing client panel,
  // which routes an unauthenticated visitor to sign-in exactly once and shows
  // its painted error state when the request id is missing. Unchanged from
  // pre-wave-1 behavior.
  if (!hasSession || !authorizationId) {
    return { action: "render-panel" };
  }

  if (!lookup) {
    return { action: "render-panel" };
  }

  if (lookup.status === "ok") {
    const authz = lookup.authorization;
    if (authz.kind === "auto-approved") {
      if (!isSafeRedirectTarget(authz.redirectUrl)) {
        return {
          action: "error",
          message:
            "The authorization server returned an unsafe redirect target.",
        };
      }
      return { action: "silent-redirect", url: authz.redirectUrl };
    }
    return { action: "render-panel" };
  }

  if (lookup.status === "rejected") {
    // GoTrue refused the bearer token (expired or revoked session). Route to
    // sign-in once, with a reason marker for the network trace. The sign-in
    // form never auto-bounces back here, so the chain is bounded at one hop.
    return {
      action: "sign-in",
      url: buildSignInRedirect(
        authorizationId,
        "silent-consent-session-expired",
      ),
    };
  }

  // GoTrue unreachable or non-auth HTTP failure: the existing client panel
  // re-checks from the browser and paints its error alert, which is the
  // pre-wave-1 pattern for a down identity service.
  return { action: "render-panel" };
}

/**
 * Builds the /auth/sign-in?next=... URL that returns an unauthenticated (or
 * stale-session) visitor to this exact consent request after one credential
 * entry. Same format the client panel has always used. The optional reason is
 * appended for network-trace observability only; nothing reads it back.
 */
export function buildSignInRedirect(
  authorizationId: string,
  reason?: string,
): string {
  const returnPath = `/oauth/consent?authorization_id=${encodeURIComponent(authorizationId)}`;
  const next = encodeURIComponent(returnPath);
  if (reason === undefined) {
    return `/auth/sign-in?next=${next}`;
  }
  const reasonParam = encodeURIComponent(reason);
  return `/auth/sign-in?next=${next}&reason=${reasonParam}`;
}

/**
 * A redirect target is safe to follow only when it is an absolute http(s) URL
 * that does not point back at the consent landing itself. Anything else risks
 * a self-redirect loop or a non-navigation protocol, so the decision function
 * refuses it and the page paints an error instead.
 */
export function isSafeRedirectTarget(target: string): boolean {
  let parsed: URL;
  try {
    parsed = new URL(target);
  } catch {
    return false;
  }
  if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
    return false;
  }
  // Trailing slash counts too: Next.js 308-normalizes /oauth/consent/ back to
  // /oauth/consent, so a target with the slash would re-enter this page and
  // GoTrue would hand back the same target forever.
  const normalizedPath = parsed.pathname.replace(/\/+$/, "") || "/";
  if (
    normalizedPath === CONSENT_LANDING_PATH ||
    parsed.pathname === CONSENT_LANDING_PATH
  ) {
    return false;
  }
  return true;
}

/**
 * Narrow a GoTrue authorization response body into the discriminated union.
 * Structural guards only, no casts. A body matching neither shape is a
 * malformed response and returns null, which the lookup maps to "failed".
 */
export function parseGoTrueAuthorizationBody(
  body: unknown,
): GoTrueAuthorization | null {
  if (typeof body !== "object" || body === null) {
    return null;
  }
  if ("redirect_url" in body) {
    const redirectUrl = body.redirect_url;
    if (typeof redirectUrl === "string" && redirectUrl.length > 0) {
      return { kind: "auto-approved", redirectUrl };
    }
    return null;
  }
  const client = "client" in body ? body.client : undefined;
  if (typeof client === "object" && client !== null) {
    const scope = "scope" in body ? body.scope : undefined;
    const name = "name" in client ? client.name : undefined;
    if (typeof name === "string" && name.length > 0 && typeof scope === "string") {
      return { kind: "needs-consent", clientName: name, scope };
    }
  }
  return null;
}

export interface GoTrueLookupConfig {
  baseUrl: string;
  anonKey: string;
}

/**
 * The slice of `fetch` the lookup needs, declared structurally so tests can
 * inject a plain object without casts and the global fetch still satisfies it.
 */
export type JsonFetch = (
  url: string,
  init?: {
    method?: string;
    headers?: Record<string, string>;
    cache?: "no-store";
  },
) => Promise<LookupResponse>;

/** The slice of a fetch Response the lookup reads. */
interface LookupResponse {
  ok: boolean;
  status: number;
  json(): Promise<unknown>;
}

/**
 * Server-side lookup of an OAuth authorization request against GoTrue,
 * mirroring supabase-js `auth.oauth.getAuthorizationDetails` but callable
 * from a Server Component. The session access token travels as a bearer; no
 * Origin header is sent, which passes GoTrue's request-origin validation by
 * design ("empty Origin header is ok"). Nothing here grants consent: it only
 * reads the authorization state GoTrue itself maintains.
 */
export async function lookupGoTrueAuthorization(
  authorizationId: string,
  accessToken: string,
  config: GoTrueLookupConfig,
  fetchFn: JsonFetch = fetch,
): Promise<GoTrueLookup> {
  let response: LookupResponse;
  try {
    response = await fetchFn(
      `${config.baseUrl}/oauth/authorizations/${encodeURIComponent(authorizationId)}`,
      {
        method: "GET",
        headers: {
          Authorization: `Bearer ${accessToken}`,
          apikey: config.anonKey,
        },
        cache: "no-store",
      },
    );
  } catch {
    return { status: "failed" };
  }

  if (response.status === 401 || response.status === 403) {
    return { status: "rejected" };
  }
  if (!response.ok) {
    return { status: "failed" };
  }

  try {
    const parsed: unknown = await response.json();
    const authorization = parseGoTrueAuthorizationBody(parsed);
    if (!authorization) {
      return { status: "failed" };
    }
    return { status: "ok", authorization };
  } catch {
    return { status: "failed" };
  }
}
