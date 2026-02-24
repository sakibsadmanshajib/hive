import { describe, expect, it } from "vitest";

import { ApiKeyService } from "../../src/domain/api-key-service";
import { hashApiKeyForLookup } from "../../src/runtime/security";
import { SupabaseApiKeyStore } from "../../src/runtime/supabase-api-key-store";

type ApiKeyRow = {
  user_id: string;
  key_hash: string;
  key_prefix: string;
  scopes: string[];
  revoked: boolean;
  created_at: string;
};

class FakeApiKeysTable {
  rows: ApiKeyRow[] = [];
  lastInsert: Record<string, unknown> | null = null;
  private query: Partial<ApiKeyRow> = {};
  private mode: "select" | "update" = "select";
  private updatePatch: Partial<ApiKeyRow> = {};

  insert(payload: Record<string, unknown>) {
    this.lastInsert = payload;
    this.rows.push({
      user_id: String(payload.user_id),
      key_hash: String(payload.key_hash),
      key_prefix: String(payload.key_prefix),
      scopes: Array.isArray(payload.scopes) ? (payload.scopes as string[]) : [],
      revoked: Boolean(payload.revoked),
      created_at: new Date().toISOString(),
    });
    return Promise.resolve({ error: null });
  }

  select() {
    this.mode = "select";
    this.query = {};
    return this;
  }

  update(patch: Partial<ApiKeyRow>) {
    this.mode = "update";
    this.updatePatch = patch;
    this.query = {};
    return this;
  }

  eq(field: keyof ApiKeyRow, value: string | boolean) {
    this.query[field] = value as never;
    return this;
  }

  async maybeSingle() {
    const row = this.rows.find((candidate) => this.matches(candidate));
    return { data: row ?? null, error: null };
  }

  async then(resolve: (value: { data: ApiKeyRow[]; error: null }) => unknown) {
    if (this.mode === "update") {
      this.rows = this.rows.map((candidate) => (this.matches(candidate) ? { ...candidate, ...this.updatePatch } : candidate));
      return resolve({ data: this.rows.filter((candidate) => this.matches(candidate)), error: null });
    }
    return resolve({ data: this.rows.filter((candidate) => this.matches(candidate)), error: null });
  }

  private matches(candidate: ApiKeyRow): boolean {
    return Object.entries(this.query).every(([field, expected]) => candidate[field as keyof ApiKeyRow] === expected);
  }
}

class FakeSupabaseClient {
  readonly apiKeys = new FakeApiKeysTable();

  from(table: string) {
    if (table !== "api_keys") {
      throw new Error(`unexpected table ${table}`);
    }
    return this.apiKeys;
  }
}

describe("ApiKeyService", () => {
  it("validates key when required scope is present", () => {
    const service = new ApiKeyService();
    const key = service.issueKey("user-1", ["read", "write"]);

    const result = service.validateKey(key, "read");

    expect(result).toBe("user-1");
  });

  it("rejects key when scope is missing", () => {
    const service = new ApiKeyService();
    const key = service.issueKey("user-2", ["read"]);

    const result = service.validateKey(key, "write");

    expect(result).toBeNull();
  });

  it("rejects revoked keys", () => {
    const service = new ApiKeyService();
    const key = service.issueKey("user-3", ["read"]);

    service.revokeKey(key);

    const result = service.validateKey(key, "read");
    expect(result).toBeNull();
  });
});

describe("SupabaseApiKeyStore", () => {
  it("stores only hashed api keys in supabase", async () => {
    const supabase = new FakeSupabaseClient();
    const store = new SupabaseApiKeyStore(supabase as never);
    const rawKey = "sk_live_abcdef1234567890";

    await store.create({ key: rawKey, userId: "3a343f16-a94c-4a8f-9f39-4ae8f6bd90e5", scopes: ["chat"] });

    expect(supabase.apiKeys.lastInsert).toMatchObject({
      user_id: "3a343f16-a94c-4a8f-9f39-4ae8f6bd90e5",
      key_hash: hashApiKeyForLookup(rawKey),
      key_prefix: "sk_live_",
      scopes: ["chat"],
      revoked: false,
    });
    expect(supabase.apiKeys.lastInsert).not.toHaveProperty("key");
  });

  it("resolves api key by hash and respects revoked flag", async () => {
    const supabase = new FakeSupabaseClient();
    const store = new SupabaseApiKeyStore(supabase as never);
    const rawKey = "sk_live_hash_lookup";

    await store.create({ key: rawKey, userId: "8c7d3298-b7b0-4ba0-a8f8-f9be8a6430cb", scopes: ["usage", "billing"] });

    await expect(store.resolve(rawKey)).resolves.toEqual({
      userId: "8c7d3298-b7b0-4ba0-a8f8-f9be8a6430cb",
      scopes: ["usage", "billing"],
    });

    await store.revoke(rawKey, "8c7d3298-b7b0-4ba0-a8f8-f9be8a6430cb");

    await expect(store.resolve(rawKey)).resolves.toBeNull();
  });
});
