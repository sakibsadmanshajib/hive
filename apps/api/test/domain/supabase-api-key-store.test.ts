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
        await store.create({
            key: "sk_live_abcdefghijklmnop",
            userId: "user_1",
            scopes: ["chat"],
            nickname: "default key",
        });

        expect(insertMock).toHaveBeenCalledTimes(2);
        const [record] = insertMock.mock.calls[0] as [{
            id: string;
            user_id: string;
            key_hash: string;
            key_prefix: string;
            nickname: string;
            scopes: string[];
            revoked: boolean;
        }];
        const [auditRecord] = insertMock.mock.calls[1] as [{
            api_key_id: string;
            user_id: string;
            event_type: string;
        }];
        expect(record.id).toBeTypeOf("string");
        expect(record.user_id).toBe("user_1");
        expect(record.key_hash).toBeTypeOf("string");
        expect(record.key_prefix).toBe("sk_live_");
        expect(record.nickname).toBe("default key");
        expect(record.scopes).toEqual(["chat"]);
        expect(record.revoked).toBe(false);
        expect(record).not.toHaveProperty("key");
        expect(auditRecord.api_key_id).toBe(record.id);
        expect(auditRecord.user_id).toBe("user_1");
        expect(auditRecord.event_type).toBe("created");
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
            store.create({ key: "sk_live_abcdefghijklmnop", userId: "user_1", scopes: ["chat"], nickname: "dup" }),
        ).rejects.toThrow("duplicate key");
    });

    it("stores nickname and expiration metadata and records a created audit event", async () => {
        const insertMock = vi.fn(async () => ({ error: null }));
        const supabase = {
            from: vi.fn((table: string) => ({
                insert: insertMock,
                table,
            })),
        };

        const store = new SupabaseApiKeyStore(supabase as never);
        await store.create({
            key: "sk_live_with_metadata",
            userId: "user_1",
            scopes: ["chat", "usage"],
            nickname: "deploy key",
            expiresAt: "2026-04-01T00:00:00.000Z",
        });

        expect(insertMock).toHaveBeenCalledTimes(2);
        expect(insertMock.mock.calls[0]?.[0]).toMatchObject({
            nickname: "deploy key",
            expires_at: "2026-04-01T00:00:00.000Z",
        });
        expect(insertMock.mock.calls[1]?.[0]).toMatchObject({
            event_type: "created",
            user_id: "user_1",
        });
    });

    it("returns null for expired keys and records a single expiry audit event across store instances", async () => {
        const keyRow = {
            id: "key_1",
            user_id: "user_1",
            scopes: ["chat"],
            revoked: false,
            expires_at: "2026-01-01T00:00:00.000Z",
        };
        const existingEvents: Array<Record<string, unknown>> = [];
        const auditInsert = vi.fn(async () => ({ error: null }));
        const supabase = {
            from: vi.fn((table: string) => {
                if (table === "api_keys") {
                    return {
                        select: vi.fn(() => ({
                            eq: vi.fn(() => ({
                                maybeSingle: vi.fn(async () => ({ data: keyRow, error: null })),
                            })),
                        })),
                    };
                }
                if (table === "api_key_events") {
                    return {
                        select: vi.fn(() => ({
                            eq: vi.fn(() => ({
                                eq: vi.fn(() => ({
                                    maybeSingle: vi.fn(async () => ({ data: existingEvents[0] ?? null, error: null })),
                                })),
                            })),
                        })),
                        insert: vi.fn(async (payload) => {
                            existingEvents.push(payload as Record<string, unknown>);
                            return auditInsert(payload);
                        }),
                    };
                }
                throw new Error(`unexpected table ${table}`);
            }),
        };

        const store = new SupabaseApiKeyStore(supabase as never);
        const storeAfterRestart = new SupabaseApiKeyStore(supabase as never);

        await expect(store.resolve("sk_live_expired")).resolves.toBeNull();
        await expect(storeAfterRestart.resolve("sk_live_expired")).resolves.toBeNull();
        expect(auditInsert).toHaveBeenCalledTimes(1);
        expect(auditInsert.mock.calls[0]?.[0]).toMatchObject({
            api_key_id: "key_1",
            event_type: "expired_observed",
            user_id: "user_1",
        });
    });

    it("rolls back created key metadata when created audit event write fails", async () => {
        const keyInsert = vi.fn(async () => ({ error: null }));
        const rollbackDelete = vi.fn(() => ({
            eq: vi.fn(async () => ({ error: null })),
        }));
        const eventInsert = vi.fn(async () => ({ error: { message: "event insert failed" } }));
        const supabase = {
            from: vi.fn((table: string) => {
                if (table === "api_keys") {
                    return {
                        insert: keyInsert,
                        delete: rollbackDelete,
                    };
                }
                if (table === "api_key_events") {
                    return {
                        insert: eventInsert,
                    };
                }
                throw new Error(`unexpected table ${table}`);
            }),
        };

        const store = new SupabaseApiKeyStore(supabase as never);

        await expect(store.create({
            key: "sk_live_rollback",
            userId: "user_1",
            scopes: ["chat"],
            nickname: "rollback",
        })).rejects.toThrow("event insert failed");

        expect(keyInsert).toHaveBeenCalledTimes(1);
        expect(rollbackDelete).toHaveBeenCalledTimes(1);
    });

    it("restores revoke state when revoked audit event write fails", async () => {
        const rollbackUpdateSelect = vi.fn(async () => ({ data: [{ id: "key_1" }], error: null }));
        const rollbackUpdate = vi.fn(() => ({
            eq: vi.fn(() => ({
                eq: vi.fn(() => ({
                    select: rollbackUpdateSelect,
                })),
            })),
        }));
        const revokeUpdateSelect = vi.fn(async () => ({
            data: [{ id: "key_1", revoked: true, revoked_at: "2026-03-12T00:00:00.000Z" }],
            error: null,
        }));
        const revokeUpdate = vi.fn(() => ({
            eq: vi.fn(() => ({
                eq: vi.fn(() => ({
                    eq: vi.fn(() => ({
                        select: revokeUpdateSelect,
                    })),
                })),
            })),
        }));
        const eventInsert = vi.fn(async () => ({ error: { message: "event insert failed" } }));
        let apiKeysUpdateCalls = 0;
        const supabase = {
            from: vi.fn((table: string) => {
                if (table === "api_keys") {
                    apiKeysUpdateCalls += 1;
                    return {
                        update: apiKeysUpdateCalls === 1 ? revokeUpdate : rollbackUpdate,
                    };
                }
                if (table === "api_key_events") {
                    return {
                        insert: eventInsert,
                    };
                }
                throw new Error(`unexpected table ${table}`);
            }),
        };

        const store = new SupabaseApiKeyStore(supabase as never);

        await expect(store.revokeById("key_1", "user_1")).rejects.toThrow("event insert failed");

        expect(revokeUpdate).toHaveBeenCalledTimes(1);
        expect(rollbackUpdate).toHaveBeenCalledTimes(1);
    });
});
