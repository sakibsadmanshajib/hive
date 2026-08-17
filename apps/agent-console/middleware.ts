import { NextResponse, type NextRequest } from "next/server";
import { createServerClient, type CookieOptions } from "@supabase/ssr";

// Verified live (docker build + curl against the production image) that
// Next.js strips basePath from `request.nextUrl.pathname` before
// middleware runs -- pathname-based matching below needs no prefix -- but
// does NOT re-add it when middleware builds a redirect target via
// `new URL(path, request.url)`, hence the explicit BASE_PATH below.
import { BASE_PATH } from "@/lib/base-path";
import { HIVE_EMBED_HEADER, HIVE_THEME_HEADER, readEmbedTheme } from "@/lib/embed";

// Auth model (blueprint Step 3.1, ratified sidecar decision): this app is a
// standalone console with its own Supabase session, not a cookie handoff
// from Open WebUI. Every request under /tasks must carry a valid session;
// everything else redirects to sign-in.
export async function middleware(request: NextRequest) {
  /*
   * Embedded rendering, decided once per request and carried on the request
   * headers so the root layout can set it on <html> before anything paints.
   * The query string alone cannot do that: a layout receives no search params,
   * and the sign-in redirect below would drop them anyway, which is how a
   * light panel ends up inside a dark shell.
   */
  const requestHeaders = new Headers(request.headers);
  const requestedTheme = readEmbedTheme(request.nextUrl.searchParams.get("theme"));
  if (request.nextUrl.searchParams.get("embed") === "1") {
    requestHeaders.set(HIVE_EMBED_HEADER, "1");
  }
  if (requestedTheme) {
    requestHeaders.set(HIVE_THEME_HEADER, requestedTheme);
  }

  let supabaseResponse = NextResponse.next({ request: { headers: requestHeaders } });

  const supabase = createServerClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL!,
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
    {
      cookies: {
        getAll() {
          return request.cookies.getAll();
        },
        setAll(
          cookiesToSet: Array<{
            name: string;
            value: string;
            options: CookieOptions;
          }>
        ) {
          cookiesToSet.forEach(({ name, value }) =>
            request.cookies.set(name, value)
          );
          supabaseResponse = NextResponse.next({
            request: { headers: requestHeaders },
          });
          cookiesToSet.forEach(({ name, value, options }) =>
            supabaseResponse.cookies.set(name, value, options)
          );
        },
      },
    }
  );

  const {
    data: { user },
  } = await supabase.auth.getUser();

  const { pathname } = request.nextUrl;

  // Framing is now same-origin only, not forbidden.
  //
  // The agent workspace is a destination inside the Hive shell: the chat
  // frontend's /agents route renders it, and the same Caddy listener serves
  // both under one origin (deploy/docker/Caddyfile.owui), so 'self' is a real
  // origin check rather than a formality. Everything the previous 'none'
  // actually defended against, which is a third-party page framing this one to
  // harvest a click or a session, is still refused: 'self' matches the scheme,
  // host and port of this document and nothing else, and X-Frame-Options
  // SAMEORIGIN says the same to a browser too old to read the CSP directive.
  const withSecurityHeaders = (res: NextResponse) => {
    res.headers.set("X-Frame-Options", "SAMEORIGIN");
    res.headers.set("Content-Security-Policy", "frame-ancestors 'self'");
    return res;
  };

  // Embedded rendering is a property of the whole visit, not of one page. The
  // chat shell asks for it on the URL it frames, and a redirect to sign-in
  // would otherwise drop it and paint a light panel inside a dark shell.
  const embedParams = new URLSearchParams();
  for (const name of ["embed", "theme"]) {
    const value = request.nextUrl.searchParams.get(name);
    if (value) {
      embedParams.set(name, value);
    }
  }
  const query = embedParams.toString();

  const redirectTo = (path: string) => {
    const target = new URL(BASE_PATH + path, request.url);
    if (query) {
      target.search = query;
    }
    const res = NextResponse.redirect(target);
    supabaseResponse.cookies.getAll().forEach((cookie) => {
      res.cookies.set(cookie);
    });
    return withSecurityHeaders(res);
  };

  if (pathname.startsWith("/tasks") && !user) {
    return redirectTo("/auth/sign-in");
  }

  if (pathname === "/" ) {
    return redirectTo(user ? "/tasks" : "/auth/sign-in");
  }

  return withSecurityHeaders(supabaseResponse);
}

export const config = {
  matcher: [
    "/((?!_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|jpg|jpeg|gif|webp)$).*)",
  ],
};
