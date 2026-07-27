import { NextResponse, type NextRequest } from "next/server";
import { cookies } from "next/headers";
import { createClient } from "@/lib/supabase/server";
import { getViewer } from "@/lib/control-plane/client";
import { resolveCanonicalOrigin } from "@/lib/http/origin";

export async function POST(request: NextRequest) {
  // Resolve every target below against the canonical origin rather than
  // request.url: Next.js builds request.url from the server's own `--hostname
  // 0.0.0.0` bind address, so all four of these redirects were emitted as
  // `https://0.0.0.0:3000/console`. See lib/http/origin.ts.
  const consoleUrl = new URL("/console", resolveCanonicalOrigin(request));

  const formData = await request.formData();
  const accountId = formData.get("account_id");

  if (!accountId || typeof accountId !== "string") {
    return NextResponse.redirect(consoleUrl, {
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
    return NextResponse.redirect(consoleUrl, {
      status: 303,
    });
  }

  if (!isValidAccount) {
    return NextResponse.redirect(consoleUrl, {
      status: 303,
    });
  }

  // Persist the selected account in a cookie
  const response = NextResponse.redirect(consoleUrl, {
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
