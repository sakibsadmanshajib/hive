// basePath, kept in sync with next.config.ts's literal "/agent-workspace".
//
// Next.js applies basePath to its own navigation primitives (next/link,
// useRouter, next/navigation's redirect) but NOT to raw absolute URLs handed
// to window.location.* or NextResponse.redirect(). Any redirect built that
// way must prefix this constant, or the Location header lands outside Caddy's
// /agent-workspace/* route (see deploy/docker/Caddyfile.owui) and Open WebUI's
// catch-all serves its own sign-in page instead of this app.
export const BASE_PATH = "/agent-workspace";
