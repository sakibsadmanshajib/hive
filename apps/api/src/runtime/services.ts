import { randomUUID } from "node:crypto";
import type { CreditBalance, UsageEvent } from "../domain/types";
import { ModelService } from "../domain/model-service";
import { getEnv, type AppEnv } from "../config/env";
import { BkashAdapter, SslcommerzAdapter } from "./provider-adapters";
import { GroqProviderClient } from "../providers/groq-client";
import { MockProviderClient } from "../providers/mock-client";
import { OllamaProviderClient } from "../providers/ollama-client";
import { ProviderRegistry } from "../providers/registry";
import type { ProviderName } from "../providers/types";
import type { PersistentApiKey, PersistentPaymentIntent } from "../domain/types";
import { RedisRateLimiter } from "./redis-rate-limiter";
import { createApiKey } from "./security";
import { LangfuseClient } from "./langfuse";
import { AuthorizationService } from "./authorization";
import { UserSettingsService } from "./user-settings";
import { createSupabaseAdminClient } from "./supabase-client";
import { SupabaseAuthService } from "./supabase-auth-service";
import { SupabaseApiKeyStore } from "./supabase-api-key-store";
import { SupabaseUserStore } from "./supabase-user-store";
import { SupabaseBillingStore } from "./supabase-billing-store";

type ChatMessage = { role: string; content: string };

type ApiKeyStore = {
  create(input: { key: string; userId: string; scopes: string[] }): Promise<void>;
  resolve(key: string): Promise<{ userId: string; scopes: string[] } | null>;
  list(userId: string): Promise<PersistentApiKey[]>;
  revoke(key: string, userId: string): Promise<boolean>;
  get(key: string): Promise<PersistentApiKey | undefined>;
};

type BillingStore = {
  getBalance(userId: string): Promise<CreditBalance>;
  consumeCredits(userId: string, credits: number, referenceId: string): Promise<boolean>;
  topUpCredits(userId: string, credits: number, referenceId: string): Promise<CreditBalance>;
  createPaymentIntent(intent: PersistentPaymentIntent): Promise<void>;
  getPaymentIntent(intentId: string): Promise<PersistentPaymentIntent | undefined>;
  recordPaymentEvent(
    eventKey: string,
    intentId: string,
    provider: string,
    providerTxnId: string,
    verified: boolean,
  ): Promise<boolean>;
  markPaymentCredited(intentId: string, mintedCredits: number, status: "credited" | "failed"): Promise<void>;
  claimPaymentIntent(
    intentId: string,
    provider: string,
    providerTxnId: string,
  ): Promise<{ success: boolean; intent?: PersistentPaymentIntent; error?: string }>;
};

class PersistentCreditService {
  constructor(private readonly store: BillingStore) { }

  getBalance(userId: string): Promise<CreditBalance> {
    return this.store.getBalance(userId);
  }

  consume(userId: string, credits: number): Promise<boolean> {
    return this.store.consumeCredits(userId, credits, `req_${randomUUID()}`);
  }

  topUp(userId: string, bdtAmount: number, referenceId: string): Promise<CreditBalance> {
    const mintedCredits = Math.trunc(Math.max(0, bdtAmount) * 100);
    return this.store.topUpCredits(userId, mintedCredits, referenceId);
  }
}

export class PersistentUsageService {
  constructor(private readonly supabase: ReturnType<typeof createSupabaseAdminClient>) { }

  async add(entry: Omit<UsageEvent, "id" | "createdAt">): Promise<UsageEvent> {
    const id = `usage_${randomUUID()}`;
    const { data, error } = await this.supabase.from("usage_events").insert({
      id,
      user_id: entry.userId,
      endpoint: entry.endpoint,
      model: entry.model,
      credits: entry.credits,
    }).select("created_at").single();
    if (error) {
      throw new Error(`failed to record usage: ${error.message}`);
    }
    return {
      id,
      userId: entry.userId,
      endpoint: entry.endpoint,
      model: entry.model,
      credits: entry.credits,
      createdAt: new Date(data.created_at as string | Date).toISOString(),
    };
  }

