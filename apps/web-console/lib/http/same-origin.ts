import { NextResponse } from "next/server";

import { resolveCanonicalOrigin } from "./origin";

// Cross-origin refusal for the mutating console routes (issue #1457).
//
// Every mutating handler under app/api/console accepts a plain
// <form method="POST"> for no-JavaScript degradation, and none of them carries
// a CSRF token. What stops a forged cross-site post today is that the Supabase
// session cookie is SameSite=Lax, so the browser withholds it, the handler's
// getUser() returns nothing and the request is a 401.
//
// That is a property of a cookie attribute set by a third-party library, and
// nothing in this repository asserted it. A Supabase client upgrade that
// changed the default, or a future route reading a differently configured
// cookie, would remove the protection with no test failing and no reviewer
// prompted to look. This helper makes the guarantee ours: it is checked in our
// code, against the same canonical origin lib/http/origin.ts already resolves
// for redirects, and it is covered by tests that name the routes.
//
// It is defence in depth, not the only defence. The session check stays.

// originOf returns the origin of a value, or null when the value cannot be one.
// URL parsing is what separates "present but unusable" from "usable": an empty
// string, the literal "null" and a malformed value all fail here rather than
// falling through to a comparison that might accidentally succeed. An opaque
// scheme (data:, blob:) parses but yields the string "null" as its origin, so
// that case is rejected explicitly instead of relying on the canonical origin
// never being "null".
function originOf(value: string): string | null {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return null;
  }
  return parsed.origin === "null" ? null : parsed.origin;
}

// isSameOrigin reports whether a request came from the canonical app origin.
export function isSameOrigin(request: { headers: Headers }): boolean {
  const header = request.headers.get("origin");

  // An ABSENT Origin header is deliberately treated as same-origin. Older
  // browsers omit it entirely on a same-origin form post, and every route this
  // guards exists to serve a plain <form method="POST"> for users without
  // JavaScript, so refusing here would break that path for real people while
  // blocking nobody: a cross-site form post carries an Origin. Do not "fix"
  // this into a refusal.
  //
  // Absent is not the same as present-but-wrong. An empty string, the literal
  // "null" a sandboxed frame sends, and anything unparsable are all present,
  // and all refused below.
  if (header === null) return true;

  const requestOrigin = originOf(header);
  if (requestOrigin === null) return false;

  // Fail closed if the canonical origin itself cannot be parsed.
  const canonicalOrigin = originOf(resolveCanonicalOrigin(request));
  if (canonicalOrigin === null) return false;

  return requestOrigin === canonicalOrigin;
}

// refuseCrossOrigin returns the response a mutating route must send when the
// request did not come from the app itself, or null when it may proceed. Call
// it as the first statement of the handler, so a forged request costs no
// session lookup and never reaches the control-plane.
//
// The body is generic on purpose: it tells a customer nothing about internals
// and nothing an attacker can use to tune a next attempt.
export function refuseCrossOrigin(request: { headers: Headers }): Response | null {
  if (isSameOrigin(request)) return null;
  return NextResponse.json({ error: "Forbidden" }, { status: 403 });
}
