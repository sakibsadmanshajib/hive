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

  // Build the redirect URL from `request.nextUrl` (which honors the
  // X-Forwarded-Host / X-Forwarded-Proto headers Next.js sets on the
  // edge) instead of `request.url`. The latter falls back to the
  // socket bind address — `0.0.0.0:3000` inside a container — which
  // browsers cannot resolve.
  const redirectUrl = request.nextUrl.clone();
  redirectUrl.pathname = "/auth/sign-in";
  redirectUrl.search = "";

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
