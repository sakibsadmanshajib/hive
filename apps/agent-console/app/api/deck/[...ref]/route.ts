import { cookies } from "next/headers";

import { createClient } from "@/lib/supabase/server";
import { parseArtifactRef } from "@/lib/edge-api/tasks";

/*
 * Same-origin deck proxy: GET /agent-workspace/api/deck/{id}[/v/{n}].
 *
 * A knowledge-work-pack task publishes its deck as a private artifact, and
 * edge-api serves /artifacts/* to whoever proves a tenant with an
 * Authorization header (optionalViewerTenant in
 * apps/edge-api/internal/artifacts/handler.go). A browser navigation sends no
 * such header, so the owner following a direct link is an anonymous viewer and
 * RLS answers 404 -- on their own deck.
 *
 * The two shortcuts around that were both rejected. Marking the artifact
 * public turns an unguessable URL into the access control, which is exactly
 * the posture Hive Enterprise sells against. A signed or expiring URL puts a
 * credential in a query string, the shape that leaked four real invitation
 * tokens on PR #578. So the link stays a plain anchor and this route supplies
 * the credential the browser cannot: it resolves the caller's existing
 * Supabase session from their cookies, server side, and re-attaches that same
 * access token on a server-to-server call to edge-api. Nothing new is minted
 * and nothing is loosened -- the viewer still has to be signed in as the
 * owning tenant, edge-api still decides.
 *
 * Same-origin is not incidental either: /artifacts/* is not on this app's
 * Caddy route (deploy/docker/Caddyfile.owui) and edge-api emits no CORS
 * headers, so a browser-side fetch could not reach it at all.
 */

/*
 * Mirrors writeArtifactHeaders in apps/edge-api/internal/artifacts/handler.go,
 * with two deliberate differences.
 *
 * frame-ancestors is 'none' unconditionally rather than a configured panel
 * origin: this route is a top-level navigation target opened in a new tab, not
 * the iframe path that ARTIFACTS_FRAME_ANCESTOR exists for.
 *
 * `sandbox allow-scripts` is added, and it is the load-bearing one. Serving
 * tenant-authored HTML from the app's own host would otherwise put that HTML
 * in the app's origin, with reach into its cookies and storage. allow-scripts
 * with no allow-same-origin drops the document into an opaque origin: the
 * deck's own inline navigation JS still runs, but it can no longer touch
 * anything of ours. That restores the isolation the separate artifacts origin
 * was providing before the proxy existed.
 */
const CONTENT_SECURITY_POLICY = [
  "default-src 'none'",
  "script-src 'unsafe-inline'",
  "style-src 'unsafe-inline'",
  "img-src data:",
  "connect-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-ancestors 'none'",
  "sandbox allow-scripts",
].join("; ");

/*
 * Every response from this route sets its own policy, the error ones
 * included. middleware.ts exempts this path from the blanket
 * `frame-ancestors 'self'` it stamps everywhere else, because that `set`
 * replaces rather than merges and would delete the sandbox directive below.
 * Carrying the policy on all four exits means the exemption never leaves a
 * response with no policy at all.
 */
const SHARED_HEADERS: Record<string, string> = {
  "X-Content-Type-Options": "nosniff",
  // Private content on a shared host: never a proxy cache, never disk.
  "Cache-Control": "private, no-store",
  "Content-Security-Policy": CONTENT_SECURITY_POLICY,
};

const DECK_HEADERS: Record<string, string> = {
  ...SHARED_HEADERS,
  "Content-Type": "text/html; charset=utf-8",
};

function errorResponse(status: number, message: string): Response {
  return new Response(message, {
    status,
    headers: { ...SHARED_HEADERS, "Content-Type": "text/plain; charset=utf-8" },
  });
}

export async function GET(
  _request: Request,
  context: { params: Promise<{ ref: string[] }> },
): Promise<Response> {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  const {
    data: { session },
  } = await supabase.auth.getSession();

  // No session, or a session with nothing to present upstream. Deliberately
  // not a fall-through to an unauthenticated upstream call: that would only
  // ever succeed for a public artifact, which is the posture this route
  // exists to avoid needing.
  const accessToken = session?.access_token;
  if (!accessToken) {
    return errorResponse(401, "Unauthorized");
  }

  // Validated against the same shape the link builder produces, before any
  // part of it reaches a URL. The id is then percent-encoded rather than
  // concatenated, so a `?` or `#` smuggled into a path segment cannot open a
  // query string or fragment on the upstream request.
  const { ref } = await context.params;
  const parsed = parseArtifactRef(`/artifacts/${ref.join("/")}`);
  if (!parsed) {
    return errorResponse(400, "Bad Request");
  }

  const baseUrl = process.env.EDGE_API_INTERNAL_BASE_URL;
  if (!baseUrl) {
    return errorResponse(502, "Bad Gateway");
  }

  let upstream: Response;
  try {
    const path =
      parsed.version === null
        ? `/artifacts/${encodeURIComponent(parsed.id)}`
        : `/artifacts/${encodeURIComponent(parsed.id)}/v/${parsed.version}`;
    upstream = await fetch(`${baseUrl}${path}`, {
      headers: { Authorization: `Bearer ${accessToken}` },
      cache: "no-store",
      // A stalled edge-api must not hold the browser navigation open
      // forever; the abort lands in the catch below as a 502.
      signal: AbortSignal.timeout(30_000),
    });
  } catch {
    // Never log the error: the request URL is in it on some runtimes, and the
    // Authorization header is one field away.
    return errorResponse(502, "Bad Gateway");
  }

  if (!upstream.ok) {
    // "You may not see this" and "there is nothing here" collapse onto the
    // same answer, so a probe cannot learn which artifact ids exist in
    // another tenant. Upstream's own body is dropped for the same reason.
    if ([401, 403, 404].includes(upstream.status)) {
      return errorResponse(404, "Not Found");
    }
    return errorResponse(502, "Bad Gateway");
  }

  // Streamed straight through, and never read here: the deck body is tenant
  // content and has no business in this process's memory or logs.
  return new Response(upstream.body, { status: 200, headers: DECK_HEADERS });
}
