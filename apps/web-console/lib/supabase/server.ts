import { createServerClient } from "@supabase/ssr";
import type { ReadonlyRequestCookies } from "next/dist/server/web/spec-extension/adapters/request-cookies";

export function createClient(cookieStore: ReadonlyRequestCookies) {
  return createServerClient(
    process.env.NEXT_PUBLIC_SUPABASE_URL!,
    process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!,
    {
      cookies: {
        getAll() {
          return cookieStore.getAll();
        },
        setAll(cookiesToSet) {
          try {
            cookiesToSet.forEach(({ name, value, options }) =>
              (cookieStore as unknown as { set: (n: string, v: string, o: unknown) => void }).set(name, value, options)
            );
          } catch {
            // setAll called from a Server Component — cookies can only be set
            // in a Server Action or Route Handler. This error is safe to ignore
            // if middleware is refreshing sessions.
          }
        },
      },
    }
  );
}
