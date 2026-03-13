import { randomUUID } from "node:crypto";
import type { CreditBalance, UsageEvent, UsageSummary, UsageSummaryBucket, UsageDailyTrendPoint } from "../domain/types";
import { ModelService } from "../domain/model-service";
import { getEnv, type AppEnv } from "../config/env";
import { BkashAdapter, SslcommerzAdapter } from "./provider-adapters";
import { GroqProviderClient } from "../providers/groq-client";
import { MockProviderClient } from "../providers/mock-client";
import { OpenAIProviderClient } from "../providers/openai-client";
import { OllamaProviderClient } from "../providers/ollama-client";
import { ProviderRegistry } from "../providers/registry";
import type { ProviderName, ProviderReadinessStatus } from "../providers/types";
import type { PersistentApiKey, PersistentApiKeyEvent, PersistentPaymentIntent } from "../domain/types";
import { bdtToCredits } from "../domain/credits-conversion";
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
import { PaymentReconciliationService } from "./payment-reconciliation";
import { PaymentReconciliationScheduler } from "./payment-reconciliation-scheduler";

type ChatMessage = { role: string; content: string };
type ImageGenerationRequest = {
  model?: string;
  prompt: string;
  n?: number;
  size?: string;
  responseFormat?: "url" | "b64_json";
  user?: string;
};

type ApiKeyStore = {
  create(input: {
    key: string;
    userId: string;
    scopes: string[];
    nickname: string;
    expiresAt?: string;
  }): Promise<void>;
  resolve(key: string): Promise<{ userId: string; scopes: string[] } | null>;
  list(userId: string): Promise<PersistentApiKey[]>;
  revoke(key: string, userId: string): Promise<boolean>;
  revokeById(id: string, userId: string): Promise<boolean>;
  get(key: string): Promise<PersistentApiKey | undefined>;
  listEvents(userId: string): Promise<PersistentApiKeyEvent[]>;
};

type BillingStore = {
  getBalance(userId: string): Promise<CreditBalance>;
  consumeCredits(userId: string, credits: number, referenceId: string): Promise<boolean>;
  refundCredits(userId: string, credits: number, referenceId: string): Promise<CreditBalance>;
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
  listRecentSnapshot(since: Date): Promise<import("../domain/types").PaymentReconciliationSnapshot>;
};

class PersistentCreditService {
  constructor(private readonly store: BillingStore) { }

  getBalance(userId: string): Promise<CreditBalance> {
    return this.store.getBalance(userId);
  }

  consume(userId: string, credits: number, referenceId = `req_${randomUUID()}`): Promise<boolean> {
    return this.store.consumeCredits(userId, credits, referenceId);
  }

  refund(userId: string, credits: number, referenceId: string): Promise<CreditBalance> {
    return this.store.refundCredits(userId, credits, referenceId);
  }

  topUp(userId: string, bdtAmount: number, referenceId: string): Promise<CreditBalance> {
    const mintedCredits = bdtToCredits(bdtAmount);
    return this.store.topUpCredits(userId, mintedCredits, referenceId);
  }
}

export class PersistentUsageService {
  constructor(private readonly supabase: ReturnType<typeof createSupabaseAdminClient>) { }

  private buildWindowStart(windowDays: number): string {
    const start = new Date();
    start.setUTCHours(0, 0, 0, 0);
    start.setUTCDate(start.getUTCDate() - (windowDays - 1));
    return start.toISOString();
  }

