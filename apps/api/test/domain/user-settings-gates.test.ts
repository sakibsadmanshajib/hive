import { describe, expect, it } from "vitest";
import { UserSettingsService } from "../../src/runtime/user-settings";

describe("UserSettingsService", () => {
  it("denies feature when setting is disabled", () => {
    const settings = new UserSettingsService({
      getSettings: async () => ({}),
      upsertSettings: async () => undefined,
    } as never);

    const canUse = settings.canUse("generateImage", { generateImage: false });

    expect(canUse).toBe(false);
  });

  it("returns defaults for unset keys", async () => {
    const settings = new UserSettingsService({
      getSettings: async () => ({}),
      upsertSettings: async () => undefined,
    } as never);

    const resolved = await settings.getForUser("user_1");

    expect(resolved.apiEnabled).toBe(true);
    expect(resolved.twoFactorEnabled).toBe(false);
  });
});
