import { createClient } from "@supabase/supabase-js";
import type { AppEnv } from "../config/env";

export function createSupabaseAdminClient(env: AppEnv) {
  return createClient(env.supabase.url, env.supabase.serviceRoleKey, {
    auth: {
      autoRefreshToken: false,
      persistSession: false,
    },
  });
}