  private buildSummary(events: UsageEvent[], windowDays: number): UsageSummary {
    const daily = new Map<string, UsageDailyTrendPoint>();
    const byModel = new Map<string, UsageSummaryBucket>();
    const byEndpoint = new Map<string, UsageSummaryBucket>();
    const now = new Date();
    now.setUTCHours(0, 0, 0, 0);

    for (let index = windowDays - 1; index >= 0; index -= 1) {
      const date = new Date(now);
      date.setUTCDate(date.getUTCDate() - index);
      const key = date.toISOString().slice(0, 10);
      daily.set(key, { date: key, requests: 0, credits: 0 });
    }

    const filteredEvents = events.filter((event) => daily.has(event.createdAt.slice(0, 10)));
    const totalRequests = filteredEvents.length;
    const totalCredits = filteredEvents.reduce((sum, event) => sum + event.credits, 0);

    for (const event of filteredEvents) {
      const dateKey = event.createdAt.slice(0, 10);
      const day = daily.get(dateKey);
      if (day) {
        day.requests += 1;
        day.credits += event.credits;
      }

      const modelBucket = byModel.get(event.model) ?? { key: event.model, requests: 0, credits: 0 };
      modelBucket.requests += 1;
      modelBucket.credits += event.credits;
      byModel.set(event.model, modelBucket);

      const endpointBucket = byEndpoint.get(event.endpoint) ?? { key: event.endpoint, requests: 0, credits: 0 };
      endpointBucket.requests += 1;
      endpointBucket.credits += event.credits;
      byEndpoint.set(event.endpoint, endpointBucket);
    }

    const sortBuckets = (entries: UsageSummaryBucket[]) => entries.sort((left, right) => {
      if (right.credits !== left.credits) {
        return right.credits - left.credits;
      }
      return left.key.localeCompare(right.key);
    });

    return {
      windowDays,
      totalRequests,
      totalCredits,
      daily: [...daily.values()],
      byModel: sortBuckets([...byModel.values()]),
      byEndpoint: sortBuckets([...byEndpoint.values()]),
    };
  }

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

