import type { SupabaseClient } from "@supabase/supabase-js";

type SessionPrincipal = { userId: string };

export class SupabaseAuthService {
  constructor(private readonly supabase: SupabaseClient) {}

  async getSessionPrincipal(token: string): Promise<SessionPrincipal | null> {
    const { data, error } = await this.supabase.auth.getUser(token);
    if (error || !data.user?.id) {
      return null;
    }
    return { userId: data.user.id };
  }
}
