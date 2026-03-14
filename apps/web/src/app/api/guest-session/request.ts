function isLoopbackHostname(hostname: string): boolean {
  return hostname === "127.0.0.1"
    || hostname === "localhost"
    || hostname === "::1"
    || hostname === "[::1]"
    || hostname === "0:0:0:0:0:0:0:1"
    || hostname === "[0:0:0:0:0:0:0:1]";
}

function normalizeComparableOrigin(value: string): string | null {
  try {
    const url = new URL(value);
    const hostname = isLoopbackHostname(url.hostname) ? "loopback" : url.hostname;
    return `${url.protocol}//${hostname}${url.port ? `:${url.port}` : ""}`;
  } catch {
    return null;
  }
}

export function isSameOriginBrowserRequest(request: Request): boolean {
  const targetOrigin = normalizeComparableOrigin(request.url);
  if (!targetOrigin) {
    return false;
  }
  const origin = request.headers.get("origin");
  if (origin && normalizeComparableOrigin(origin) === targetOrigin) {
    return true;
  }

  const referer = request.headers.get("referer");
  if (referer) {
    return normalizeComparableOrigin(referer) === targetOrigin;
  }

  return false;
}

export function readClientIp(request: Request): string | null {
  const forwardedFor = request.headers.get("x-forwarded-for");
  if (forwardedFor) {
    const firstIp = forwardedFor.split(",")[0]?.trim();
    if (firstIp) {
      return firstIp;
    }
  }

  const realIp = request.headers.get("x-real-ip");
  if (realIp?.trim()) {
    return realIp.trim();
  }

  return null;
}
