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
// their own machine and mail localhost verification links. This costs nothing
// in trust: a deployment that configures a real canonical origin still has the
// header ignored entirely, and the demotion only engages in a state where the
// app is already unreachable for external users. Loopback still wins when no
// forwarded host is present, which is exactly local development.
//
// Wildcard bind addresses and unparseable hosts are rejected from every source
// rather than passed through, so neither `0.0.0.0` nor a crafted header value
// can be spliced into a Location.
//
// This file is a deliberate copy of apps/web-console/lib/http/origin.ts. The
// two apps are separate npm packages with separate lockfiles, tsconfig `@/*`
// roots and Docker build contexts, so neither can import the other's lib. The
// tools/lint-no-request-url-origin.mjs guard fails if the two copies drift
// apart, so edit both together.
//
// Used by this app's auth callback so its redirect resolves the same way
// web-console's does.

const FALLBACK_ORIGIN = "http://localhost:3000";

// A server bound to a wildcard address is reachable at some real hostname, but
// the wildcard itself is never a usable origin for a client to follow.
const WILDCARD_HOSTNAMES = new Set(["0.0.0.0", "[::]", "::", "0"]);

const LOOPBACK_HOSTNAMES = new Set(["localhost", "127.0.0.1", "[::1]", "::1"]);

// Parsing through URL both normalizes the port away and validates the value:
// anything with a space, path separator or other illegal host character throws
// instead of being pasted into a Location header.
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
  // X-Forwarded-Host can arrive as a comma-separated list when more than one
  // proxy appends to it; the first entry is the original client-facing host.
  const rawHost = headers.get("x-forwarded-host") ?? headers.get("host");
  if (!rawHost) return null;

  const host = rawHost.split(",")[0].trim();
  const parsed = parseHost(host);
  if (!parsed || !isUsableHostname(parsed.hostname)) return null;

  const forwardedProto = headers.get("x-forwarded-proto")?.split(",")[0].trim();
  const proto =
    forwardedProto ||
    (isLoopbackHostname(parsed.hostname) ? "http" : "https");

  return `${proto}://${parsed.host}`;
}

export function resolveCanonicalOrigin(request: { headers: Headers }): string {
  const configured = configuredOrigin(process.env.NEXT_PUBLIC_APP_URL);

  if (configured && !isLoopbackHostname(configured.hostname)) {
    return configured.origin;
  }

  return forwardedOrigin(request.headers) ?? configured?.origin ?? FALLBACK_ORIGIN;
}

