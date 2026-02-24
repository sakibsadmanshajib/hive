import type { PostgresStore } from "./postgres-store";

export const USER_SETTING_KEYS = ["apiEnabled", "generateImage", "twoFactorEnabled"] as const;

export type UserSettingKey = (typeof USER_SETTING_KEYS)[number];
export type UserSettings = Record<UserSettingKey, boolean>;

const defaultSettings: UserSettings = {
  apiEnabled: true,
  generateImage: true,
  twoFactorEnabled: false,
};

export class UserSettingsService {
  constructor(private readonly store: PostgresStore) {}

  defaults(): UserSettings {
    return { ...defaultSettings };
  }

  canUse(key: UserSettingKey, settings: Partial<UserSettings>): boolean {
    const value = settings[key];
    if (typeof value === "boolean") {
      return value;
    }
    return defaultSettings[key];
  }

  async getForUser(userId: string): Promise<UserSettings> {
    const stored = await this.store.getUserSettings(userId);
    return {
      apiEnabled: typeof stored.apiEnabled === "boolean" ? stored.apiEnabled : defaultSettings.apiEnabled,
      generateImage: typeof stored.generateImage === "boolean" ? stored.generateImage : defaultSettings.generateImage,
      twoFactorEnabled:
        typeof stored.twoFactorEnabled === "boolean" ? stored.twoFactorEnabled : defaultSettings.twoFactorEnabled,
    };
  }

  async updateForUser(userId: string, patch: Partial<UserSettings>): Promise<UserSettings> {
    for (const key of USER_SETTING_KEYS) {
      const value = patch[key];
      if (typeof value === "boolean") {
        await this.store.upsertUserSetting(userId, key, value);
      }
    }
    return this.getForUser(userId);
  }
}
