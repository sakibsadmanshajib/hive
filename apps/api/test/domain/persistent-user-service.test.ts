import { describe, expect, it, vi } from "vitest";

import { PersistentUserService } from "../../src/runtime/services";

describe("PersistentUserService", () => {
    it("resolves me() with user profile and active keys", async () => {
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
                { key: "sk_live_12345678", revoked: false, scopes: ["chat"], createdAt: "2026-01-01T00:00:00.000Z" },
            ]),
        } as any;

        const service = new PersistentUserService(mockApiKeyStore, mockUserStore);
        const me = await service.me("user-1");

        expect(me).toBeDefined();
        expect(me?.email).toBe("test@example.com");
        expect(me?.apiKeys).toHaveLength(1);
        expect(me?.apiKeys[0].key_id).toBe("12345678"); // last 8 chars
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

    it("revokes api key via store", async () => {
        const mockApiKeyStore = {
            revoke: vi.fn().mockResolvedValue(true),
        } as any;
        const mockUserStore = {} as any;

        const service = new PersistentUserService(mockApiKeyStore, mockUserStore);
        const result = await service.revokeApiKey("user-x", "sk_live_valid");

        expect(result).toBe(true);
        expect(mockApiKeyStore.revoke).toHaveBeenCalledWith("sk_live_valid", "user-x");
    });
});
