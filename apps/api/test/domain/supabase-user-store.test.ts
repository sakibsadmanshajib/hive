import { describe, expect, it, vi } from "vitest";
import { SupabaseUserStore } from "../../src/runtime/supabase-user-store";

describe("SupabaseUserStore", () => {
    it("findById returns user profile when found", async () => {
        const supabase = {
            from: vi.fn(() => ({
                select: vi.fn(() => ({
                    eq: vi.fn(() => ({
                        maybeSingle: vi.fn(async () => ({
                            data: {
                                user_id: "user_1",
                                email: "test@example.com",
                                name: "Test User",
                                created_at: "2026-01-01T00:00:00Z",
                            },
                            error: null,
                        })),
                    })),
                })),
            })),
        };

        const store = new SupabaseUserStore(supabase as never);
        const user = await store.findById("user_1");

        expect(user).toBeDefined();
        expect(user?.userId).toBe("user_1");
        expect(user?.email).toBe("test@example.com");
        expect(user?.name).toBe("Test User");
    });

    it("findById returns undefined when user does not exist", async () => {
        const supabase = {
            from: vi.fn(() => ({
                select: vi.fn(() => ({
                    eq: vi.fn(() => ({
                        maybeSingle: vi.fn(async () => ({ data: null, error: null })),
                    })),
                })),
            })),
        };

        const store = new SupabaseUserStore(supabase as never);
        const user = await store.findById("missing_user");

        expect(user).toBeUndefined();
    });

    it("getSettings returns empty record when no settings exist", async () => {
        const supabase = {
            from: vi.fn(() => ({
                select: vi.fn(() => ({
                    eq: vi.fn(() => ({
                        returns: vi.fn(async () => ({ data: [], error: null })),
                    })),
                })),
            })),
        };

        const store = new SupabaseUserStore(supabase as never);
        const settings = await store.getSettings("user_1");

        expect(settings).toEqual({});
    });

    it("getSettings returns mapped settings", async () => {
        const supabase = {
            from: vi.fn(() => ({
                select: vi.fn(() => ({
                    eq: vi.fn(() => ({
                        returns: vi.fn(async () => ({
                            data: [
                                { setting_key: "apiEnabled", enabled: true },
                                { setting_key: "generateImage", enabled: false },
                            ],
                            error: null,
                        })),
                    })),
                })),
            })),
        };

        const store = new SupabaseUserStore(supabase as never);
        const settings = await store.getSettings("user_1");

        expect(settings.apiEnabled).toBe(true);
        expect(settings.generateImage).toBe(false);
    });

    it("upsertSettings calls supabase upsert with correct shape", async () => {
        const upsertMock = vi.fn(async () => ({ error: null }));
        const supabase = {
            from: vi.fn(() => ({
                upsert: upsertMock,
            })),
        };

        const store = new SupabaseUserStore(supabase as never);
        await store.upsertSettings("user_1", { apiEnabled: true, generateImage: false });

        expect(upsertMock).toHaveBeenCalledTimes(1);
        const [updates] = upsertMock.mock.calls[0] as [Array<{ user_id: string; setting_key: string; enabled: boolean }>];
        expect(updates).toEqual(
            expect.arrayContaining([
                expect.objectContaining({ user_id: "user_1", setting_key: "apiEnabled", enabled: true }),
                expect.objectContaining({ user_id: "user_1", setting_key: "generateImage", enabled: false }),
            ]),
        );
    });
});
