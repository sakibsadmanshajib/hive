import { NextResponse, type NextRequest } from "next/server";
import { cookies } from "next/headers";
import { createClient } from "@/lib/supabase/server";
import { getViewer } from "@/lib/control-plane/client";

export async function POST(request: NextRequest) {
  const formData = await request.formData();
  const accountId = formData.get("account_id");

  if (!accountId || typeof accountId !== "string") {
    return NextResponse.redirect(new URL("/console", request.url), {
      status: 303,
    });
  }

  // Validate the requested account exists in the viewer's memberships
  let isValidAccount = false;
  try {
    const viewer = await getViewer();
    isValidAccount = viewer.memberships.some(
      (m) => m.account_id === accountId
    );
  } catch {
    // If we can't fetch the viewer, deny the switch
    return NextResponse.redirect(new URL("/console", request.url), {
      status: 303,
    });
  }

  if (!isValidAccount) {
    return NextResponse.redirect(new URL("/console", request.url), {
      status: 303,
    });
  }

  // Persist the selected account in a cookie
  const response = NextResponse.redirect(new URL("/console", request.url), {
    status: 303,
  });

  response.cookies.set("hive_account_id", accountId, {
    httpOnly: true,
    sameSite: "lax",
    path: "/",
    // No explicit maxAge — session-scoped
  });

  return response;
}