  async list(userId: string): Promise<UsageEvent[]> {
    const { data, error } = await this.supabase
      .from("usage_events")
      .select("id, user_id, endpoint, model, credits, created_at")
      .eq("user_id", userId)
      .order("created_at", { ascending: false })
      .limit(500);
    if (error) {
      throw new Error(`failed to read usage events: ${error.message}`);
    }
    return (data ?? []).map((row) => ({
      id: String(row.id),
      userId: String(row.user_id),
      endpoint: String(row.endpoint),
      model: String(row.model),
      credits: Number(row.credits),
      createdAt: new Date(row.created_at as string | Date).toISOString(),
    }));
  }
}

class PersistentPaymentService {
  constructor(
    private readonly store: BillingStore,
    private readonly credits: PersistentCreditService,
  ) { }

  async createIntent(input: { userId: string; provider: "bkash" | "sslcommerz"; bdtAmount: number }) {
    const intent: PersistentPaymentIntent = {
      intentId: `intent_${Math.random().toString(36).slice(2, 12)}`,
      userId: input.userId,
      provider: input.provider,
      bdtAmount: Math.max(0, input.bdtAmount),
      status: "initiated",
      mintedCredits: 0,
    };
    await this.store.createPaymentIntent(intent);
    return intent;
  }

  getIntent(intentId: string) {
    return this.store.getPaymentIntent(intentId);
  }

  async applyWebhook(payload: {
    provider: "bkash" | "sslcommerz";
    intent_id: string;
    provider_txn_id: string;
    verified: boolean;
  }) {
    if (!payload.verified) {
      await this.store.markPaymentCredited(payload.intent_id, 0, "failed");
      return this.store.getPaymentIntent(payload.intent_id);
    }

    // Call the PostgreSQL RPC for atomic idempotency, intent verification, and crediting
    const result = await this.store.claimPaymentIntent(payload.intent_id, payload.provider, payload.provider_txn_id);
    if (!result.success || !result.intent) {
      throw new Error(
        `payment intent claim failed: ${result.error ?? "unknown error"} ` +
        `(intent_id=${payload.intent_id}, provider=${payload.provider}, provider_txn_id=${payload.provider_txn_id})`,
      );
    }

    return result.intent;
  }

  async confirmDemoIntent(input: { intentId: string; providerTxnId?: string }) {
    return this.applyWebhook({
      provider: "bkash",
      intent_id: input.intentId,
      provider_txn_id: input.providerTxnId ?? `demo_txn_${randomUUID().slice(0, 10)}`,
      verified: true,
    });
  }
}

export class PersistentUserService {
  constructor(
    private readonly apiKeys: ApiKeyStore,
    private readonly supabaseUsers: SupabaseUserStore,
  ) { }

  async me(userId: string) {
    const user = await this.supabaseUsers.findById(userId);
    if (!user) {
      return undefined;
    }
    const keys = await this.apiKeys.list(userId);
    return {
      userId: user.userId,
      email: user.email,
      name: user.name,
      createdAt: user.createdAt,
      apiKeys: keys.map((key) => ({
        key_id: key.keyPrefix,
        revoked: key.revoked,
        scopes: key.scopes,
        createdAt: key.createdAt,
      })),
    };
  }

  async validateApiKey(key: string, requiredScope: string): Promise<string | null> {
    const apiKey = await this.apiKeys.resolve(key);
    if (!apiKey || !apiKey.scopes.includes(requiredScope)) {
      return null;
    }
    return apiKey.userId;
  }

  async resolveApiKey(key: string): Promise<{ userId: string; scopes: string[] } | null> {
    return this.apiKeys.resolve(key);
  }



