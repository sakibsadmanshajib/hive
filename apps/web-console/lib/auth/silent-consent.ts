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

import { buildSignInRedirect } from "@/lib/auth/next-target";

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
  // 401: GoTrue refused the bearer itself, so one credential entry can fix it.
  | { status: "unauthorized" }
  // 403: the bearer was accepted and this authorization is still not the
  // caller's to read, which no amount of signing in changes.
  | { status: "forbidden" }
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
  /**
   * True when this request already came back from the one permitted sign-in
   * hop, read from the marker buildSignInRedirect puts in the return target.
   * A second rejection paints an error rather than asking for a password
   * again, which is what bounds the hop count at one.
   */
  signInAlreadyAttempted: boolean;
}

/**
 * Pure decision for the consent landing page. Unit-tested directly; the page
 * component maps each action onto a redirect, the existing client panel, or a
 * painted error state.
 */
export function decideConsentLanding(
  input: ConsentLandingInput,
): ConsentLandingDecision {
  const { hasSession, authorizationId, lookup, signInAlreadyAttempted } = input;

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

  if (lookup.status === "forbidden") {
    // The bearer was good enough for GoTrue and this authorization is still
    // not readable by this user: an id belonging to somebody else, or one that
    // has expired. Signing in again cannot change that, so never spend a
    // credential prompt on it.
    return {
      action: "error",
      message:
        "This sign-in request is no longer valid. Start again from the application you were signing in to.",
    };
  }

  if (lookup.status === "unauthorized") {
    // GoTrue refused the bearer token (expired or revoked session). Route to
    // sign-in once, with a reason marker for the network trace. The return
    // target carries a marker, so a second rejection lands here with
    // signInAlreadyAttempted set and paints instead of asking again: the hop
    // count is bounded at one even when the rejection is not really about the
    // session at all.
    if (signInAlreadyAttempted) {
      return {
        action: "error",
        message:
          "This sign-in request could not be completed. Start again from the application you were signing in to.",
      };
    }
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
  // The comparison runs against the path Next.js will actually route, not the
  // path as written. Measured against this repository's own production console
  // build: `/oauth/consent/`, `//oauth/consent` and `/oauth//consent` all
  // answer 308 back to `/oauth/consent`, so any of them would re-enter this
  // page and GoTrue would hand back the same target forever. Repeated slashes
  // therefore collapse before the comparison, exactly as trailing ones do.
  //
  // Percent escapes are decoded first. On the build measured here an encoded
  // path (`/oauth/%63onsent`, `/%6Fauth/consent`) answers 404 rather than
  // routing, so decoding is defence in depth rather than a live hole, but the
  // guard should not rest on one proxy's normalisation rules. A malformed
  // escape makes decodeURIComponent throw, in which case the raw path is
  // compared: refusing to decode must never mean accepting the target.
  //
  // Two things need no handling here, both measured rather than assumed.
  // Backslashes are already forward slashes by this point, because the WHATWG
  // URL parser rewrites them for http(s) URLs. Dot segments are gone too:
  // `/x/../oauth/consent` parses to `/oauth/consent` before the guard reads it.
  let decodedPath = parsed.pathname;
  try {
    decodedPath = decodeURIComponent(parsed.pathname);
  } catch {
    decodedPath = parsed.pathname;
  }
  const normalizedPath =
    decodedPath.replace(/\/{2,}/g, "/").replace(/\/+$/, "") || "/";
  if (normalizedPath === CONSENT_LANDING_PATH) {
    return false;
  }
  return true;
}

/**
 * Narrow a GoTrue authorization response body into the discriminated union.
 * Structural guards only, no casts. A body matching neither shape is a
 * malformed response and returns null, which the lookup maps to "failed".
 *
 * Order matters, and it is deliberate: the pending shape is tested FIRST. The
 * two shapes are disjoint in the GoTrue this repository pins, so today either
 * order gives the same answer. They are not disjoint by contract, though, and
 * this is a consent gate, so it fails toward showing the Approve and Deny
 * panel. A future GoTrue that added redirect_url to a still-pending body would
 * otherwise silently skip the panel forever with no test to catch it.
 */
export function parseGoTrueAuthorizationBody(
  body: unknown,
): GoTrueAuthorization | null {
  if (typeof body !== "object" || body === null) {
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
  if ("redirect_url" in body) {
    const redirectUrl = body.redirect_url;
    if (typeof redirectUrl === "string" && redirectUrl.length > 0) {
      return { kind: "auto-approved", redirectUrl };
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
/**
 * One line per failed or refused lookup, on the server only, with a fixed
 * prefix the box's log scrape can grep. The authorization id is a request
 * handle, not a credential: GoTrue will not answer for it without the owner's
 * bearer. The bearer, the anon key and the response body never appear here.
 */
function logLookupOutcome(
  outcome: string,
  authorizationId: string,
  status: number | null,
): void {
  console.error("oauth consent lookup failed", {
    outcome,
    authorizationId,
    status,
  });
}

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
    // Every non-ok outcome below is silent to the user by design: the page
    // falls back to the client panel, which redoes the work. Silent to the
    // OPERATOR as well is how a GoTrue outage on this path becomes invisible,
    // so each one says so once, with the request id and the status and
    // nothing else. Never the bearer, never the response body.
    logLookupOutcome("network error", authorizationId, null);
    return { status: "failed" };
  }

  if (response.status === 401) {
    logLookupOutcome("bearer refused", authorizationId, response.status);
    return { status: "unauthorized" };
  }
  if (response.status === 403) {
    logLookupOutcome("authorization forbidden", authorizationId, response.status);
    return { status: "forbidden" };
  }
  if (!response.ok) {
    logLookupOutcome("upstream error", authorizationId, response.status);
    return { status: "failed" };
  }

  try {
    const parsed: unknown = await response.json();
    const authorization = parseGoTrueAuthorizationBody(parsed);
    if (!authorization) {
      logLookupOutcome("unparseable body", authorizationId, response.status);
      return { status: "failed" };
    }
    return { status: "ok", authorization };
  } catch {
    logLookupOutcome("body read failed", authorizationId, response.status);
    return { status: "failed" };
  }
}
