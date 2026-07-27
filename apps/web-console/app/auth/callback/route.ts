import { NextResponse, type NextRequest } from "next/server";
import { createServerClient, type CookieOptions } from "@supabase/ssr";
import { cookies } from "next/headers";

import { resolveNextTarget } from "@/lib/auth/next-target";
import { resolveCanonicalOrigin } from "@/lib/http/origin";

export async function GET(request: NextRequest) {
  // Query params come from the request line and are safe to read here, but the
  // origin must not: Next.js builds request.url from the server's own
  // `--hostname 0.0.0.0` bind address, so resolving a redirect against it
  // emitted `https://0.0.0.0:3000/console`. See lib/http/origin.ts.
  const { searchParams } = request.nextUrl;
  const origin = resolveCanonicalOrigin(request);
  const code = searchParams.get("code");
  const next = searchParams.get("next");
  const hiveVerify = searchParams.get("hive_verify") === "1";

  // Shared allow-list with /auth/sign-in's post-login redirect (see
  // lib/auth/next-target.ts) so an OAuth-consent signup that requires email
  // confirmation can round-trip back to /oauth/consent same as a plain
  // sign-in does, instead of the two redirect checks drifting out of sync.
  const safeNext = resolveNextTarget(next);

  if (code) {
    const cookieStore = await cookies();
    const supabase = createServerClient(
      process.env.NEXT_PUBLIC_SUPABASE_URL!,
      process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
      {
        cookies: {
          getAll() {
            return cookieStore.getAll();
          },
          setAll(
            cookiesToSet: Array<{
              name: string;
              value: string;
              options: CookieOptions;
            }>
          ) {
            try {
              cookiesToSet.forEach(({ name, value, options }) =>
                cookieStore.set(name, value, options)
              );
            } catch {
              // Ignore: called from Server Component context
            }
          },
        },
      }
    );

    const { data, error } = await supabase.auth.exchangeCodeForSession(code);

    if (!error) {
      if (hiveVerify) {
        // Finalize email verification via the control-plane (#112). The
        // privileged auth.users write used to happen here with
        // SUPABASE_SERVICE_ROLE_KEY — a god-key in a public edge bundle that
        // also failed *silently* when the key was unset. We now forward only
        // the user's session bearer; the control-plane (which holds the
        // service-role DB pool) flips hive_email_verified, and only when
        // Supabase has already confirmed the email. No service-role key or
        // internal token lives on the edge.
        const accessToken = data?.session?.access_token;
        const controlPlaneBaseUrl = process.env.CONTROL_PLANE_BASE_URL;
        if (accessToken && controlPlaneBaseUrl) {
          try {
            const resp = await fetch(
              new URL(
                "/api/v1/accounts/current/email-verification/finalize",
                controlPlaneBaseUrl,
              ),
              {
                method: "POST",
                headers: {
                  Authorization: `Bearer ${accessToken}`,
                  "Content-Type": "application/json",
                },
                cache: "no-store",
                // This step is best-effort and sits on the sign-in redirect
                // path, so it must never make sign-in latency hostage to
                // control-plane responsiveness. Hard-cap it; a timeout throws
                // and is handled by the catch below (logged, redirect proceeds).
                signal: AbortSignal.timeout(3000),
              },
            );
            if (!resp.ok) {
              // Loud but non-fatal to the redirect: surface in server logs so a
              // failure is observable rather than silently dropped.
              console.error(
                `auth/callback: email verification finalize failed (status ${resp.status})`,
              );
            }
          } catch (err) {
            console.error("auth/callback: email verification finalize error", err);
          }
        } else {
          console.error(
            "auth/callback: cannot finalize email verification (missing session token or CONTROL_PLANE_BASE_URL)",
          );
        }
      }

      return NextResponse.redirect(new URL(safeNext, origin));
    }
  }

  // Fall back to /console on any error
  return NextResponse.redirect(new URL("/console", origin));
}