  async createApiKey(userId: string, scopes: string[]) {
    const key = createApiKey();
    await this.apiKeys.create({ key, userId, scopes });
    return key;
  }

  revokeApiKey(userId: string, key: string): Promise<boolean> {
    return this.apiKeys.revoke(key, userId);
  }
}



class RuntimeAiService {
  constructor(
    private readonly models: ModelService,
    private readonly credits: PersistentCreditService,
    private readonly usage: PersistentUsageService,
    private readonly providerRegistry: ProviderRegistry,
    private readonly langfuse: LangfuseClient,
  ) { }

  async chatCompletions(userId: string, modelId: string | undefined, messages: ChatMessage[]) {
    const model = modelId && modelId !== "auto" ? this.models.findById(modelId) : this.models.pickDefault("chat");
    if (!model || model.capability !== "chat") {
      return { error: "unknown model", statusCode: 400 as const };
    }
    const creditsCost = model.creditsPerRequest;
    const consumed = await this.credits.consume(userId, creditsCost);
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }

    const text = messages.map((msg) => msg.content).join(" ").trim();
    await this.usage.add({ userId, endpoint: "/v1/chat/completions", model: model.id, credits: creditsCost });

    let providerResult;
    try {
      providerResult = await this.providerRegistry.chat(
        model.id,
        messages.map((message) => ({
          role: this.normalizeRole(message.role),
          content: message.content,
        })),
      );
    } catch (error) {
      return {
        error: error instanceof Error ? error.message : "provider unavailable",
        statusCode: 502 as const,
      };
    }

    await this.langfuse.trace({
      userId,
      model: model.id,
      provider: providerResult.providerUsed,
      endpoint: "/v1/chat/completions",
      credits: creditsCost,
      promptPreview: text.slice(0, 160),
    });

    return {
      statusCode: 200 as const,
      body: {
        id: `chatcmpl_${randomUUID().slice(0, 12)}`,
        object: "chat.completion",
        created: Math.floor(Date.now() / 1000),
        model: model.id,
        choices: [
          {
            index: 0,
            finish_reason: "stop",
            message: {
              role: "assistant",
              content: providerResult.content || `MVP response: ${text || "Your request was processed."}`,
            },
          },
        ],
      },
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": providerResult.providerUsed,
        "x-provider-model": providerResult.providerModel,
        "x-actual-credits": String(creditsCost),
      },
    };
  }

  async responses(userId: string, input: string) {
    const model = this.models.pickDefault("chat");
    const creditsCost = Math.max(4, Math.floor(model.creditsPerRequest * 0.75));
    const consumed = await this.credits.consume(userId, creditsCost);
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }
    await this.usage.add({ userId, endpoint: "/v1/responses", model: model.id, credits: creditsCost });

    let providerResult;
    try {
      providerResult = await this.providerRegistry.chat(model.id, [{ role: "user", content: input }]);
    } catch (error) {
      return {
        error: error instanceof Error ? error.message : "provider unavailable",
        statusCode: 502 as const,
      };
    }

    return {
      statusCode: 200 as const,
      body: {
        id: `resp_${randomUUID().slice(0, 12)}`,
        object: "response",
        model: model.id,
        output: [{ type: "text", text: providerResult.content || `MVP output: ${input || "No input provided."}` }],
      },
    };
  }

  async imageGeneration(userId: string, prompt: string) {
    const model = this.models.pickDefault("image");
    const creditsCost = model.creditsPerRequest;
    const consumed = await this.credits.consume(userId, creditsCost);
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }
    await this.usage.add({ userId, endpoint: "/v1/images/generations", model: model.id, credits: creditsCost });
    return {
      statusCode: 200 as const,
      headers: { "x-actual-credits": String(creditsCost) },
      body: {
        created: Math.floor(Date.now() / 1000),
        object: "list",
        data: [{ url: `https://example.invalid/generated/${encodeURIComponent(prompt || "image")}.png` }],
      },
    };
  }

  providersStatus() {
    return this.providerRegistry.status();
  }

  private normalizeRole(role: string): "system" | "user" | "assistant" {
    if (role === "system" || role === "assistant") {
      return role;
    }
    return "user";
  }
}

