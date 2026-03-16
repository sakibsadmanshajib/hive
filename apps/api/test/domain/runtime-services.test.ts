import { afterEach, describe, expect, it, vi } from "vitest";

describe("RuntimeServices", () => {
    afterEach(() => {
        vi.restoreAllMocks();
        vi.resetModules();
    });

    it("creates all domain services via DI wiring", async () => {
        vi.resetModules();
        vi.spyOn(console, "warn").mockImplementation(() => undefined);

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
                paymentReconciliation: {
                    enabled: false,
                    intervalMs: 60000,
                    lookbackHours: 24,
                },
                providers: {
                    ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b", timeoutMs: 50, maxRetries: 0 },
                    groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant", timeoutMs: 50, maxRetries: 0 },
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

        vi.doMock("../../src/providers/registry", () => ({
            ProviderRegistry: class {
                captureStartupReadiness = vi.fn(async () => ({
                    ollama: { ready: true, detail: "startup model ready" },
                    groq: { ready: true, detail: "startup model ready" },
                    mock: { ready: true, detail: "startup model ready" },
                }));
                status = vi.fn(async () => ({ providers: [] }));
                chat = vi.fn();
                metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
                metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
            },
        }));

        // Import the module under test dynamically after declaring mocks
        const { createRuntimeServices } = await import("../../src/runtime/services");

        const services = createRuntimeServices();

        expect(services).toBeDefined();
        expect(services.users).toBeDefined();
        expect(services.usage).toBeDefined();
        expect(services.credits).toBeDefined();
        expect(services.payments).toBeDefined();
        expect(services.reconciliation).toBeDefined();
        expect(services.authz).toBeDefined();
        expect(services.ai).toBeDefined();
        expect(services.models).toBeDefined();
        expect(services.adapters.bkash).toBeDefined();
        expect(services.adapters.sslcommerz).toBeDefined();
        expect(typeof services.ai.providersMetrics).toBe("function");
        expect(typeof services.ai.providersMetricsPrometheus).toBe("function");
        expect(services.reconciliationScheduler).toBeUndefined();
    });

    it("does not expose guest-free when no zero-cost provider offers are configured", async () => {
        vi.resetModules();
        vi.spyOn(console, "warn").mockImplementation(() => undefined);

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
                paymentReconciliation: {
                    enabled: false,
                    intervalMs: 60000,
                    lookbackHours: 24,
                },
                providers: {
                    ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b", timeoutMs: 50, maxRetries: 0 },
                    groq: {
                        baseUrl: "https://api.groq.com/openai/v1",
                        model: "llama-3.1-8b-instant",
                        freeModel: undefined,
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
                    openai: {
                        baseUrl: "https://api.openai.com/v1",
                        chatModel: "gpt-4o-mini",
                        imageModel: "gpt-image-1",
                        freeModel: undefined,
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
                    openrouter: {
                        baseUrl: "https://openrouter.ai/api/v1",
                        model: "openrouter/auto",
                        freeModel: undefined,
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
                    gemini: {
                        baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai/",
                        model: "gemini-3-flash-preview",
                        freeModel: undefined,
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
                    anthropic: {
                        baseUrl: "https://api.anthropic.com/v1",
                        model: "claude-sonnet-4-20250514",
                        freeModel: undefined,
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
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

        vi.doMock("../../src/providers/registry", () => ({
            ProviderRegistry: class {
                captureStartupReadiness = vi.fn(async () => ({
                    ollama: { ready: true, detail: "startup model ready" },
                    groq: { ready: true, detail: "startup model ready" },
                    openai: { ready: true, detail: "startup model ready" },
                    openrouter: { ready: true, detail: "startup model ready" },
                    gemini: { ready: true, detail: "startup model ready" },
                    anthropic: { ready: true, detail: "startup model ready" },
                    mock: { ready: true, detail: "startup model ready" },
                }));
                status = vi.fn(async () => ({ providers: [] }));
                chat = vi.fn();
                metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
                metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
            },
        }));

        const { createRuntimeServices } = await import("../../src/runtime/services");

        const services = createRuntimeServices();

        expect(services.models.list().map((model) => model.id)).not.toContain("guest-free");
    });

    it("exposes guest-free when Ollama is configured as an explicit zero-cost offer", async () => {
        vi.resetModules();
        vi.spyOn(console, "warn").mockImplementation(() => undefined);

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
                paymentReconciliation: {
                    enabled: false,
                    intervalMs: 60000,
                    lookbackHours: 24,
                },
                providers: {
                    ollama: {
                        baseUrl: "http://127.0.0.1:11434",
                        model: "llama3.1:8b",
                        freeModel: "qwen2.5:0.5b",
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
                    groq: {
                        baseUrl: "https://api.groq.com/openai/v1",
                        model: "llama-3.1-8b-instant",
                        freeModel: undefined,
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
                    openai: {
                        baseUrl: "https://api.openai.com/v1",
                        chatModel: "gpt-4o-mini",
                        imageModel: "gpt-image-1",
                        freeModel: undefined,
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
                    openrouter: {
                        baseUrl: "https://openrouter.ai/api/v1",
                        model: "openrouter/auto",
                        freeModel: "openrouter/free-model",
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
                    gemini: {
                        baseUrl: "https://generativelanguage.googleapis.com/v1beta/openai/",
                        model: "gemini-3-flash-preview",
                        freeModel: undefined,
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
                    anthropic: {
                        baseUrl: "https://api.anthropic.com/v1",
                        model: "claude-sonnet-4-20250514",
                        freeModel: undefined,
                        timeoutMs: 50,
                        maxRetries: 0,
                    },
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

        vi.doMock("../../src/providers/registry", () => ({
            ProviderRegistry: class {
                captureStartupReadiness = vi.fn(async () => ({
                    ollama: { ready: true, detail: "startup model ready" },
                    groq: { ready: true, detail: "startup model ready" },
                    openai: { ready: true, detail: "startup model ready" },
                    openrouter: { ready: true, detail: "startup model ready" },
                    gemini: { ready: true, detail: "startup model ready" },
                    anthropic: { ready: true, detail: "startup model ready" },
                    mock: { ready: true, detail: "startup model ready" },
                }));
                status = vi.fn(async () => ({ providers: [] }));
                chat = vi.fn();
                metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
                metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
            },
        }));

        const { createRuntimeServices } = await import("../../src/runtime/services");

        const services = createRuntimeServices();

        expect(services.models.list().map((model) => model.id)).toContain("guest-free");
    });

    it("starts the reconciliation scheduler when enabled", async () => {
        vi.resetModules();
        vi.spyOn(console, "warn").mockImplementation(() => undefined);

        const start = vi.fn();

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
                paymentReconciliation: {
                    enabled: true,
                    intervalMs: 60000,
                    lookbackHours: 24,
                },
                providers: {
                    ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b", timeoutMs: 50, maxRetries: 0 },
                    groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant", timeoutMs: 50, maxRetries: 0 },
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

        vi.doMock("../../src/runtime/supabase-client", () => ({
            createSupabaseAdminClient: () => ({ from: () => ({}) }),
        }));

        vi.doMock("../../src/runtime/supabase-billing-store", () => ({
            SupabaseBillingStore: class {
                async listRecentSnapshot() {
                    return { intents: [], events: [] };
                }
            },
        }));

        vi.doMock("../../src/runtime/supabase-api-key-store", () => ({
            SupabaseApiKeyStore: class { },
        }));

        vi.doMock("../../src/runtime/supabase-user-store", () => ({
            SupabaseUserStore: class { },
        }));

        vi.doMock("../../src/runtime/payment-reconciliation-scheduler", () => ({
            PaymentReconciliationScheduler: class {
                start = start;
            },
        }));

        vi.doMock("../../src/providers/registry", () => ({
            ProviderRegistry: class {
                captureStartupReadiness = vi.fn(async () => ({
                    ollama: { ready: true, detail: "startup model ready" },
                    groq: { ready: true, detail: "startup model ready" },
                    mock: { ready: true, detail: "startup model ready" },
                }));
                status = vi.fn(async () => ({ providers: [] }));
                chat = vi.fn();
                metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
                metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
            },
        }));

        const { createRuntimeServices } = await import("../../src/runtime/services");

        const services = createRuntimeServices();

        expect(services.reconciliationScheduler).toBeDefined();
        expect(start).toHaveBeenCalledTimes(1);
    });

    it("triggers provider startup readiness capture during service creation", async () => {
        vi.resetModules();
        vi.spyOn(console, "warn").mockImplementation(() => undefined);

        const captureStartupReadiness = vi.fn(async () => ({
            ollama: { ready: true, detail: "startup model ready" },
            groq: { ready: true, detail: "startup model ready" },
            mock: { ready: true, detail: "startup model ready" },
        }));

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
                paymentReconciliation: {
                    enabled: false,
                    intervalMs: 60000,
                    lookbackHours: 24,
                },
                providers: {
                    ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b", timeoutMs: 50, maxRetries: 0 },
                    groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant", timeoutMs: 50, maxRetries: 0 },
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

        vi.doMock("../../src/providers/registry", () => ({
            ProviderRegistry: class {
                captureStartupReadiness = captureStartupReadiness;
                status = vi.fn(async () => ({ providers: [] }));
                chat = vi.fn();
                metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
                metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
            },
        }));

        const { createRuntimeServices } = await import("../../src/runtime/services");

        createRuntimeServices();
        await Promise.resolve();

        expect(captureStartupReadiness).toHaveBeenCalledTimes(1);
    });

    it("warns for enabled but unready startup providers without failing service creation", async () => {
        vi.resetModules();

        const warn = vi.spyOn(console, "warn").mockImplementation(() => undefined);

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
                paymentReconciliation: {
                    enabled: false,
                    intervalMs: 60000,
                    lookbackHours: 24,
                },
                providers: {
                    ollama: { baseUrl: "http://127.0.0.1:11434", model: "llama3.1:8b", timeoutMs: 50, maxRetries: 0 },
                    groq: { baseUrl: "https://api.groq.com/openai/v1", model: "llama-3.1-8b-instant", timeoutMs: 50, maxRetries: 0 },
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

        vi.doMock("../../src/providers/registry", () => ({
            ProviderRegistry: class {
                captureStartupReadiness = vi.fn(async () => ({
                    ollama: { ready: false, detail: "startup model missing: llama3.1:8b" },
                    groq: { ready: false, detail: "disabled by config" },
                    mock: { ready: true, detail: "startup model ready" },
                }));
                status = vi.fn(async () => ({ providers: [] }));
                chat = vi.fn();
                metrics = vi.fn(async () => ({ scrapedAt: new Date().toISOString(), providers: [] }));
                metricsPrometheus = vi.fn(async () => ({ contentType: "text/plain", body: "" }));
            },
        }));

        const { createRuntimeServices } = await import("../../src/runtime/services");

        const services = createRuntimeServices();
        await Promise.resolve();

        expect(services).toBeDefined();
        expect(warn).toHaveBeenCalledWith(
            expect.objectContaining({
                provider: "ollama",
                detail: "startup model missing: llama3.1:8b",
            }),
            "provider startup readiness warning",
        );
        expect(warn).not.toHaveBeenCalledWith(
            expect.objectContaining({ provider: "groq" }),
            "provider startup readiness warning",
        );
    });
});
