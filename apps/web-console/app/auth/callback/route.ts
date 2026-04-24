import { NextResponse, type NextRequest } from "next/server";
import { createServerClient, type CookieOptions } from "@supabase/ssr";
import { cookies } from "next/headers";

const ALLOWED_NEXT_TARGETS = new Set([
  "/console",
  "/console/settings/profile",
  "/auth/reset-password",
]);

export async function GET(request: NextRequest) {
  const { searchParams, origin } = new URL(request.url);
  const code = searchParams.get("code");
  const next = searchParams.get("next") ?? "";
  const hiveVerify = searchParams.get("hive_verify") === "1";

  // Only allow explicitly safe redirect targets
  const safeNext = ALLOWED_NEXT_TARGETS.has(next) ? next : "/console";

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
        const userId = data?.user?.id ?? data?.session?.user?.id;
        const appMetadata =
          data?.user?.app_metadata ??
          data?.session?.user?.app_metadata ??
          {};

        if (userId && process.env.SUPABASE_SERVICE_ROLE_KEY) {
          await fetch(
            new URL(`/auth/v1/admin/users/${userId}`, process.env.NEXT_PUBLIC_SUPABASE_URL!),
            {
              method: "PUT",
              headers: {
                Authorization: `Bearer ${process.env.SUPABASE_SERVICE_ROLE_KEY}`,
                apikey: process.env.SUPABASE_SERVICE_ROLE_KEY,
                "Content-Type": "application/json",
              },
              body: JSON.stringify({
                app_metadata: {
                  ...appMetadata,
                  hive_email_verified: true,
                },
              }),
            }
          ).catch(() => undefined);
        }
      }

      return NextResponse.redirect(new URL(safeNext, origin));
    }
  }

  // Fall back to /console on any error
  return NextResponse.redirect(new URL("/console", origin));
}
