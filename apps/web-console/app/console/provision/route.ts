import { cookies } from "next/headers";
import { NextResponse, type NextRequest } from "next/server";

import { reconcileTenantMembership } from "@/lib/control-plane/client";
import { readTenantIdClaim } from "@/lib/auth/tenant-claim";
import { resolveCanonicalOrigin } from "@/lib/http/origin";
import { createClient } from "@/lib/supabase/server";

// Tenant-membership provisioning gate.
//
// A signed-in user can legitimately hold no tenant membership: the Supabase
// access-token hook issues a valid token with no tenant_id claim rather than
// failing sign-in. Somebody has to settle that membership, and it must be code
// that ships with the repository rather than a Supabase Database Webhook an
// operator has to remember to create in a dashboard.
//
// This is a Route Handler rather than logic inside app/console/layout.tsx, for
// two reasons. First, a layout re-renders on every navigation, so provisioning
// would fire on every page view for as long as the user stayed tenant-less, and
// a provisioning write does not belong on a render path. Here it runs once per
// entry into the console, and not at all once the claim is present. Second,
// only a Route Handler (or a Server Action) may write cookies, which is what
// lets the session refresh below actually persist.
//
// Layouts do not wrap Route Handlers, so living under /console costs nothing
// structurally while still inheriting middleware's authentication and
// email-verification gates.
export async function GET(request: NextRequest) {
  const cookieStore = await cookies();
  const supabase = createClient(cookieStore);
  // resolveCanonicalOrigin rather than request.url: it keeps the host
  // trustworthy against a spoofed X-Forwarded-Host and avoids the
  // standalone-runtime trap where Next.js leaks HOSTNAME=0.0.0.0 into
  // request.url. Same reasoning as app/auth/sign-out/route.ts.
  const origin = resolveCanonicalOrigin(request);

  const {
    data: { session },
  } = await supabase.auth.getSession();

  // Already tenanted. Nothing to do, and no reason to spend a control-plane
  // round trip: this is the path a stale link or a double submit takes.
  if (readTenantIdClaim(session?.access_token)) {
    return NextResponse.redirect(new URL("/console", origin), { status: 303 });
  }

  const status = await reconcileTenantMembership();

  if (status !== "provisioned") {
    // Terminal until an administrator acts. /no-workspace deliberately sits
    // outside /console so the console layout does not run there and bounce the
    // user straight back into this handler.
    return NextResponse.redirect(new URL("/no-workspace", origin), {
      status: 303,
    });
  }

  // The membership now exists, but claims are minted when a token is issued, so
  // the session in hand still has no tenant_id. Refresh it here, server side,
  // so the rotated cookies travel on this response and /console sees the claim
  // on the very next request. Doing this on the server rather than from a
  // client effect also means there is no window where the console renders
  // against a token that does not match the database.
  //
  // A failed refresh is not retried in a loop, deliberately. Supabase rotates
  // the token on its own schedule and re-entering /console comes back through
  // here, while the per-user rate limit on the control-plane endpoint is the
  // backstop that bounds the worst case.
  const { data: refreshed } = await supabase.auth.refreshSession();

  // Loop breaker, and the reason this is checked rather than assumed. The
  // console layout sends every claimless request here, so returning to /console
  // while the claim is still missing would come straight back, forever. If the
  // refreshed token does not carry the claim, something disagrees with the
  // database (the hook is not installed on this project, the refresh failed,
  // the membership was revoked in between) and the honest answer is the
  // designed state, which offers an explicit retry.
  if (!readTenantIdClaim(refreshed.session?.access_token)) {
    return NextResponse.redirect(new URL("/no-workspace", origin), {
      status: 303,
    });
  }

  return NextResponse.redirect(new URL("/console", origin), { status: 303 });
}
