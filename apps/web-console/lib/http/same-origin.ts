import { NextResponse } from "next/server";

import { resolveCanonicalOrigin, resolveForwardedOrigin } from "./origin";

// Cross-origin refusal for every state-changing request the console serves
// (issue #1457).
//
// Every mutating handler under app/api/console accepts a plain
// <form method="POST"> for no-JavaScript degradation, and none of them carries
// a CSRF token. The issue assumed the Supabase session cookie's SameSite=Lax
// attribute stopped a forged post on its own. On this deployment it does not:
// SameSite is scoped to the registrable domain, and the demo box serves
// console-hive, chat-hive and artifacts-hive under the one scubed.co
// (deploy/cloudflare/README.md), so a post from artifacts to console is
// SAME-site and the cookie is sent. deploy/docker/Caddyfile.artifacts is
// explicit that the artifacts host serves untrusted HTML, isolated only by
// origin. So this is not defence in depth over a working protection: it closes
// a reachable forgery against a signed-in operator.
//
// The check itself is the ordinary one. A state-changing request must carry an
// Origin naming this app, compared against the origin lib/http/origin.ts
// already resolves for redirects.

// Requests that cannot change state. A cross-origin GET is how the whole web
// works; refusing one would break every link into the console.
const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);

// The one path that legitimately receives a cross-origin POST: SSLCommerz
// returns the customer to us with a form post from the payment rail's own
// origin (app/api/payments/return/sslcommerz/route.ts). It settles nothing on
// our side, it reads an intent and redirects, and the control-plane is the
// authority on the payment itself.
const CROSS_ORIGIN_ALLOWED_PATHS = new Set(["/api/payments/return/sslcommerz"]);

// originOf returns the origin of a value, or null when the value cannot be one.
// URL parsing is what separates "present but unusable" from "usable": an empty
// string, the literal "null" and a malformed value all fail here rather than
// falling through to a comparison that might accidentally succeed.
//
// Only http and https are accepted. A browser sends either a serialized origin
// over one of those two schemes or the literal "null", so nothing legitimate is
// lost, and it closes two shapes that would otherwise be surprising: an opaque
// scheme such as `data:` yields the string "null" as its origin, and a `blob:`
// URL yields its INNER url's origin, so `blob:https://canonical/x` would
// otherwise compare equal to the canonical origin.
function originOf(value: string | null): string | null {
  if (value === null) return null;
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return null;
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return null;
  return parsed.origin;
}

// isSameOrigin reports whether a request came from this app.
export function isSameOrigin(request: { headers: Headers }): boolean {
  const header = request.headers.get("origin");

  // An ABSENT Origin header is deliberately treated as same-origin. Older
  // browsers omit it entirely on a same-origin form post, and the routes this
  // guards include plain <form method="POST"> surfaces for users without
  // JavaScript, so refusing here would break that path for real people while
  // blocking nobody: every browser sends Origin on a cross-site form post, and
  // a non-browser client that omits it holds no session cookie either. Do not
  // "fix" this into a refusal.
  //
  // Absent is not the same as present-but-wrong. An empty string, the literal
  // "null" a sandboxed frame sends, and anything unparsable are all present,
  // and all refused below.
  if (header === null) return true;

  const requestOrigin = originOf(header);
  if (requestOrigin === null) return false;

  // Two origins are accepted, and the second one is what keeps a correct
  // deployment from locking itself out.
  //
  //   1. The canonical origin. Operator-configured, unspoofable, and the origin
  //      every redirect already resolves to.
  //   2. The origin the request was actually addressed to, from
  //      X-Forwarded-Host or Host.
  //
  // Accepting (2) costs nothing against the threat this guard exists for. To
  // pass through it an attacker must make Origin and Host agree, and a browser
  // derives Host from the URL that scopes the session cookie: a forged post
  // from another origin carries the victim's host in Host and the attacker's in
  // Origin, so it still fails. Host is a forbidden header name, and setting
  // X-Forwarded-Host from a page makes the request non-simple and triggers a
  // preflight these routes never answer. A non-browser client can set both, and
  // has no cookie.
  //
  // Without (2) the guard would be a second, undeclared deployment constraint.
  // NEXT_PUBLIC_APP_URL is baked into the console image at build time
  // (deploy/docker/Dockerfile.web-console.prod) while the hostname a browser
  // reaches comes from CONSOLE_DOMAIN and CONSOLE_EXTERNAL_SCHEME in Caddy, and
  // nothing ties the three together. `.env.example` itself ships them
  // divergent, so a stock `cp .env.example .env` run would 403 every mutation
  // for a legitimately signed-in operator. That is an availability break for
  // self-hosters, in exchange for no security.
  const accepted = [resolveCanonicalOrigin(request), resolveForwardedOrigin(request)];
  return accepted.some((candidate) => {
    const origin = originOf(candidate);
    return origin !== null && origin === requestOrigin;
  });
}

// refuseCrossOrigin returns the response a mutating route must send when the
// request did not come from the app, or null when it may proceed.
//
// The body is generic on purpose: it tells a customer nothing about internals
// and nothing an attacker can use to tune a next attempt. It is JSON rather
// than the redirect-with-an-error-query that the form routes use for ordinary
// failures, deliberately: an ordinary failure is a person's own submission
// coming back to them, while this one is by definition not a submission the
// person made, so there is no page to send them back to.
export function refuseCrossOrigin(request: { headers: Headers }): Response | null {
  if (isSameOrigin(request)) return null;
  return NextResponse.json({ error: "Forbidden" }, { status: 403 });
}

// refuseCrossOriginMutation is the middleware-level guard: the same check, but
// applied only to methods that can change state, and skipping the one path that
// legitimately receives a cross-origin post.
//
// This is where the guarantee lives, rather than in a call at the top of each
// handler, because a handler added tomorrow is covered without anyone
// remembering. The individual console handlers keep their own call as well: they
// are the routes issue #1457 named, they carry the no-JavaScript forms, and an
// explicit call is what makes the refusal visible and testable at the handler
// itself if the middleware matcher is ever narrowed.
export function refuseCrossOriginMutation(request: {
  method: string;
  headers: Headers;
  nextUrl: { pathname: string };
}): Response | null {
  if (SAFE_METHODS.has(request.method.toUpperCase())) return null;
  if (CROSS_ORIGIN_ALLOWED_PATHS.has(request.nextUrl.pathname)) return null;
  return refuseCrossOrigin(request);
}
