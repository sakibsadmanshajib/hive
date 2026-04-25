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

  // Resolve the redirect origin against the canonical app URL when one
  // is configured (matches sign-up + forgot-password). This keeps the
  // host trustworthy (no open-redirect via spoofed X-Forwarded-Host)
  // and avoids the standalone-runtime trap where Next.js leaks
  // `HOSTNAME=0.0.0.0` into `request.url` / `request.nextUrl.origin`.
  // Fall back to the X-Forwarded-Host / Host headers for environments
  // where NEXT_PUBLIC_APP_URL is not set (local dev, ad-hoc previews).
  const appUrl = process.env.NEXT_PUBLIC_APP_URL;
  let origin: string;
  if (appUrl) {
    origin = new URL(appUrl).origin;
  } else {
    const headers = request.headers;
    const host =
      headers.get("x-forwarded-host") ?? headers.get("host") ?? "localhost";
    const isLoopback =
      host.startsWith("localhost") ||
      host.startsWith("127.0.0.1") ||
      host.startsWith("[::1]");
    const proto =
      headers.get("x-forwarded-proto") ?? (isLoopback ? "http" : "https");
    origin = `${proto}://${host}`;
  }
  const redirectUrl = new URL("/auth/sign-in", origin);

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
