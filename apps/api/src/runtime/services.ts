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
import { PostgresStore, type PersistentPaymentIntent } from "./postgres-store";
import { RedisRateLimiter } from "./redis-rate-limiter";
import { createApiKey, hashPassword, verifyPassword } from "./security";
import { LangfuseClient } from "./langfuse";

type ChatMessage = { role: string; content: string };

class PersistentCreditService {
  constructor(private readonly store: PostgresStore) {}

  getBalance(userId: string): Promise<CreditBalance> {
    return this.store.getBalance(userId);
  }

  consume(userId: string, credits: number): Promise<boolean> {
    return this.store.consumeCredits(userId, credits, `req_${randomUUID()}`);
  }

  topUp(userId: string, bdtAmount: number, referenceId: string): Promise<CreditBalance> {
    return this.store.topUp(userId, bdtAmount, referenceId);
  }
}

class PersistentUsageService {
  constructor(private readonly store: PostgresStore) {}

  add(entry: Omit<UsageEvent, "id" | "createdAt">): Promise<UsageEvent> {
    return this.store.addUsage(entry.userId, entry.endpoint, entry.model, entry.credits);
  }

  list(userId: string): Promise<UsageEvent[]> {
    return this.store.listUsage(userId);
  }
}

class PersistentPaymentService {
  constructor(
    private readonly store: PostgresStore,
    private readonly credits: PersistentCreditService,
  ) {}

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
    const eventKey = `${payload.provider}:${payload.provider_txn_id}`;
    const inserted = await this.store.recordPaymentEvent(
      eventKey,
      payload.intent_id,
      payload.provider,
      payload.provider_txn_id,
      payload.verified,
    );
    if (!inserted) {
      return this.store.getPaymentIntent(payload.intent_id);
    }

    const intent = await this.store.getPaymentIntent(payload.intent_id);
    if (!intent) {
      return undefined;
    }

    if (intent.provider !== payload.provider) {
      await this.store.markPaymentCredited(intent.intentId, 0, "failed");
      return this.store.getPaymentIntent(payload.intent_id);
    }

    if (!payload.verified) {
      await this.store.markPaymentCredited(intent.intentId, 0, "failed");
      return this.store.getPaymentIntent(payload.intent_id);
    }

    if (intent.status === "credited") {
      return intent;
    }

    const mintedCredits = Math.trunc(intent.bdtAmount * 100);
    await this.credits.topUp(intent.userId, intent.bdtAmount, `payment_${intent.intentId}`);
    await this.store.markPaymentCredited(intent.intentId, mintedCredits, "credited");
    return this.store.getPaymentIntent(payload.intent_id);
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

class PersistentUserService {
  constructor(private readonly store: PostgresStore) {}

  async register(input: { email: string; password: string; name?: string }) {
    const existing = await this.store.findUserByEmail(input.email);
    if (existing) {
      return { error: "email already registered" as const };
    }

    const userId = `user_${randomUUID().slice(0, 12)}`;
    await this.store.createUser({
      userId,
      email: input.email,
      name: input.name,
      passwordHash: hashPassword(input.password),
    });
    const apiKey = createApiKey();
    await this.store.createApiKey({ key: apiKey, userId, scopes: ["chat", "image", "usage", "billing"] });
    return {
      userId,
      email: input.email,
      name: input.name,
      apiKey,
    };
  }

  async login(input: { email: string; password: string }) {
    const user = await this.store.findUserByEmail(input.email);
    if (!user || !verifyPassword(input.password, user.passwordHash)) {
      return { error: "invalid credentials" as const };
    }
    const apiKey = createApiKey();
    await this.store.createApiKey({ key: apiKey, userId: user.userId, scopes: ["chat", "image", "usage", "billing"] });
    return {
      userId: user.userId,
      email: user.email,
      name: user.name,
      apiKey,
    };
  }

  validateApiKey(key: string, requiredScope: string): Promise<string | null> {
    return this.store.validateApiKey(key, requiredScope);
  }

  async me(userId: string) {
    const user = await this.store.findUserById(userId);
    if (!user) {
      return undefined;
    }
    const keys = await this.store.listApiKeys(userId);
    return {
      userId: user.userId,
      email: user.email,
      name: user.name,
      createdAt: user.createdAt,
      apiKeys: keys.map((key) => ({
        key_id: key.key.slice(-8),
        revoked: key.revoked,
        scopes: key.scopes,
        createdAt: key.createdAt,
      })),
    };
  }

  async createApiKey(userId: string, scopes: string[]) {
    const key = createApiKey();
    await this.store.createApiKey({ key, userId, scopes });
    return key;
  }

  revokeApiKey(userId: string, key: string): Promise<boolean> {
    return this.store.revokeApiKey(key, userId);
  }
}

class RuntimeAiService {
  constructor(
    private readonly models: ModelService,
    private readonly credits: PersistentCreditService,
    private readonly usage: PersistentUsageService,
    private readonly providerRegistry: ProviderRegistry,
    private readonly langfuse: LangfuseClient,
  ) {}

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
  ai: RuntimeAiService;
  rateLimiter: RedisRateLimiter;
  adapters: {
    bkash: BkashAdapter;
    sslcommerz: SslcommerzAdapter;
  };
};

export function createRuntimeServices(): RuntimeServices {
  const env = getEnv();
  const store = new PostgresStore(env.postgresUrl);
  const models = new ModelService();
  const credits = new PersistentCreditService(store);
  const usage = new PersistentUsageService(store);
  const payments = new PersistentPaymentService(store, credits);
  const users = new PersistentUserService(store);
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
      new OllamaProviderClient({ baseUrl: env.providers.ollama.baseUrl }),
      new GroqProviderClient({ baseUrl: env.providers.groq.baseUrl, apiKey: env.providers.groq.apiKey }),
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
  });

  const ai = new RuntimeAiService(models, credits, usage, providerRegistry, langfuse);

  return {
    env,
    models,
    credits,
    usage,
    payments,
    users,
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
