// resolveClientOrigin returns the origin a browser-built link should be
// anchored to, with the same loopback demotion lib/http/origin.ts applies on the
// server.
//
// Issue #487 is what this exists for. Three call sites built a GoTrue redirect
// by hand as
//
//   process.env.NEXT_PUBLIC_APP_URL ?? window.location.origin ?? "http://localhost:3000"
//
// and `.env.example` ships `NEXT_PUBLIC_APP_URL=http://localhost:3000`. A
// NEXT_PUBLIC_* value is inlined into the client bundle at BUILD time, not read
// at container start, so the variable a running container reports says nothing
// about what the browser actually executes: those are two different moments. A
// build that leaves the build arg unset, or carries the example default forward,
// bakes a loopback origin into the bundle and mails every user a password-reset
// link pointing at their own machine. Nothing catches it, because nobody clicks
// their own password-reset email.
//
// The demotion is the same one the server helper makes, for the same reason and
// with the same tradeoff:
//
//   1. NEXT_PUBLIC_APP_URL when it names a real host. Operator configured, and
//      unlike a header it cannot be chosen by a request.
//   2. The page's own origin. In a browser this is where the user already is,
//      which is by construction a reachable origin for them.
//   3. A loopback NEXT_PUBLIC_APP_URL, which is correct for local development,
//      where it is also the page's own origin anyway.
//   4. http://localhost:3000.
//
// window.location.origin is not attacker-chosen in the way a Host header is: it
// is the origin that served the executing script. Trusting it is what makes the
// demotion safe on the client, and it is strictly better than emitting a
// localhost link to a real user.

const FALLBACK_ORIGIN = "http://localhost:3000";

// A wildcard bind address is never an origin a client can follow.
const WILDCARD_HOSTNAMES = new Set(["0.0.0.0", "[::]", "::", "0"]);

const LOOPBACK_HOSTNAMES = new Set(["localhost", "127.0.0.1", "[::1]", "::1"]);

function parseConfigured(appUrl: string | undefined): URL | null {
  if (!appUrl) return null;
  let parsed: URL;
  try {
    parsed = new URL(appUrl);
  } catch {
    return null;
  }
  if (parsed.hostname === "" || WILDCARD_HOSTNAMES.has(parsed.hostname)) {
    return null;
  }
  return parsed;
}

function isLoopback(hostname: string): boolean {
  return LOOPBACK_HOSTNAMES.has(hostname);
}

// Read at call time rather than at module scope. The value is a build-time
// constant in a real bundle, and reading it lazily is what lets a test vary it.
export function resolveClientOrigin(): string {
  const configured = parseConfigured(process.env.NEXT_PUBLIC_APP_URL);

  if (configured && !isLoopback(configured.hostname)) {
    return configured.origin;
  }

  if (typeof window !== "undefined") {
    const pageOrigin = window.location?.origin;
    if (pageOrigin && pageOrigin !== "null") {
      const parsedPage = parseConfigured(pageOrigin);
      if (parsedPage) return parsedPage.origin;
    }
  }

  return configured?.origin ?? FALLBACK_ORIGIN;
}
