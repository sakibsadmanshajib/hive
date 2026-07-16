import { NextResponse, type NextRequest } from "next/server";
import { createServerClient, type CookieOptions } from "@supabase/ssr";

// Auth model (blueprint Step 3.1, ratified sidecar decision): this app is a
// standalone console with its own Supabase session, not a cookie handoff
// from Open WebUI. Every request under /tasks must carry a valid session;
// everything else redirects to sign-in.
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

  const {
    data: { user },
  } = await supabase.auth.getUser();

  const { pathname } = request.nextUrl;

  // Console is opened via window.open from an OWUI Action (not framed), but
  // set the same defence-in-depth headers as web-console anyway -- cheap and
  // there is no legitimate reason for this app to ever be iframed.
  const withSecurityHeaders = (res: NextResponse) => {
    res.headers.set("X-Frame-Options", "DENY");
    res.headers.set("Content-Security-Policy", "frame-ancestors 'none'");
    return res;
  };

  const redirectTo = (path: string) => {
    const res = NextResponse.redirect(new URL(path, request.url));
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
