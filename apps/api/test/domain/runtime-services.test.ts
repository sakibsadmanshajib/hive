import { describe, expect, it, vi } from "vitest";

describe("RuntimeServices", () => {
    it("creates all domain services via DI wiring", async () => {
        vi.resetModules();

        vi.doMock("../../src/config/env", () => ({
            getEnv: () => ({
                nodeEnv: "test",
                port: 8080,
                postgresUrl: "postgres://test",
                redisUrl: "redis://127.0.0.1:6379",
                rateLimitPerMinute: 60,
                adminStatusToken: "admin",
                allowDemoPaymentConfirm: true,
                allowDevApiKeyPrefix: false,
                google: { clientId: "id", clientSecret: "secret", redirectUri: "callback" },
                auth: {
                    sessionTtlMinutes: 60,
                    enforceTwoFactorSensitiveActions: false,
                    twoFactorVerificationWindowMinutes: 10,
                },
                webhookSecrets: { bkash: "bk", sslcommerz: "ssl" },
                bkash: { verifyEndpoint: "", bearerToken: "" },
                sslcommerz: { validatorEndpoint: "", storeId: "", storePassword: "" },
                supabase: {
                    url: "https://demo.supabase.co",
                    serviceRoleKey: "service-role-key",
                    flags: {
                        authEnabled: false,
                        userRepoEnabled: false,
                        apiKeysEnabled: false,
                        billingStoreEnabled: true,
                    },
                },
                providers: {
                    ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b" },
                    groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant" },
                    circuitBreaker: { failureThreshold: 5, resetTimeoutMs: 100 },
                },
                langfuse: {
                    enabled: false,
                    baseUrl: "https://cloud.langfuse.com",
                    publicKey: undefined,
                    secretKey: undefined,
                },
            }),
        }));

        vi.doMock("../../src/runtime/postgres-store", () => ({
            PostgresStore: class { },
        }));

        vi.doMock("../../src/runtime/supabase-client", () => ({
            createSupabaseAdminClient: () => ({ from: () => ({}) }),
        }));

        vi.doMock("../../src/runtime/supabase-billing-store", () => ({
            SupabaseBillingStore: class { },
        }));

        vi.doMock("../../src/runtime/supabase-api-key-store", () => ({
            SupabaseApiKeyStore: class { },
        }));

        vi.doMock("../../src/runtime/supabase-user-store", () => ({
            SupabaseUserStore: class { },
        }));

        // Import the module under test dynamically after declaring mocks
        const { createRuntimeServices } = await import("../../src/runtime/services");

        const services = createRuntimeServices();

        expect(services).toBeDefined();
        expect(services.users).toBeDefined();
        expect(services.usage).toBeDefined();
        expect(services.credits).toBeDefined();
        expect(services.payments).toBeDefined();
        expect(services.authz).toBeDefined();
        expect(services.ai).toBeDefined();
        expect(services.models).toBeDefined();
        expect(services.adapters.bkash).toBeDefined();
        expect(services.adapters.sslcommerz).toBeDefined();
    });
});
