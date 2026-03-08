import { describe, expect, it, vi } from "vitest";
import { SupabaseApiKeyStore } from "../../src/runtime/supabase-api-key-store";

function fakeSupabase(overrides: Record<string, unknown> = {}) {
    return {
        from: vi.fn(() => ({
            insert: vi.fn(async () => overrides.insertResult ?? { error: null }),
            select: vi.fn(() => ({
                eq: vi.fn(() => ({
                    eq: vi.fn(async () => overrides.selectResult ?? { data: [], error: null }),
                    maybeSingle: vi.fn(async () => overrides.singleResult ?? { data: null, error: null }),
                })),
            })),
            update: vi.fn(() => ({
                eq: vi.fn(() => ({
                    eq: vi.fn(async () => overrides.updateResult ?? { error: null }),
                })),
            })),
        })),
    };
}

describe("SupabaseApiKeyStore", () => {
    it("create inserts a new api key record", async () => {
        const insertMock = vi.fn(async () => ({ error: null }));
        const supabase = {
            from: vi.fn(() => ({
                insert: insertMock,
            })),
        };

        const store = new SupabaseApiKeyStore(supabase as never);
        await store.create({ key: "sk_live_abcdefghijklmnop", userId: "user_1", scopes: ["chat"] });

        expect(insertMock).toHaveBeenCalledTimes(1);
        const [record] = insertMock.mock.calls[0] as [{ user_id: string; scopes: string[]; revoked: boolean }];
        expect(record.user_id).toBe("user_1");
        expect(record.scopes).toEqual(["chat"]);
        expect(record.revoked).toBe(false);
    });

    it("list returns api keys for a user", async () => {
        const supabase = {
            from: vi.fn(() => ({
                select: vi.fn(() => ({
                    eq: vi.fn(async () => ({
                        data: [
                            { key_prefix: "sk_live_a", user_id: "user_1", scopes: ["chat"], revoked: false, created_at: "2026-01-01T00:00:00Z" },
                        ],
                        error: null,
                    })),
                })),
            })),
        };

        const store = new SupabaseApiKeyStore(supabase as never);
        const keys = await store.list("user_1");

        expect(keys).toHaveLength(1);
        expect(keys[0].scopes).toEqual(["chat"]);
        expect(keys[0].revoked).toBe(false);
    });

    it("throws on insert error", async () => {
        const supabase = {
            from: vi.fn(() => ({
                insert: vi.fn(async () => ({ error: { message: "duplicate key" } })),
            })),
        };

        const store = new SupabaseApiKeyStore(supabase as never);

        await expect(
            store.create({ key: "sk_live_abcdefghijklmnop", userId: "user_1", scopes: ["chat"] }),
        ).rejects.toThrow("duplicate key");
    });
});
