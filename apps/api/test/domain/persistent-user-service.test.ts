import { describe, expect, it, vi } from "vitest";

import { PersistentUserService } from "../../src/runtime/services";

describe("PersistentUserService", () => {
    it("resolves me() with enriched user profile, keys, and lifecycle events", async () => {
        const mockUserStore = {
            findById: vi.fn().mockResolvedValue({
                userId: "user-1",
                email: "test@example.com",
                name: "Test User",
                createdAt: "2026-01-01T00:00:00.000Z",
            }),
        } as any;

        const mockApiKeyStore = {
            list: vi.fn().mockResolvedValue([
                {
                    id: "key-1",
                    keyPrefix: "sk_live_",
                    nickname: "primary",
                    revoked: false,
                    status: "active",
                    scopes: ["chat"],
                    createdAt: "2026-01-01T00:00:00.000Z",
                    expiresAt: "2026-04-01T00:00:00.000Z",
                },
            ]),
            listEvents: vi.fn().mockResolvedValue([
                {
                    id: "event-1",
                    apiKeyId: "key-1",
                    userId: "user-1",
                    eventType: "created",
                    eventAt: "2026-01-01T00:00:00.000Z",
                    metadata: { nickname: "primary" },
                },
            ]),
        } as any;

        const service = new PersistentUserService(mockApiKeyStore, mockUserStore);
        const me = await service.me("user-1");

        expect(me).toBeDefined();
        expect(me?.email).toBe("test@example.com");
        expect(me?.apiKeys).toHaveLength(1);
        expect(me?.apiKeys[0]).toMatchObject({
            id: "key-1",
            key_id: "sk_live_",
            nickname: "primary",
            status: "active",
            expiresAt: "2026-04-01T00:00:00.000Z",
        });
        expect(me?.apiKeyEvents).toEqual([
            expect.objectContaining({
                id: "event-1",
                apiKeyId: "key-1",
                eventType: "created",
            }),
        ]);
    });

    it("validates valid api key and required scope", async () => {
        const mockApiKeyStore = {
            resolve: vi.fn().mockResolvedValue({ userId: "user-2", scopes: ["billing", "chat"] }),
        } as any;
        const mockUserStore = {} as any;

        const service = new PersistentUserService(mockApiKeyStore, mockUserStore);
        const userId = await service.validateApiKey("sk_live_valid", "chat");

        expect(userId).toBe("user-2");
        expect(mockApiKeyStore.resolve).toHaveBeenCalledWith("sk_live_valid");
    });

    it("fails validation if key lacks required scope", async () => {
        const mockApiKeyStore = {
            resolve: vi.fn().mockResolvedValue({ userId: "user-2", scopes: ["chat"] }),
        } as any;
        const mockUserStore = {} as any;

        const service = new PersistentUserService(mockApiKeyStore, mockUserStore);
        const userId = await service.validateApiKey("sk_live_valid", "billing");

        expect(userId).toBeNull();
    });

    it("creates api key with nickname and optional expiration", async () => {
        const mockApiKeyStore = {
            create: vi.fn().mockResolvedValue(undefined),
        } as any;
        const mockUserStore = {} as any;

        const service = new PersistentUserService(mockApiKeyStore, mockUserStore);
        const result = await service.createApiKey("user-x", {
            nickname: "deploy",
            scopes: ["chat", "usage"],
            expiresAt: "2026-05-01T00:00:00.000Z",
        });

        expect(result).toMatchObject({
            key: expect.any(String),
            nickname: "deploy",
            scopes: ["chat", "usage"],
            expiresAt: "2026-05-01T00:00:00.000Z",
        });
        expect(mockApiKeyStore.create).toHaveBeenCalledWith(expect.objectContaining({
            userId: "user-x",
            nickname: "deploy",
            scopes: ["chat", "usage"],
            expiresAt: "2026-05-01T00:00:00.000Z",
        }));
    });

    it("revokes api key by stable id via store", async () => {
        const mockApiKeyStore = {
            revokeById: vi.fn().mockResolvedValue(true),
        } as any;
        const mockUserStore = {} as any;

        const service = new PersistentUserService(mockApiKeyStore, mockUserStore);
        const result = await service.revokeApiKey("user-x", "key-1");

        expect(result).toBe(true);
        expect(mockApiKeyStore.revokeById).toHaveBeenCalledWith("key-1", "user-x");
    });

    it("does not clear a persisted profile name when the session identity omits name", async () => {
        const mockApiKeyStore = {} as any;
        const mockUserStore = {
            findById: vi.fn().mockResolvedValue({
                userId: "user-1",
                email: "demo@example.com",
                name: "Persisted Name",
                createdAt: "2026-01-01T00:00:00.000Z",
            }),
            upsertProfile: vi.fn(),
        } as any;

        const service = new PersistentUserService(mockApiKeyStore, mockUserStore, {} as any);
        await service.ensureSessionUser({
            userId: "user-1",
            email: "demo@example.com",
        });

        expect(mockUserStore.upsertProfile).not.toHaveBeenCalled();
    });

    it("preserves the existing profile name when refreshing session identity fields", async () => {
        const mockApiKeyStore = {} as any;
        const mockUserStore = {
            findById: vi.fn().mockResolvedValue({
                userId: "user-1",
                email: "old@example.com",
                name: "Persisted Name",
                createdAt: "2026-01-01T00:00:00.000Z",
            }),
            upsertProfile: vi.fn().mockResolvedValue(undefined),
        } as any;

        const service = new PersistentUserService(mockApiKeyStore, mockUserStore, {} as any);
        await service.ensureSessionUser({
            userId: "user-1",
            email: "new@example.com",
        });

        expect(mockUserStore.upsertProfile).toHaveBeenCalledWith({
            userId: "user-1",
            email: "new@example.com",
            name: "Persisted Name",
        });
    });
});
