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

  // Protect /console: redirect unauthenticated users to sign-in
  if (pathname.startsWith("/console") && !user) {
    const signInUrl = new URL("/auth/sign-in", request.url);
    return NextResponse.redirect(signInUrl);
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
    !user.email_confirmed_at &&
    pathname !== "/console/settings/profile"
  ) {
    const verifyUrl = new URL("/console/settings/profile", request.url);
    return NextResponse.redirect(verifyUrl);
  }

  // Redirect authenticated users from root to /console
  if (pathname === "/" && user) {
    const consoleUrl = new URL("/console", request.url);
    return NextResponse.redirect(consoleUrl);
  }

  return supabaseResponse;
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