  async listRecent(userId: string, windowDays: number): Promise<UsageEvent[]> {
    const { data, error } = await this.supabase
      .from("usage_events")
      .select("id, user_id, endpoint, model, credits, created_at")
      .eq("user_id", userId)
      .gte("created_at", this.buildWindowStart(windowDays))
      .order("created_at", { ascending: false });
    if (error) {
      throw new Error(`failed to read recent usage events: ${error.message}`);
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

  async listWithSummary(userId: string, windowDays = 7): Promise<{ data: UsageEvent[]; summary: UsageSummary }> {
    const [data, summaryRows] = await Promise.all([
      this.list(userId),
      this.listRecent(userId, windowDays),
    ]);
    return {
      data,
      summary: this.buildSummary(summaryRows, windowDays),
    };
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
    const events = await this.apiKeys.listEvents(userId);
    return {
      userId: user.userId,
      email: user.email,
      name: user.name,
      createdAt: user.createdAt,
      apiKeys: keys.map((key) => ({
        id: key.id,
        key_id: key.keyPrefix,
        nickname: key.nickname,
        status: key.status,
        revoked: key.revoked,
        scopes: key.scopes,
        createdAt: key.createdAt,
        expiresAt: key.expiresAt,
        revokedAt: key.revokedAt,
      })),
      apiKeyEvents: events,
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
  async createApiKey(userId: string, input: {
    nickname: string;
    scopes: string[];
    expiresAt?: string;
  }) {
    const key = createApiKey();
    await this.apiKeys.create({
      key,
      userId,
      nickname: input.nickname,
      scopes: input.scopes,
      expiresAt: input.expiresAt,
    });
    return {
      key,
      nickname: input.nickname,
      scopes: input.scopes,
      expiresAt: input.expiresAt,
    };
  }

  revokeApiKey(userId: string, id: string): Promise<boolean> {
    return this.apiKeys.revokeById(id, userId);
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

  async imageGeneration(userId: string, request: ImageGenerationRequest) {
    const model = request.model && request.model !== "auto" ? this.models.findById(request.model) : this.models.pickDefault("image");
    if (!model || model.capability !== "image") {
      return { error: "unknown model", statusCode: 400 as const };
    }
    const creditsCost = model.creditsPerRequest;
    const chargeReferenceId = `req_${randomUUID()}`;
    const consumed = await this.credits.consume(userId, creditsCost, chargeReferenceId);
    if (!consumed) {
      return { error: "insufficient credits", statusCode: 402 as const };
    }

    let providerResult;
    try {
      providerResult = await this.providerRegistry.imageGeneration(model.id, {
        prompt: request.prompt,
        n: request.n ?? 1,
        size: request.size,
        responseFormat: request.responseFormat ?? "url",
        user: request.user,
      });
    } catch {
      await this.credits.refund(userId, creditsCost, `refund_${chargeReferenceId}`);
      return {
        error: "provider unavailable",
        statusCode: 502 as const,
        headers: {
          "x-model-routed": model.id,
        },
      };
    }

    await this.usage.add({ userId, endpoint: "/v1/images/generations", model: model.id, credits: creditsCost });
    return {
      statusCode: 200 as const,
      headers: {
        "x-model-routed": model.id,
        "x-provider-used": providerResult.providerUsed,
        "x-provider-model": providerResult.providerModel,
        "x-actual-credits": String(creditsCost),
      },
      body: {
        created: providerResult.created,
        data: providerResult.data.map((entry) => ({
          ...(entry.url ? { url: entry.url } : {}),
          ...(entry.b64Json ? { b64_json: entry.b64Json } : {}),
        })),
      },
    };
  }

  providersStatus() {
    return this.providerRegistry.status();
  }

  providersMetrics() {
    return this.providerRegistry.metrics();
  }

  providersMetricsPrometheus() {
    return this.providerRegistry.metricsPrometheus();
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
  reconciliation: PaymentReconciliationService;
  reconciliationScheduler?: PaymentReconciliationScheduler;
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

function shouldWarnForStartupReadiness(result: ProviderReadinessStatus): boolean {
  return !result.ready && result.detail !== "disabled by config" && result.detail !== "not registered";
}

function startProviderReadinessCapture(providerRegistry: ProviderRegistry): void {
  void providerRegistry
    .captureStartupReadiness()
    .then((results) => {
      for (const [provider, result] of Object.entries(results) as Array<[ProviderName, ProviderReadinessStatus]>) {
        if (shouldWarnForStartupReadiness(result)) {
          console.warn(
            {
              provider,
              detail: result.detail,
            },
            "provider startup readiness warning",
          );
        }
      }
    })
    .catch((error) => {
      const reason = error instanceof Error ? error.message : String(error);
      console.error({ error: reason }, "provider startup readiness failed");
    });
}

export function createRuntimeServices(): RuntimeServices {
  const env = getEnv();
  const supabase = createSupabaseAdminClient(env);
  const models = new ModelService();
  const billingStore: BillingStore = new SupabaseBillingStore(supabase);
  const credits = new PersistentCreditService(billingStore);
  const usage = new PersistentUsageService(supabase);
  const payments = new PersistentPaymentService(billingStore, credits);
  const reconciliation = new PaymentReconciliationService(billingStore);
  const reconciliationScheduler = env.paymentReconciliation.enabled
    ? new PaymentReconciliationScheduler(
      reconciliation,
      {
        warn: (payload, message) => console.warn(message ?? "payment reconciliation warning", payload),
        error: (payload, message) => console.error(message ?? "payment reconciliation error", payload),
      },
      {
        intervalMs: env.paymentReconciliation.intervalMs,
        lookbackHours: env.paymentReconciliation.lookbackHours,
      },
    )
    : undefined;
  reconciliationScheduler?.start();
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
  const openaiConfig = env.providers.openai ?? {
    baseUrl: "https://api.openai.com/v1",
    apiKey: undefined,
    model: "gpt-image-1",
    timeoutMs: 4000,
    maxRetries: 1,
  };

  const providerModelMap: Record<ProviderName, string> = {
    mock: "mock-chat",
    ollama: env.providers.ollama.model,
    groq: env.providers.groq.model,
    openai: openaiConfig.model,
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
      new OpenAIProviderClient({
        baseUrl: openaiConfig.baseUrl,
        apiKey: openaiConfig.apiKey,
        timeoutMs: openaiConfig.timeoutMs,
        maxRetries: openaiConfig.maxRetries,
      }),
      new MockProviderClient(),
    ],
    defaultProvider: "mock",
    modelProviderMap: {
      "fast-chat": "ollama",
      "smart-reasoning": "groq",
      "image-basic": "openai",
    },
    providerModelMap,
    fallbackOrder: {
      ollama: ["groq", "mock"],
      groq: ["ollama", "mock"],
      openai: ["mock"],
      mock: [],
    },
    circuitBreaker: env.providers.circuitBreaker,
  });
  startProviderReadinessCapture(providerRegistry);

  const ai = new RuntimeAiService(models, credits, usage, providerRegistry, langfuse);

  return {
    env,
    models,
    credits,
    usage,
    payments,
    reconciliation,
    reconciliationScheduler,
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
