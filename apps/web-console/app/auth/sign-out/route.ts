import { NextResponse, type NextRequest } from "next/server";
import { cookies } from "next/headers";
import { createClient } from "@/lib/supabase/server";

// Sign-out is a state-changing action — only allow POST so the
// SameSite=Lax auth cookies cannot be used to terminate a session via
// cross-site top-level navigation (CSRF-style logout).
export async function POST(request: NextRequest) {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);

  await supabase.auth.signOut();

  // Build the redirect URL from explicit Host / X-Forwarded-* headers.
  // Inside the docker image Next.js runs in standalone mode with
  // `HOSTNAME=0.0.0.0`, which leaks into both `request.url` and
  // `request.nextUrl.origin`. Resolving the host from headers gives a
  // browser-resolvable origin in dev (localhost) and behind any
  // upstream proxy (forwarded host) in staging/prod.
  const headers = request.headers;
  const host =
    headers.get("x-forwarded-host") ?? headers.get("host") ?? "localhost";
  const proto =
    headers.get("x-forwarded-proto") ??
    (host.startsWith("localhost") || host.startsWith("127.0.0.1")
      ? "http"
      : "https");
  const redirectUrl = new URL("/auth/sign-in", `${proto}://${host}`);

  const response = NextResponse.redirect(redirectUrl, { status: 303 });

  // Clear any account-scoping cookie set by /console/account-switch.
  response.cookies.set("hive_account_id", "", {
    httpOnly: true,
    sameSite: "lax",
    path: "/",
    maxAge: 0,
  });

  return response;
}
