import type { SupabaseClient } from "@supabase/supabase-js";
import type { UserSettings } from "./user-settings";
import type { SessionUserIdentity } from "../domain/types";

type UserProfileRow = {
  user_id: string;
  email: string;
  name: string | null;
  created_at: string;
};

type UserSettingRow = {
  setting_key: string;
  enabled: boolean;
};

export type SupabaseUserProfile = {
  userId: string;
  email: string;
  name?: string;
  createdAt: string;
};

export class SupabaseUserStore {
  constructor(private readonly supabase: SupabaseClient) {}

  async findById(userId: string): Promise<SupabaseUserProfile | undefined> {
    const { data, error } = await this.supabase
      .from("user_profiles")
      .select("user_id, email, name, created_at")
      .eq("user_id", userId)
      .maybeSingle<UserProfileRow>();

    if (error || !data) {
      return undefined;
    }

    return {
      userId: data.user_id,
      email: data.email,
      name: data.name ?? undefined,
      createdAt: new Date(data.created_at).toISOString(),
    };
  }

  async findByEmail(email: string): Promise<SupabaseUserProfile | undefined> {
    const { data, error } = await this.supabase
      .from("user_profiles")
      .select("user_id, email, name, created_at")
      .eq("email", email.toLowerCase())
      .maybeSingle<UserProfileRow>();

    if (error || !data) {
      return undefined;
    }

    return {
      userId: data.user_id,
      email: data.email,
      name: data.name ?? undefined,
      createdAt: new Date(data.created_at).toISOString(),
    };
  }

  async upsertProfile(profile: SessionUserIdentity): Promise<void> {
    const normalizedEmail = profile.email.trim().toLowerCase();
    const normalizedName = typeof profile.name === "string" && profile.name.trim().length > 0
      ? profile.name.trim()
      : null;

    const { error } = await this.supabase.from("user_profiles").upsert(
      {
        user_id: profile.userId,
        gateway_user_id: profile.userId,
        email: normalizedEmail,
        name: normalizedName,
        updated_at: new Date().toISOString(),
      },
      { onConflict: "user_id" },
    );

    if (error) {
      throw new Error(`failed to upsert user profile: ${error.message}`);
    }
  }

  async upsertSettings(userId: string, patch: Partial<UserSettings>): Promise<void> {
    const updates = Object.entries(patch).flatMap(([settingKey, enabled]) => {
      if (typeof enabled !== "boolean") {
        return [];
      }
      return [{ user_id: userId, setting_key: settingKey, enabled }];
    });

    if (updates.length === 0) {
      return;
    }

    const { error } = await this.supabase.from("user_settings").upsert(updates, { onConflict: "user_id,setting_key" });
    if (error) {
      throw error;
    }
  }

  async getSettings(userId: string): Promise<Record<string, boolean>> {
    const { data, error } = await this.supabase
      .from("user_settings")
      .select("setting_key, enabled")
      .eq("user_id", userId)
      .returns<UserSettingRow[]>();

    if (error || !data) {
      return {};
    }

    return data.reduce<Record<string, boolean>>((acc, row) => {
      acc[row.setting_key] = Boolean(row.enabled);
      return acc;
    }, {});
  }
}
