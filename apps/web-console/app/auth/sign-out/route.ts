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

  const response = NextResponse.redirect(new URL("/auth/sign-in", request.url), {
    status: 303,
  });

  // Clear any account-scoping cookie set by /console/account-switch.
  response.cookies.set("hive_account_id", "", {
    httpOnly: true,
    sameSite: "lax",
    path: "/",
    maxAge: 0,
  });

  return response;
}
