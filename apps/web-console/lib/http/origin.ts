// resolveCanonicalOrigin returns a trustworthy origin for server-side redirects.
//
// Never derive one from `request.url` or `request.nextUrl.origin`. Next.js does
// not build those from the Host header: when `experimental.trustHostHeader` is
// unset it composes the request URL from the server's OWN bind address, in
// next/dist/server/lib/router-utils/resolve-routes.js --
//
//   const initUrl = config.experimental.trustHostHeader
//     ? `https://${req.headers.host || 'localhost'}${req.url}`
//     : opts.port
//       ? `${protocol}://${formatHostname(opts.hostname || 'localhost')}:${opts.port}${req.url}`
//       : req.url || ''
//
// and `formatHostname('0.0.0.0')` returns it verbatim. Both console containers
// start with `--hostname 0.0.0.0 --port 3000` (deploy/docker/Dockerfile.web-console
// and Dockerfile.agent-console), so every redirect resolved against the request
// URL is emitted as `http(s)://0.0.0.0:3000/...` and strands the user.
//
// `experimental.trustHostHeader` is not the fix: it hardcodes `https://` and
// pastes in the raw, attacker-reachable Host header, which is exactly the
// open-redirect shape this helper exists to prevent.
//
// Precedence, most trustworthy first:
//
//   1. NEXT_PUBLIC_APP_URL, when it names a real (non-loopback, non-wildcard)
//      host. It is operator-configured and cannot be spoofed by a request, so
//      it outranks every header.
//   2. X-Forwarded-Host, then Host. Required by deployments that set no
//      NEXT_PUBLIC_APP_URL at all: apps/agent-console bakes its NEXT_PUBLIC_*
//      values as build args (deploy/docker/Dockerfile.agent-console) and
//      deliberately has no NEXT_PUBLIC_APP_URL, so this is the path that
//      carries it.
//   3. http://localhost:3000.
//
// A loopback NEXT_PUBLIC_APP_URL is deliberately demoted below the forwarded
// host. `.env.example` ships `NEXT_PUBLIC_APP_URL=http://localhost:3000`, and a
// deployment that carries that value forward would otherwise 307 real users to
// their own machine and mail localhost verification links.
//
// What the demotion costs, stated precisely rather than waved away: while it is
// engaged, this helper trusts a request header, so a client that can choose its
// own Host or X-Forwarded-Host chooses the redirect origin. A deployment that
// configures a real canonical origin never enters that state, since the header
// is ignored outright. The exposed window is any listener reachable by a client
// that is not a browser while NEXT_PUBLIC_APP_URL is loopback, and that window
// is not empty: deploy/docker/docker-compose.yml publishes the dev console as
// `3000:3000`, on every interface rather than on 127.0.0.1, so it is reachable
// across the LAN with the .env.example value in place. Browsers send the real
// Host, so reaching a victim through this needs a non-browser client or a rogue
// proxy already on that network. Accepted for a dev-only listener, and the
// alternative is worse: without the demotion that same deployment emits
// localhost redirects to every user, which is a guaranteed break rather than a
// conditional one.
//
// Loopback still wins when no forwarded host is present, which is exactly local
// development.
//
// Wildcard bind addresses and unparseable hosts are rejected from every source
// rather than passed through, so neither `0.0.0.0` nor a crafted header value
// can be spliced into a Location.
//
// Shared by the auth callback, sign-out, account-switch and members-invite
// routes so every host-header-dependent redirect resolves the same way.
// apps/agent-console/lib/http/origin.ts is a deliberate copy of this file (the
// two apps are separate npm packages with separate build contexts); the
// tools/lint-no-request-url-origin.mjs guard fails if they drift apart.

const FALLBACK_ORIGIN = "http://localhost:3000";

// A server bound to a wildcard address is reachable at some real hostname, but
// the wildcard itself is never a usable origin for a client to follow.
const WILDCARD_HOSTNAMES = new Set(["0.0.0.0", "[::]", "::", "0"]);

const LOOPBACK_HOSTNAMES = new Set(["localhost", "127.0.0.1", "[::1]", "::1"]);

// Parsing through URL rejects values that cannot be an authority at all: a
// space, a control character or an empty host throws instead of being pasted
// into a Location header.
//
// It is not a syntax filter for the whole string. `good.test/../evil` parses
// happily, with everything from the slash onward landing in `.pathname`. What
// makes that safe is that callers read back only `.host`, so nothing beyond the
// authority survives. Read `.href` or `.toString()` here and that stops being
// true.
function parseHost(host: string): URL | null {
  try {
    return new URL(`http://${host}`);
  } catch {
    return null;
  }
}

function isUsableHostname(hostname: string): boolean {
  return hostname !== "" && !WILDCARD_HOSTNAMES.has(hostname);
}

function isLoopbackHostname(hostname: string): boolean {
  return LOOPBACK_HOSTNAMES.has(hostname);
}

function configuredOrigin(appUrl: string | undefined): URL | null {
  if (!appUrl) return null;
  let parsed: URL;
  try {
    parsed = new URL(appUrl);
  } catch {
    return null;
  }
  return isUsableHostname(parsed.hostname) ? parsed : null;
}

function forwardedOrigin(headers: Headers): string | null {
  // X-Forwarded-Host outranks Host, but an unusable value in it must not
  // shadow a usable Host. This is a preference order, not one shot at whichever
  // header happens to be present: `?? headers.get("host")` would fall through
  // only when X-Forwarded-Host is absent, so a wildcard or malformed value in
  // it would abandon a perfectly good Host and drop all the way to the
  // localhost fallback.
  for (const name of ["x-forwarded-host", "host"]) {
    const rawHost = headers.get(name);
    if (!rawHost) continue;

    // Either header can arrive as a comma-separated list when more than one
    // proxy appends to it; the first entry is the original client-facing host.
    const parsed = parseHost(rawHost.split(",")[0].trim());
    if (!parsed || !isUsableHostname(parsed.hostname)) continue;

    const forwardedProto = headers.get("x-forwarded-proto")?.split(",")[0].trim();
    const proto =
      forwardedProto ||
      (isLoopbackHostname(parsed.hostname) ? "http" : "https");

    return `${proto}://${parsed.host}`;
  }

  return null;
}

export function resolveCanonicalOrigin(request: { headers: Headers }): string {
  const configured = configuredOrigin(process.env.NEXT_PUBLIC_APP_URL);

  if (configured && !isLoopbackHostname(configured.hostname)) {
    return configured.origin;
  }

  return forwardedOrigin(request.headers) ?? configured?.origin ?? FALLBACK_ORIGIN;
}
