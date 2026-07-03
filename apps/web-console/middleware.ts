import { NextResponse, type NextRequest } from "next/server";
import { createServerClient, type CookieOptions } from "@supabase/ssr";

export async function middleware(request: NextRequest) {
  let supabaseResponse = NextResponse.next({ request });

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
          supabaseResponse = NextResponse.next({ request });
          cookiesToSet.forEach(({ name, value, options }) =>
            supabaseResponse.cookies.set(name, value, options)
          );
        },
      },
    }
  );

  // Refresh session — required for SSR session persistence
  const {
    data: { user },
  } = await supabase.auth.getUser();

  const { pathname } = request.nextUrl;

  // Clickjacking defense, applied to every response this middleware returns.
  // No route in this app (including /oauth/consent's Approve/Deny screen) is
  // meant to be framed. Set here rather than next.config.ts headers()
  // because @opennextjs/cloudflare's routing translation for next.config
  // headers is not something we've verified; middleware always runs as part
  // of the actual Next.js request pipeline on Workers.
  const withSecurityHeaders = (res: NextResponse) => {
    res.headers.set("X-Frame-Options", "DENY");
    res.headers.set("Content-Security-Policy", "frame-ancestors 'none'");
    return res;
  };

  // Redirect while preserving any auth cookies getUser() refreshed onto
  // supabaseResponse. A bare NextResponse.redirect would drop the rotated
  // session cookies, so the next request would carry a stale token.
  const redirectTo = (path: string) => {
    const res = NextResponse.redirect(new URL(path, request.url));
    supabaseResponse.cookies.getAll().forEach((cookie) => {
      res.cookies.set(cookie);
    });
    return withSecurityHeaders(res);
  };

  // Mirror the control-plane's email-verified rule (auth/client.go): the
  // app_metadata.hive_email_verified override wins when present, otherwise
  // fall back to Supabase's email_confirmed_at. Gating on email_confirmed_at
  // alone would wrongly admit a user whose Supabase email is confirmed but
  // whose hive_email_verified override is false.
  const appMetadata = user?.app_metadata as
    | { hive_email_verified?: boolean }
    | undefined;
  const emailVerified =
    typeof appMetadata?.hive_email_verified === "boolean"
      ? appMetadata.hive_email_verified
      : Boolean(user?.email_confirmed_at);

  // Protect /console: redirect unauthenticated users to sign-in
  if (pathname.startsWith("/console") && !user) {
    return redirectTo("/auth/sign-in");
  }

  // Email-verification gate, enforced server-side on every console route.
  // Previously only the console layout component checked verification, so
  // a direct fetch / curl with a valid session cookie could still reach
  // gated pages (e.g. /console/api-keys). Unverified users are redirected
  // to the profile page — the app's verification surface — which is
  // exempted here to avoid a redirect loop.
  if (
    pathname.startsWith("/console") &&
    user &&
    !emailVerified &&
    pathname !== "/console/settings/profile"
  ) {
    return redirectTo("/console/settings/profile");
  }

  // Redirect authenticated users from root to /console
  if (pathname === "/" && user) {
    return redirectTo("/console");
  }

  return withSecurityHeaders(supabaseResponse);
}

export const config = {
  matcher: [
    /*
     * Match all request paths except static assets and Next.js internals:
     * - _next/static (static files)
     * - _next/image (image optimization)
     * - favicon.ico (favicon)
     * - Public files
     */
    "/((?!_next/static|_next/image|favicon.ico|.*\\.(?:svg|png|jpg|jpeg|gif|webp)$).*)",
  ],
};
