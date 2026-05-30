// resolveCanonicalOrigin returns a trustworthy origin for server-side redirects.
//
// It prefers the canonical NEXT_PUBLIC_APP_URL so a spoofed X-Forwarded-Host
// cannot drive an open redirect, and avoids the standalone-runtime trap where
// Next.js leaks `HOSTNAME=0.0.0.0` into `request.url` / `request.nextUrl.origin`.
// It falls back to the X-Forwarded-Host / Host headers for environments where
// NEXT_PUBLIC_APP_URL is not set (local dev, ad-hoc previews).
//
// Shared by the auth sign-out route and the members invite proxy so every
// host-header-dependent redirect resolves the same way.
export function resolveCanonicalOrigin(request: { headers: Headers }): string {
  const appUrl = process.env.NEXT_PUBLIC_APP_URL;
  if (appUrl) {
    return new URL(appUrl).origin;
  }

  const headers = request.headers;
  const host =
    headers.get("x-forwarded-host") ?? headers.get("host") ?? "localhost";
  const isLoopback =
    host.startsWith("localhost") ||
    host.startsWith("127.0.0.1") ||
    host.startsWith("[::1]");
  const proto =
    headers.get("x-forwarded-proto") ?? (isLoopback ? "http" : "https");
  return `${proto}://${host}`;
}
