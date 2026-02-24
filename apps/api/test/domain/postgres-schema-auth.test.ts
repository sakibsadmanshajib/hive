import { describe, expect, it, vi } from "vitest";
import { PostgresStore } from "../../src/runtime/postgres-store";

describe("Postgres auth schema bootstrap", () => {
  it("creates auth/rbac/settings/2fa tables during schema setup", async () => {
    const query = vi.fn(async () => ({ rows: [], rowCount: 0 }));
    const store = new PostgresStore("postgres://unused");
    (store as unknown as { pool: { query: typeof query }; schemaReady?: Promise<void> }).pool = { query };

    await (store as unknown as { ensureSchema: () => Promise<void> }).ensureSchema();

    const schemaSql = String((query.mock.calls as Array<Array<unknown>>)[0]?.[0] ?? "");
    expect(schemaSql).toContain("CREATE TABLE IF NOT EXISTS permissions");
    expect(schemaSql).toContain("CREATE TABLE IF NOT EXISTS roles");
    expect(schemaSql).toContain("CREATE TABLE IF NOT EXISTS role_permissions");
    expect(schemaSql).toContain("CREATE TABLE IF NOT EXISTS user_roles");
    expect(schemaSql).toContain("CREATE TABLE IF NOT EXISTS user_settings");
    expect(schemaSql).toContain("CREATE TABLE IF NOT EXISTS auth_sessions");
    expect(schemaSql).toContain("CREATE TABLE IF NOT EXISTS user_2fa");
    expect(schemaSql).toContain("CREATE TABLE IF NOT EXISTS auth_2fa_challenges");
  });

  it("round-trips user settings helper mapping", async () => {
    const query = vi
      .fn()
      .mockResolvedValueOnce({ rows: [], rowCount: 0 })
      .mockResolvedValueOnce({ rows: [], rowCount: 1 })
      .mockResolvedValueOnce({ rows: [{ setting_key: "apiEnabled", enabled: true }], rowCount: 1 });

    const store = new PostgresStore("postgres://unused");
    (store as unknown as { pool: { query: typeof query }; schemaReady?: Promise<void> }).pool = { query };

    await store.upsertUserSetting("user_1", "apiEnabled", true);
    const settings = await store.getUserSettings("user_1");

    expect(settings.apiEnabled).toBe(true);
  });
});