export type RuntimeServices = {
  env: AppEnv;
  models: ModelService;
  credits: PersistentCreditService;
  usage: PersistentUsageService;
  payments: PersistentPaymentService;
  users: PersistentUserService;
  authz: AuthorizationService;
  userSettings: UserSettingsService;
  supabaseAuth: SupabaseAuthService;
  ai: RuntimeAiService;
  rateLimiter: RedisRateLimiter;
  adapters: {
    bkash: BkashAdapter;
    sslcommerz: SslcommerzAdapter;
  };
};

export function createRuntimeServices(): RuntimeServices {
  const env = getEnv();
  const supabase = createSupabaseAdminClient(env);
  const models = new ModelService();
  const billingStore: BillingStore = new SupabaseBillingStore(supabase);
  const credits = new PersistentCreditService(billingStore);
  const usage = new PersistentUsageService(supabase);
  const payments = new PersistentPaymentService(billingStore, credits);
  const apiKeyStore: ApiKeyStore = new SupabaseApiKeyStore(supabase);
  const supabaseUserStore = new SupabaseUserStore(supabase);
  const users = new PersistentUserService(apiKeyStore, supabaseUserStore);
  const authz = new AuthorizationService(supabase);
  const userSettings = new UserSettingsService(supabaseUserStore);
  const supabaseAuth = new SupabaseAuthService(supabase);
  const langfuse = new LangfuseClient({
    enabled: env.langfuse.enabled,
    baseUrl: env.langfuse.baseUrl,
    publicKey: env.langfuse.publicKey,
    secretKey: env.langfuse.secretKey,
  });

  const providerModelMap: Record<ProviderName, string> = {
    mock: "mock-chat",
    ollama: env.providers.ollama.model,
    groq: env.providers.groq.model,
  };
  const providerRegistry = new ProviderRegistry({
    clients: [
      new OllamaProviderClient({
        baseUrl: env.providers.ollama.baseUrl,
        timeoutMs: env.providers.ollama.timeoutMs,
        maxRetries: env.providers.ollama.maxRetries,
      }),
      new GroqProviderClient({
        baseUrl: env.providers.groq.baseUrl,
        apiKey: env.providers.groq.apiKey,
        timeoutMs: env.providers.groq.timeoutMs,
        maxRetries: env.providers.groq.maxRetries,
      }),
      new MockProviderClient(),
    ],
    defaultProvider: "mock",
    modelProviderMap: {
      "fast-chat": "ollama",
      "smart-reasoning": "groq",
      "image-basic": "mock",
    },
    providerModelMap,
    fallbackOrder: {
      ollama: ["groq", "mock"],
      groq: ["ollama", "mock"],
      mock: [],
    },
    circuitBreaker: env.providers.circuitBreaker,
  });

  const ai = new RuntimeAiService(models, credits, usage, providerRegistry, langfuse);

  return {
    env,
    models,
    credits,
    usage,
    payments,
    users,
    authz,
    userSettings,
    supabaseAuth,
    ai,
    rateLimiter: new RedisRateLimiter(env.redisUrl, env.rateLimitPerMinute),
    adapters: {
      bkash: new BkashAdapter({
        webhookSecret: env.webhookSecrets.bkash,
        verifyEndpoint: env.bkash.verifyEndpoint,
        bearerToken: env.bkash.bearerToken,
      }),
      sslcommerz: new SslcommerzAdapter({
        webhookSecret: env.webhookSecrets.sslcommerz,
        validatorEndpoint: env.sslcommerz.validatorEndpoint,
        storeId: env.sslcommerz.storeId,
        storePassword: env.sslcommerz.storePassword,
      }),
    },
  };
}
