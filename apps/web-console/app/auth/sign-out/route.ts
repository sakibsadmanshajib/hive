import { NextResponse, type NextRequest } from "next/server";
import { cookies } from "next/headers";
import { createClient } from "@/lib/supabase/server";

async function signOutAndRedirect(request: NextRequest) {
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

export async function POST(request: NextRequest) {
  return signOutAndRedirect(request);
}

export async function GET(request: NextRequest) {
  return signOutAndRedirect(request);
}
