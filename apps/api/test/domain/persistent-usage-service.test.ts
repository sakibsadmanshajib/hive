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

    it("builds a usage summary alongside the raw event list", async () => {
        vi.useFakeTimers();
        try {
            vi.setSystemTime(new Date("2026-03-13T12:00:00.000Z"));

            const mockRecentEq = vi.fn().mockReturnValue({
                gte: vi.fn().mockReturnValue({
                    order: vi.fn().mockResolvedValue({
                        data: [
                            {
                                id: "usage_3",
                                user_id: "user-1",
                                endpoint: "/v1/chat/completions",
                                model: "smart-reasoning",
                                credits: 30,
                                created_at: "2026-03-13T11:00:00.000Z",
                            },
                            {
                                id: "usage_2",
                                user_id: "user-1",
                                endpoint: "/v1/responses",
                                model: "smart-reasoning",
                                credits: 20,
                                created_at: "2026-03-12T09:00:00.000Z",
                            },
                            {
                                id: "usage_1",
                                user_id: "user-1",
                                endpoint: "/v1/chat/completions",
                                model: "fast-chat",
                                credits: 10,
                                created_at: "2026-03-10T08:00:00.000Z",
                            },
                        ],
                        error: null,
                    }),
                }),
            });

            const mockListEq = vi.fn().mockReturnValue({
                order: vi.fn().mockReturnValue({
                    limit: vi.fn().mockResolvedValue({
                        data: [
                            {
                                id: "usage_3",
                                user_id: "user-1",
                                endpoint: "/v1/chat/completions",
                                model: "smart-reasoning",
                                credits: 30,
                                created_at: "2026-03-13T11:00:00.000Z",
                            },
                            {
                                id: "usage_2",
                                user_id: "user-1",
                                endpoint: "/v1/responses",
                                model: "smart-reasoning",
                                credits: 20,
                                created_at: "2026-03-12T09:00:00.000Z",
                            },
                            {
                                id: "usage_1",
                                user_id: "user-1",
                                endpoint: "/v1/chat/completions",
                                model: "fast-chat",
                                credits: 10,
                                created_at: "2026-03-10T08:00:00.000Z",
                            },
                            {
                                id: "usage_0",
                                user_id: "user-1",
                                endpoint: "/v1/chat/completions",
                                model: "legacy-model",
                                credits: 99,
                                created_at: "2026-03-01T08:00:00.000Z",
                            },
                        ],
                        error: null,
                    }),
                }),
            });
            const mockSelect = vi
                .fn()
                .mockReturnValueOnce({ eq: mockListEq })
                .mockReturnValueOnce({ eq: mockRecentEq });

            const supabase = {
                from: vi.fn().mockImplementation((table: string) => (table === "usage_events" ? { select: mockSelect } : {})),
            } as any;

            const service = new PersistentUsageService(supabase);
            const result = await service.listWithSummary("user-1");

            expect(result.data).toHaveLength(4);
            expect(result.data.some((event) => event.id === "usage_0")).toBe(true);
            expect(result.summary).toEqual({
                windowDays: 7,
                totalRequests: 3,
                totalCredits: 60,
                daily: [
                    { date: "2026-03-07", requests: 0, credits: 0 },
                    { date: "2026-03-08", requests: 0, credits: 0 },
                    { date: "2026-03-09", requests: 0, credits: 0 },
                    { date: "2026-03-10", requests: 1, credits: 10 },
                    { date: "2026-03-11", requests: 0, credits: 0 },
                    { date: "2026-03-12", requests: 1, credits: 20 },
                    { date: "2026-03-13", requests: 1, credits: 30 },
                ],
                byModel: [
                    { key: "smart-reasoning", requests: 2, credits: 50 },
                    { key: "fast-chat", requests: 1, credits: 10 },
                ],
                byEndpoint: [
                    { key: "/v1/chat/completions", requests: 2, credits: 40 },
                    { key: "/v1/responses", requests: 1, credits: 20 },
                ],
            });
            expect(mockListEq).toHaveBeenCalledWith("user_id", "user-1");
            expect(mockRecentEq).toHaveBeenCalledWith("user_id", "user-1");
        } finally {
            vi.useRealTimers();
        }
    });
});
