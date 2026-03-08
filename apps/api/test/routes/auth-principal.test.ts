import { describe, expect, it } from "vitest";
import { requireApiUser } from "../../src/routes/auth";

function fakeReply() {
  return {
    statusCode: 200,
    payload: undefined as unknown,
    code(status: number) {
      this.statusCode = status;
      return this;
    },
    send(payload: unknown) {
      this.payload = payload;
      return payload;
    },
  };
}

describe("auth principal resolution", () => {
  it("resolves principal from bearer session token", async () => {
    const reply = fakeReply();
    const userId = await requireApiUser(
      { headers: { authorization: "Bearer sess_123" } } as never,
      reply as never,
      {
        env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: true } } },
        supabaseAuth: { getSessionPrincipal: async () => ({ userId: "user_session" }) },
        users: { resolveApiKey: async () => null },
        authz: { requirePermission: async () => true },
        userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      } as never,
      "usage",
    );

    expect(userId).toBe("user_session");
  });

  it("resolves principal from x-api-key", async () => {
    const reply = fakeReply();
    const userId = await requireApiUser(
      { headers: { "x-api-key": "sk_live_123" } } as never,
      reply as never,
      {
        env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: true } } },
        supabaseAuth: { getSessionPrincipal: async () => null },
        users: { resolveApiKey: async () => ({ userId: "user_api", scopes: ["usage"] }) },
        authz: { requirePermission: async () => true },
        userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      } as never,
      "usage",
    );

    expect(userId).toBe("user_api");
  });

  it("accepts bearer token validated through supabase when flag enabled", async () => {
    const reply = fakeReply();
    const userId = await requireApiUser(
      { headers: { authorization: "Bearer sb_session_token" } } as never,
      reply as never,
      {
        env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: true } } },
        supabaseAuth: { getSessionPrincipal: async () => ({ userId: "user_supabase" }) },
        users: { resolveApiKey: async () => null },
        authz: { requirePermission: async () => true },
        userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      } as never,
      "usage",
    );

    expect(userId).toBe("user_supabase");
  });

  it("returns 401 when credentials are missing", async () => {
    const reply = fakeReply();
    const userId = await requireApiUser(
      { headers: {} } as never,
      reply as never,
      {
        env: { allowDevApiKeyPrefix: false, supabase: { flags: { authEnabled: false } } },
        supabaseAuth: { getSessionPrincipal: async () => null },
        users: { resolveApiKey: async () => null },
        authz: { requirePermission: async () => true },
        userSettings: { getForUser: async () => ({ apiEnabled: true }), canUse: () => true },
      } as never,
      "usage",
    );

    expect(userId).toBeUndefined();
    expect(reply.statusCode).toBe(401);
  });
});
