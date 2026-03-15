import type { SupabaseClient } from "@supabase/supabase-js";
import type { SessionUserIdentity } from "../domain/types";

export class SupabaseAuthService {
  constructor(private readonly supabase: SupabaseClient) {}

  async getSessionPrincipal(token: string): Promise<SessionUserIdentity | null> {
    const { data, error } = await this.supabase.auth.getUser(token);
    if (error || !data.user?.id) {
      return null;
    }

    const email = typeof data.user.email === "string" ? data.user.email.trim().toLowerCase() : "";
    if (!email) {
      return null;
    }

    const metadata = data.user.user_metadata as Record<string, unknown> | undefined;
    const name = typeof metadata?.name === "string" && metadata.name.trim().length > 0
      ? metadata.name.trim()
      : undefined;

    return {
      userId: data.user.id,
      email,
      name,
    };
  }
}
