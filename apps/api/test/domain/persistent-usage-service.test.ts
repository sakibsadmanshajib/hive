import { describe, expect, it, vi } from "vitest";

import { PersistentUsageService } from "../../src/runtime/services";

describe("PersistentUsageService", () => {
    it("adds a usage event and returns the database persisted createdAt", async () => {
        const mockInsert = vi.fn().mockReturnValue({
            select: vi.fn().mockReturnValue({
                single: vi.fn().mockResolvedValue({
                    data: { created_at: "2026-03-09T12:00:00.000Z" },
                    error: null,
                }),
            }),
        });

        const supabase = {
            from: vi.fn().mockImplementation((table: string) => {
                if (table === "usage_events") {
                    return { insert: mockInsert };
                }
                return {};
            }),
        } as any;

        const service = new PersistentUsageService(supabase);
        const result = await service.add({
            userId: "user-1",
            endpoint: "/v1/chat/completions",
            model: "test-model",
            credits: 10,
        });

        expect(result.id).toMatch(/^usage_/);
        expect(result.userId).toBe("user-1");
        expect(result.createdAt).toBe("2026-03-09T12:00:00.000Z");
        expect(mockInsert).toHaveBeenCalledWith(
            expect.objectContaining({
                user_id: "user-1",
                endpoint: "/v1/chat/completions",
                model: "test-model",
                credits: 10,
            })
        );
    });

    it("lists usage events correctly mocked", async () => {
        const mockEq = vi.fn().mockReturnValue({
            order: vi.fn().mockReturnValue({
                limit: vi.fn().mockResolvedValue({
                    data: [
                        {
                            id: "usage_123",
                            user_id: "user-1",
                            endpoint: "/v1/chat",
                            model: "model-a",
                            credits: 15,
                            created_at: "2026-03-09T10:00:00.000Z",
                        },
                    ],
                    error: null,
                }),
            }),
        });

        const mockSelect = vi.fn().mockReturnValue({ eq: mockEq });

        const supabase = {
            from: vi.fn().mockImplementation((table: string) => {
                if (table === "usage_events") {
                    return { select: mockSelect };
                }
                return {};
            }),
        } as any;

        const service = new PersistentUsageService(supabase);
        const results = await service.list("user-1");

        expect(results).toHaveLength(1);
        expect(results[0]).toEqual({
            id: "usage_123",
            userId: "user-1",
            endpoint: "/v1/chat",
            model: "model-a",
            credits: 15,
            createdAt: "2026-03-09T10:00:00.000Z",
        });
        expect(mockEq).toHaveBeenCalledWith("user_id", "user-1");
    });
});
